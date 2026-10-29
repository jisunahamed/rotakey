package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func queueFixture(t *testing.T, limit int, handler http.HandlerFunc) (*Server, string, string, func(context.Context, bool) *httptest.ResponseRecorder) {
	t.Helper()
	s := repairIntegrationServer(t)
	ctx := context.Background()
	id, _ := newID("queue_test")
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	secret, _ := s.vault.Encrypt([]byte("test-secret"))
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO providers(id,name,slug,base_url,allow_private_network,max_concurrent_requests,timeout_seconds) VALUES($1,$1,$1,$2,true,$3,1)`, []any{id, upstream.URL, limit}},
		{`INSERT INTO model_routes(id,provider_id,public_alias,upstream_model,capability_status) VALUES($1,$2,$1,'test-model','probe_verified')`, []any{id + "m", id}},
		{`INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) VALUES($1,$2,$1,$3,'test')`, []any{id + "k", id, secret}},
	} {
		if _, err := s.db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	call := func(ctx context.Context, stream bool) *httptest.ResponseRecorder {
		return dispatchQueueTest(t, s, id+"m", ctx, stream)
	}
	return s, id, id + "k", call
}

func dispatchQueueTest(t *testing.T, s *Server, alias string, ctx context.Context, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	routes, err := s.resolveRoutes(ctx, alias, routingModeModel)
	if err != nil {
		t.Error(err)
		return httptest.NewRecorder()
	}
	payload := map[string]any{"model": alias, "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "stream": stream}
	raw, _ := json.Marshal(payload)
	rid, _ := newID("req")
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw))).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.beginActiveRequest(rid, "chat", time.Now())
	defer s.activeRequests.Delete(rid)
	s.servePooled(rec, req, dispatchRequest{RequestID: rid, Started: time.Now(), Endpoint: "chat", PublicMode: messageModeChat, Alias: alias, Public: payload, Raw: raw, Stream: stream}, routes, "")
	return rec
}

const queueAnswer = `{"choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`

func waitQueueCondition(t *testing.T, fn func() bool) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("queue condition did not become true")
}

func TestIntegrationProviderQueueConcurrencyAndCancellation(t *testing.T) {
	var running, peak, calls atomic.Int32
	release := make(chan struct{})
	s, id, _, call := queueFixture(t, 2, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, queueAnswer)
	})
	// Use a long execution timeout so the test is about waiting and cancellation.
	_, _ = s.db.Exec(context.Background(), `UPDATE providers SET timeout_seconds=10 WHERE id=$1`, id)
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := call(ctx, false)
			if rec.Code != 200 && ctx.Err() == nil {
				t.Errorf("unexpected status %d: %s", rec.Code, rec.Body.String())
			}
		}()
	}
	waitQueueCondition(t, func() bool {
		return s.redis.ZCard(context.Background(), providerAdmissionKeys(id)[2]).Val() == 2 && s.redis.ZCard(context.Background(), providerAdmissionKeys(id)[0]).Val() == 3
	})
	if calls.Load() != 2 {
		t.Fatalf("calls=%d want 2", calls.Load())
	}
	cancel()
	wg.Wait()
	close(release)
	if peak.Load() > 2 {
		t.Fatalf("concurrency=%d", peak.Load())
	}
	if n := s.redis.ZCard(context.Background(), providerAdmissionKeys(id)[2]).Val(); n != 0 {
		t.Fatalf("running slot leak %d", n)
	}
	if n := s.redis.ZCard(context.Background(), providerAdmissionKeys(id)[0]).Val(); n != 0 {
		t.Fatalf("queue leak %d", n)
	}
}

