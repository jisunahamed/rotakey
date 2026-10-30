package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type earlyCommentWriter struct {
	bytes.Buffer
	flushed chan struct{}
}

func (w *earlyCommentWriter) Flush() {
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}

func TestStreamInspectionRelaysKeepalivesBeforeFirstData(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	comments := &earlyCommentWriter{flushed: make(chan struct{}, 1)}
	type result struct {
		reader io.Reader
		mode   string
	}
	done := make(chan result, 1)
	go func() { source, mode := successfulStreamSource(r, comments); done <- result{source, mode} }()
	prefix := ": provider is reasoning\n\n"
	if _, err := io.WriteString(w, prefix); err != nil {
		t.Fatal(err)
	}
	select {
	case <-comments.flushed:
	case <-time.After(time.Second):
		t.Fatal("keepalive withheld while waiting for content")
	}
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: [DONE]\n\n"
	go func() { _, _ = io.WriteString(w, body); _ = w.Close() }()
	out := <-done
	rest, err := io.ReadAll(out.reader)
	if err != nil || out.mode != messageModeChat || comments.String()+string(rest) != prefix+body {
		t.Fatalf("stream bytes changed or duplicated: mode=%s err=%v", out.mode, err)
	}
}

func TestStreamHeadersArriveBeforeFirstUpstreamData(t *testing.T) {
	s, candidate, plan := chatAttempt(t, "https://example.com")
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}
		s.streamAttempt(writer, request, dispatchRequest{Stream: true, PublicMode: messageModeChat}, candidate, plan, response, reservation{}, AttemptRecord{}, "")
	}))
	defer gateway.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(gateway.URL)
	if err != nil {
		t.Fatalf("headers blocked on first model delta: %v", err)
	}
	defer response.Body.Close()
	go func() { _, _ = io.WriteString(w, "data: [DONE]\n\n"); _ = w.Close() }()
	got, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(got) != "data: [DONE]\n\n" {
		t.Fatalf("bad stream %q: %v", got, err)
	}
}

func TestCancelledRequestsNeverStartRepairDiagnosis(t *testing.T) {
	rs := &repairSession{}
	if rs.handle(context.Background(), nil, upstreamPlan{}, &attemptOutcome{ErrorCode: "cancelled"}, nil) {
		t.Fatal("cancelled call triggered repair")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rs.handle(ctx, nil, upstreamPlan{}, &attemptOutcome{Status: 502}, nil) {
		t.Fatal("disconnected call triggered repair")
	}
}

func TestProviderTemplateFailureDoesNotBuyRepairDiagnosis(t *testing.T) {
	rs := &repairSession{policy: RepairPolicy{Enabled: true}, incidents: map[string]*RepairIncident{}, backgroundEvidence: map[string][]byte{}}
	candidate := routeCandidate{Route: routeRuntime{Model: ModelRoute{ID: "model"}}}
	outcome := &attemptOutcome{Status: 500, ErrorCode: "upstream_error", ErrorMessage: "Failed to apply prompt template", Record: AttemptRecord{Error: "upstream_error", ErrorMessage: "Failed to apply prompt template"}}
	if rs.handle(context.Background(), &candidate, upstreamPlan{Path: "/chat/completions"}, outcome, nil) || len(rs.backgroundEvidence) != 0 || len(rs.incidents) != 1 {
		t.Fatal("unrepairable provider configuration triggered diagnosis")
	}
	for _, incident := range rs.incidents {
		if incident.Status != "provider_configuration_required" {
			t.Fatalf("wrong diagnosis: %s", incident.Status)
		}
	}
}

func TestIntegrationTemplateFailureDoesNotRotateAllKeys(t *testing.T) {
	var calls atomic.Int32
	s, id, _, call := queueFixture(t, 0, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"type":"Internal Server Error","message":"Failed to apply prompt template: object is not callable"}}`)
	})
	for i := 0; i < 4; i++ {
		_, err := s.db.Exec(context.Background(), `INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) SELECT $1,provider_id,$1,secret_cipher,secret_suffix FROM credentials WHERE id=$2`, fmt.Sprintf("%s-extra-%d", id, i), id+"k")
		if err != nil {
			t.Fatal(err)
		}
	}
	rec := call(context.Background(), false)
	if rec.Code != 500 || calls.Load() != 1 {
		t.Fatalf("deterministic error retried: HTTP %d calls=%d", rec.Code, calls.Load())
	}
	if s.redis.Exists(context.Background(), "failures:"+id+"k").Val() != 0 {
		t.Fatal("model template error struck a key")
	}
}

func TestIntegrationProviderWorkerLimitWaitsInsteadOfKeyStorm(t *testing.T) {
	var calls atomic.Int32
	s, id, _, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"message":"ResourceExhausted: Worker local total request limit reached (37/32)"}}`)
			return
		}
		fmt.Fprint(w, queueAnswer)
	})
	started := time.Now()
	rec := call(context.Background(), false)
	if rec.Code != 200 || calls.Load() != 2 || time.Since(started) < 900*time.Millisecond {
		t.Fatalf("capacity not queued: status=%d calls=%d duration=%v", rec.Code, calls.Load(), time.Since(started))
	}
	if s.redis.Exists(context.Background(), "failures:"+id+"k").Val() != 0 {
		t.Fatal("busy provider struck its key")
	}
	var recorded int
	if err := s.db.QueryRow(context.Background(), `SELECT jsonb_array_length(attempts) FROM request_logs WHERE model_alias=$1`, id+"m").Scan(&recorded); err != nil || recorded != 2 {
		t.Fatalf("capacity attempt missing from audit trail: %d %v", recorded, err)
	}
}