func TestIntegrationProviderQueueRateLimitWaitAndRefresh(t *testing.T) {
	var calls atomic.Int32
	s, id, key, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, queueAnswer) })
	ctx := context.Background()
	// A known 429 hold must keep the request waiting, not spend quota or return
	// 429; its release after longer than provider timeout must still dispatch.
	_ = s.redis.Set(ctx, "cooldown:"+key, "429", 2*time.Second).Err()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(ctx, false) }()
	waitQueueCondition(t, func() bool { return s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 1 })
	if calls.Load() != 0 || s.redis.ZCard(ctx, providerAdmissionKeys(id)[2]).Val() != 0 {
		t.Fatal("waiter consumed upstream slot")
	}
	select {
	case rec := <-done:
		if rec.Code != 200 {
			t.Fatalf("wait became %d: %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reset did not wake request")
	}
	if calls.Load() != 1 {
		t.Fatalf("unexpected calls=%d", calls.Load())
	}
	// Existing key edits must be picked up while waiting.
	_ = s.redis.Set(ctx, "cooldown:"+key, "429", time.Minute).Err()
	go func() { done <- call(ctx, false) }()
	waitQueueCondition(t, func() bool { return s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 1 })
	_, err := s.db.Exec(ctx, `UPDATE credentials SET enabled=false WHERE id=$1`, key)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != 503 {
			t.Fatalf("disabled key wait became %d", rec.Code)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("disabled key remained queued")
	}
}

func TestRedisProviderAdmissionFIFOAtomicQuotaAndStaleRecovery(t *testing.T) {
	s := repairIntegrationServer(t)
	ctx := context.Background()
	id, _ := newID("admission")
	provider := Provider{ID: id, MaxConcurrentRequests: 1}
	constraint := limitConstraint{Key: "limit:" + id, Capacity: 1, WindowMS: 60000, Cost: 1}
	first, why, _, _, err := s.tryProviderAdmission(ctx, provider, "first", []limitConstraint{constraint}, 1)
	if err != nil || why != "" {
		t.Fatalf("first: %v %s", err, why)
	}
	defer first.lease.release()
	_, why, _, _, err = s.tryProviderAdmission(ctx, provider, "second", []limitConstraint{constraint}, 1)
	if err != nil || why != "concurrency_limit" {
		t.Fatalf("second: %v %s", err, why)
	}
	if got := s.redis.HGet(ctx, constraint.Key, "count").Val(); got != "1" {
		t.Fatalf("waiter spent quota: %s", got)
	}
	_, why, position, _, err := s.tryProviderAdmission(ctx, provider, "third", nil, 0)
	if err != nil || why != "queue_order" || position != 2 {
		t.Fatalf("FIFO: %v %s %d", err, why, position)
	}
	first.lease.release()
	_, why, _, _, err = s.tryProviderAdmission(ctx, provider, "second", []limitConstraint{constraint}, 1)
	if err != nil || why != "rate_limit" {
		t.Fatalf("quota not enforced: %v %s", err, why)
	}
	_ = s.redis.Del(ctx, constraint.Key).Err()
	second, why, _, _, err := s.tryProviderAdmission(ctx, provider, "second", []limitConstraint{constraint}, 1)
	if err != nil || why != "" {
		t.Fatalf("reset not admitted: %v %s", err, why)
	}
	second.lease.release()
	// Expired waiting-owner leases cannot strand the FIFO head after a crash.
	keys := providerAdmissionKeys(id)
	_ = s.redis.ZAdd(ctx, keys[1], redis.Z{Score: 1, Member: "third"}).Err()
	third, why, _, _, err := s.tryProviderAdmission(ctx, provider, "fresh", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("stale queue stranded: %v %s", err, why)
	}
	third.lease.release()
}

// Separate Server objects represent workers with independent in-memory request
// state but the same coordination storage, as in a multi-instance deployment.
func TestIntegrationProviderQueueSharedAcrossWorkersModelsAndKeys(t *testing.T) {
	var running, peak, calls atomic.Int32
	tickets := make(chan struct{}, 5)
	s, id, _, _ := queueFixture(t, 2, func(w http.ResponseWriter, r *http.Request) {
		n := running.Add(1)
		calls.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-tickets:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, queueAnswer)
	})
	ctx := context.Background()
	_, err := s.db.Exec(ctx, `UPDATE providers SET timeout_seconds=10 WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(ctx, `INSERT INTO model_routes(id,provider_id,public_alias,upstream_model,capability_status) VALUES($1,$2,$1,'second-model','probe_verified')`, id+"m2", id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(ctx, `INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) SELECT $1,provider_id,$1,secret_cipher,secret_suffix FROM credentials WHERE provider_id=$2`, id+"k2", id)
	if err != nil {
		t.Fatal(err)
	}
	worker := &Server{db: s.db, redis: s.redis, limiter: s.limiter, vault: s.vault, logger: s.logger, cfg: s.cfg}
	done := make(chan *httptest.ResponseRecorder, 5)
	for i := 0; i < 5; i++ {
		target := s
		alias := id + "m"
		if i%2 == 1 {
			target = worker
			alias = id + "m2"
		}
		go func() { done <- dispatchQueueTest(t, target, alias, ctx, false) }()
	}
	waitQueueCondition(t, func() bool {
		return s.redis.ZCard(ctx, providerAdmissionKeys(id)[2]).Val() == 2 && s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 3
	})
	if calls.Load() != 2 {
		t.Fatalf("dispatched %d calls before slots released", calls.Load())
	}
	for i := 0; i < 5; i++ {
		tickets <- struct{}{}
	}
	for i := 0; i < 5; i++ {
		select {
		case rec := <-done:
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), "answer") {
				t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
			}
		case <-time.After(6 * time.Second):
			t.Fatal("queue did not drain")
		}
	}
	if peak.Load() > 2 || calls.Load() != 5 {
		t.Fatalf("peak=%d calls=%d", peak.Load(), calls.Load())
	}
	if s.redis.ZCard(ctx, providerAdmissionKeys(id)[2]).Val() != 0 {
		t.Fatal("slot leaked")
	}
}

func TestIntegrationProviderQueueHoldsStreamingSlot(t *testing.T) {
	var calls atomic.Int32
	finish := make(chan struct{})
	s, id, _, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-finish:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		} else {
			fmt.Fprint(w, queueAnswer)
		}
	})
	ctx := context.Background()
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() { done <- call(ctx, true) }()
	waitQueueCondition(t, func() bool { return calls.Load() == 1 })
	go func() { done <- call(ctx, false) }()
	waitQueueCondition(t, func() bool { return s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 1 })
	if calls.Load() != 1 || s.redis.ZCard(ctx, providerAdmissionKeys(id)[2]).Val() != 1 {
		t.Fatal("stream released its slot before EOF")
	}
	close(finish)
	for i := 0; i < 2; i++ {
		select {
		case rec := <-done:
			if rec.Code != 200 {
				t.Fatalf("stream response=%d", rec.Code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("stream queue stuck")
		}
	}
}

func TestIntegrationProviderQueueDynamicLimitAndImpossibleRequest(t *testing.T) {
	s, id, key, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, queueAnswer) })
	ctx := context.Background()
	held, why, _, _, err := s.tryProviderAdmission(ctx, Provider{ID: id, MaxConcurrentRequests: 1}, "held", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("hold: %s %v", why, err)
	}
	defer held.lease.release()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(ctx, false) }()
	waitQueueCondition(t, func() bool { return s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 1 })
	_, err = s.db.Exec(ctx, `UPDATE providers SET max_concurrent_requests=2 WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != 200 {
			t.Fatalf("increased limit=%d", rec.Code)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("limit edit did not wake waiter")
	}
	_, err = s.db.Exec(ctx, `INSERT INTO rate_policies(credential_id,scope_key,tpm) VALUES($1,'*',1)`, key)
	if err != nil {
		t.Fatal(err)
	}
	rec := call(ctx, false)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "request_exceeds_limit") {
		t.Fatalf("impossible request=%d %s", rec.Code, rec.Body.String())
	}
	if s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() != 0 {
		t.Fatal("impossible request left in queue")
	}
}