func TestIntegrationCancellationHasNoProviderStrikeOrRepair(t *testing.T) {
	started := make(chan struct{}, 1)
	s, id, key, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { call(ctx, false); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("call not sent")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not finish")
	}
	if s.redis.Exists(context.Background(), "failures:"+key, "cooldown:"+key).Val() != 0 {
		t.Fatal("cancel changed key health")
	}
	var attemptError string
	if err := s.db.QueryRow(context.Background(), `SELECT attempts->0->>'error' FROM request_logs WHERE model_alias=$1`, id+"m").Scan(&attemptError); err != nil || attemptError != "client_cancelled" {
		t.Fatalf("cancel marked provider connection error: %s %v", attemptError, err)
	}
	var repairs int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM repair_incidents`).Scan(&repairs); err != nil || repairs != 0 {
		t.Fatalf("cancel created %d repair incidents: %v", repairs, err)
	}
}

func TestStreamErrorRetainsProviderReasonWithSecretRedacted(t *testing.T) {
	s, candidate, plan := chatAttempt(t, "https://example.com")
	candidate.Credential.Secret = []byte("sensitive-key")
	body := "data: {\"error\":{\"message\":\"sensitive-key: conversation is too long\"}}\n\n"
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	rec := httptest.NewRecorder()
	out := s.streamAttempt(rec, httptest.NewRequest("POST", "/v1/messages", nil), dispatchRequest{Stream: true, PublicMode: messageModeAnthropic}, candidate, plan, response, reservation{}, AttemptRecord{}, "")
	if out.ErrorCode != "upstream_stream_error" || !strings.Contains(out.ErrorMessage, "conversation is too long") || strings.Contains(out.ErrorMessage, "sensitive-key") || rec.Body.String() != body {
		t.Fatalf("lost reason, secret leaked, or response altered: %+v", out)
	}
}

func TestIntegrationIdenticalServerFailuresStopAfterTwoKeys(t *testing.T) {
	for _, recoverOnSecond := range []bool{false, true} {
		t.Run(fmt.Sprint(recoverOnSecond), func(t *testing.T) {
			var calls atomic.Int32
			s, id, _, call := queueFixture(t, 0, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 2 && recoverOnSecond {
					fmt.Fprint(w, queueAnswer)
					return
				}
				w.WriteHeader(500)
				fmt.Fprint(w, `{"error":{"message":"Internal server error"}}`)
			})
			for i := 0; i < 9; i++ {
				if _, err := s.db.Exec(context.Background(), `INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) SELECT $1,provider_id,$1,secret_cipher,secret_suffix FROM credentials WHERE id=$2`, fmt.Sprintf("%s-extra-%d", id, i), id+"k"); err != nil {
					t.Fatal(err)
				}
			}
			rec := call(context.Background(), false)
			if calls.Load() != 2 || (recoverOnSecond && rec.Code != 200) || (!recoverOnSecond && rec.Code != 500) {
				t.Fatalf("wrong failure budget: HTTP %d calls=%d", rec.Code, calls.Load())
			}
			var attempts int
			if err := s.db.QueryRow(context.Background(), `SELECT jsonb_array_length(attempts) FROM request_logs WHERE model_alias=$1`, id+"m").Scan(&attempts); err != nil || attempts != 2 {
				t.Fatalf("attempt evidence missing: count=%d err=%v", attempts, err)
			}
		})
	}
}

func TestRepeatedFailureBudgetScopesPayloadAndRoute(t *testing.T) {
	candidate := routeCandidate{Route: routeRuntime{Model: ModelRoute{ID: "route"}}}
	plan := upstreamPlan{Path: "/chat/completions", Encoded: []byte(`{"max_tokens":8}`)}
	outcome := attemptOutcome{Status: 500, Record: AttemptRecord{ErrorMessage: "Internal server error"}}
	key := repeatedFailureKey(candidate, plan, outcome)
	plan.Encoded = []byte(`{"max_tokens":16}`)
	if key == "" || repeatedFailureKey(candidate, plan, outcome) == key {
		t.Fatal("a repaired payload shares the old failure budget")
	}
	plan.Encoded = []byte(`{"max_tokens":8}`)
	candidate.Route.Model.ID = "backup-route"
	if repeatedFailureKey(candidate, plan, outcome) == key {
		t.Fatal("backup shares primary failure budget")
	}
	for _, status := range []int{200, 401, 403, 429} {
		outcome.Status = status
		if repeatedFailureKey(candidate, plan, outcome) != "" {
			t.Fatalf("status %d incorrectly consumes 5xx budget", status)
		}
	}
}

func TestProviderCapacityDoesNotBuyDiagnosis(t *testing.T) {
	rs := &repairSession{policy: RepairPolicy{Enabled: true}, incidents: map[string]*RepairIncident{}, backgroundEvidence: map[string][]byte{}}
	candidate := routeCandidate{Route: routeRuntime{Model: ModelRoute{ID: "route"}}}
	outcome := &attemptOutcome{Done: true, Status: 200, ErrorCode: "upstream_stream_error", ErrorMessage: "Concurrency limit exceeded for user, please retry later", Record: AttemptRecord{Error: "upstream_stream_error"}}
	if rs.handle(context.Background(), &candidate, upstreamPlan{}, outcome, nil) || len(rs.backgroundEvidence) != 0 {
		t.Fatal("capacity rejection bought a diagnosis or replay")
	}
	for _, incident := range rs.incidents {
		if incident.Status != "waiting_for_provider_capacity" {
			t.Fatal("capacity evidence not retained")
		}
	}
}

func TestIntegrationStreamCapacityBackoffPreservesResponse(t *testing.T) {
	var calls atomic.Int32
	body := "data: {\"error\":{\"message\":\"Concurrency limit exceeded for user, please retry later\"}}\n\n"
	s, id, _, call := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Retry-After", "1")
			fmt.Fprint(w, body)
			return
		}
		fmt.Fprint(w, queueAnswer)
	})
	rec := call(context.Background(), true)
	if rec.Code != 200 || rec.Body.String() != body || calls.Load() != 1 {
		t.Fatalf("started provider response replayed or changed: HTTP %d calls=%d body=%s", rec.Code, calls.Load(), rec.Body.String())
	}
	if ttl := s.redis.PTTL(context.Background(), providerBackoffKey(id)).Val(); ttl <= 0 {
		t.Fatal("stream capacity did not set provider backpressure")
	}
	var delivered int64
	if err := s.db.QueryRow(context.Background(), `SELECT (attempts->0->>'response_bytes')::bigint FROM request_logs WHERE model_alias=$1`, id+"m").Scan(&delivered); err != nil || delivered != int64(len(body)) {
		t.Fatalf("missing actual forwarded byte evidence: %d %v", delivered, err)
	}
	started := time.Now()
	rec = call(context.Background(), false)
	if rec.Code != 200 || calls.Load() != 2 || time.Since(started) < 500*time.Millisecond {
		t.Fatalf("later call bypassed shared backoff: HTTP %d calls=%d duration=%v", rec.Code, calls.Load(), time.Since(started))
	}
}

func TestIntegrationUserConcurrencyWaitsAcrossKeys(t *testing.T) {
	var calls atomic.Int32
	s, id, _, call := queueFixture(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"Concurrency limit exceeded for user, please retry later"}}`)
			return
		}
		fmt.Fprint(w, queueAnswer)
	})
	if _, err := s.db.Exec(context.Background(), `INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) SELECT $1,provider_id,$1,secret_cipher,secret_suffix FROM credentials WHERE id=$2`, id+"extra", id+"k"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	rec := call(context.Background(), false)
	if rec.Code != 200 || calls.Load() != 2 || time.Since(started) < 900*time.Millisecond {
		t.Fatalf("sibling key bypassed user concurrency: HTTP %d calls=%d duration=%v", rec.Code, calls.Load(), time.Since(started))
	}
}