func TestIntegrationProviderQueueUpstream429WaitsWithoutFallback(t *testing.T) {
	var calls, backupCalls atomic.Int32
	s, id, _, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"code":"rate_limit_exceeded"}}`)
			return
		}
		fmt.Fprint(w, queueAnswer)
	})
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backupCalls.Add(1); fmt.Fprint(w, queueAnswer) }))
	defer backup.Close()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO providers(id,name,slug,base_url,allow_private_network) VALUES($1,$1,$1,$2,true)`, []any{id + "backup", backup.URL}},
		{`INSERT INTO model_routes(id,provider_id,public_alias,upstream_model,capability_status) VALUES($1,$2,$3,'test-model','probe_verified')`, []any{id + "bm", id + "backup", id + "m"}},
		{`INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) SELECT $1,$2,$1,secret_cipher,secret_suffix FROM credentials WHERE provider_id=$3`, []any{id + "bk", id + "backup", id}},
		{`INSERT INTO provider_fallbacks(primary_id,alternative_id) VALUES($1,$2)`, []any{id, id + "backup"}},
	} {
		if _, err := s.db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	// A busy primary must wait even though the backup is ready.
	held, why, _, _, err := s.tryProviderAdmission(ctx, Provider{ID: id, MaxConcurrentRequests: 1}, "held", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("hold=%s %v", why, err)
	}
	defer held.lease.release()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(ctx, false) }()
	waitQueueCondition(t, func() bool { return s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 1 })
	if backupCalls.Load() != 0 || calls.Load() != 0 {
		t.Fatal("busy primary routed to backup")
	}
	held.lease.release()
	select {
	case rec := <-done:
		if rec.Code != 200 {
			t.Fatalf("429 became=%d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("429 did not resume")
	}
	if calls.Load() != 2 || backupCalls.Load() != 0 {
		t.Fatalf("primary=%d backup=%d", calls.Load(), backupCalls.Load())
	}
	// Ordinary pooled providers must also retain the selected primary across a
	// 429, rather than advancing the rotation on each rate-limit retry.
	if _, err := s.db.Exec(ctx, `DELETE FROM provider_fallbacks WHERE primary_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	calls.Store(0)
	_ = s.redis.Del(ctx, "rr:pool:"+id+"m").Err()
	if rec := call(ctx, false); rec.Code != 200 || calls.Load() != 2 || backupCalls.Load() != 0 {
		t.Fatalf("rate-limit retry changed primary: HTTP %d primary=%d other=%d", rec.Code, calls.Load(), backupCalls.Load())
	}
}

func TestIntegrationProviderQueueExecutionTimeoutReleasesSlot(t *testing.T) {
	s, id, _, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	rec := call(context.Background(), false)
	if rec.Code != 504 {
		t.Fatalf("timeout response=%d %s", rec.Code, rec.Body.String())
	}
	if s.redis.ZCard(context.Background(), providerAdmissionKeys(id)[2]).Val() != 0 {
		t.Fatal("timeout leaked slot")
	}
	var code string
	if err := s.db.QueryRow(context.Background(), `SELECT error_code FROM request_logs ORDER BY created_at DESC LIMIT 1`).Scan(&code); err != nil || code != "timeout" {
		t.Fatalf("timeout log=%s %v", code, err)
	}
}

func TestRedisProviderQueueLimitDecreaseAndLeaseLoss(t *testing.T) {
	s := repairIntegrationServer(t)
	ctx := context.Background()
	id, _ := newID("lower_limit")
	provider := Provider{ID: id, MaxConcurrentRequests: 2}
	a, why, _, _, err := s.tryProviderAdmission(ctx, provider, "a", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("a=%s %v", why, err)
	}
	defer a.lease.release()
	b, why, _, _, err := s.tryProviderAdmission(ctx, provider, "b", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("b=%s %v", why, err)
	}
	defer b.lease.release()
	provider.MaxConcurrentRequests = 1
	_, why, _, _, err = s.tryProviderAdmission(ctx, provider, "waiter", nil, 0)
	if err != nil || why != "concurrency_limit" {
		t.Fatalf("lowered limit=%s %v", why, err)
	}
	a.lease.release()
	_, why, _, _, err = s.tryProviderAdmission(ctx, provider, "waiter", nil, 0)
	if err != nil || why != "concurrency_limit" {
		t.Fatalf("one running at limit one=%s %v", why, err)
	}
	guarded, release, _ := guardProviderLease(ctx, b.lease)
	close(b.lease.lost)
	select {
	case <-guarded.Done():
	case <-time.After(time.Second):
		t.Fatal("lost ownership did not cancel upstream context")
	}
	release()
	waiter, why, _, _, err := s.tryProviderAdmission(ctx, provider, "waiter", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("released limit=%s %v", why, err)
	}
	waiter.lease.release()
}

func TestIntegrationProviderQueueAuxiliaryWaitsBeforeTimeout(t *testing.T) {
	s, id, _, _ := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, queueAnswer) })
	ctx := context.WithValue(context.Background(), admissionServerKey{}, s)
	provider, err := scanProvider(s.db.QueryRow(ctx, `SELECT `+providerColumns+` FROM providers WHERE id=$1`, id))
	if err != nil {
		t.Fatal(err)
	}
	held, why, _, _, err := s.tryProviderAdmission(ctx, provider, "held", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("hold=%s %v", why, err)
	}
	defer held.lease.release()
	done := make(chan error, 1)
	go func() {
		_, _, _, _, err := probeProviderModelWithSecret(ctx, provider, &modelInput{UpstreamModel: "test-model"}, []byte("test-secret"))
		done <- err
	}()
	waitQueueCondition(t, func() bool { return s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 1 })
	// More than the provider's 1s timeout can pass in the queue.
	timer := time.NewTimer(1200 * time.Millisecond)
	<-timer.C
	held.lease.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("auxiliary inference stuck")
	}
}

func TestIntegrationProviderQueueClearsInferenceHTTPDeadlines(t *testing.T) {
	s, id, _, _ := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, queueAnswer) })
	ctx := context.Background()
	routes, err := s.resolveRoutes(ctx, id+"m", routingModeModel)
	if err != nil {
		t.Fatal(err)
	}
	held, why, _, _, err := s.tryProviderAdmission(ctx, Provider{ID: id, MaxConcurrentRequests: 1}, "held", nil, 0)
	if err != nil || why != "" {
		t.Fatalf("hold=%s %v", why, err)
	}
	defer held.lease.release()
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		s.servePooled(w, r, dispatchRequest{Started: time.Now(), PublicMode: messageModeChat, Alias: id + "m", Public: payload, Raw: raw}, routes, "")
	})
	gateway := httptest.NewUnstartedServer(s.Handler())
	gateway.Config.ReadTimeout = 150 * time.Millisecond
	gateway.Config.WriteTimeout = 150 * time.Millisecond
	gateway.Start()
	defer gateway.Close()
	done := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 4 * time.Second}
		response, err := client.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"max_tokens":8,"messages":[{"role":"user","content":"hello"}]}`))
		if err == nil {
			defer response.Body.Close()
			body, e := io.ReadAll(response.Body)
			err = e
			if response.StatusCode != 200 || !strings.Contains(string(body), "answer") {
				err = fmt.Errorf("HTTP %d: %s", response.StatusCode, body)
			}
		}
		done <- err
	}()
	waitQueueCondition(t, func() bool { return s.redis.ZCard(ctx, providerAdmissionKeys(id)[0]).Val() == 1 })
	<-time.After(300 * time.Millisecond)
	held.lease.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRedisProviderAdmissionTwoKeyQuotaReset(t *testing.T) {
	s := repairIntegrationServer(t)
	ctx := context.Background()
	id, _ := newID("two_keys")
	provider := Provider{ID: id, MaxConcurrentRequests: 2}
	if now, err := s.redis.Time(ctx).Result(); err == nil && now.UnixMilli()%1000 > 700 {
		time.Sleep(time.Duration(1010-now.UnixMilli()%1000) * time.Millisecond)
	}
	limits := []limitConstraint{{Key: "limit:" + id + ":a", Capacity: 1, WindowMS: 1000, Cost: 1}, {Key: "limit:" + id + ":b", Capacity: 1, WindowMS: 1000, Cost: 1}}
	for i, c := range limits {
		held, why, _, _, err := s.tryProviderAdmission(ctx, provider, fmt.Sprint(i), []limitConstraint{c}, 0)
		if err != nil || why != "" {
			t.Fatalf("quota=%s %v", why, err)
		}
		held.lease.release()
	}
	for _, c := range limits {
		_, why, _, _, err := s.tryProviderAdmission(ctx, provider, "waiting", []limitConstraint{c}, 0)
		if err != nil || why != "rate_limit" {
			t.Fatalf("exhausted keys=%s %v", why, err)
		}
	}
	if n := s.redis.ZCard(ctx, providerAdmissionKeys(id)[2]).Val(); n != 0 {
		t.Fatalf("waiting reserved %d slots", n)
	}
	// Both quotas reset naturally. No manually clearing quota or moving to backup.
	waitQueueCondition(t, func() bool {
		held, why, _, _, err := s.tryProviderAdmission(ctx, provider, "waiting", []limitConstraint{limits[0]}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if why != "" {
			return false
		}
		held.lease.release()
		return true
	})
}
