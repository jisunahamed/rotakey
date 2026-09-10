package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Both URLs must point at disposable integration services. Each test owns a
// freshly created PostgreSQL schema; it never changes existing application rows.
func repairIntegrationServer(t *testing.T) *Server {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	redis := integrationRedis(t)
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "repair_test_" + time.Now().Format("20060102150405000000000")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		owner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); owner.Close() })
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = runMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	vault, err := newVault([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	return &Server{db: pool, redis: redis, limiter: newLimiter(redis), vault: vault, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), cfg: Config{MaxResponseBytes: 16 << 20, CaptureBytes: 1 << 20}}
}

func TestIntegrationRepairPolicyVersionConflict(t *testing.T) {
	s := repairIntegrationServer(t)
	p := s.repairPolicy(context.Background())
	if p.Enabled || p.Version != 1 || p.DailyTokens != 100000 {
		t.Fatalf("bad defaults: %+v", p)
	}
	save := func(policy RepairPolicy) int {
		raw, _ := json.Marshal(policy)
		recorder := httptest.NewRecorder()
		s.handleRepairPolicy(recorder, httptest.NewRequest("PUT", "/api/admin/repair/policy", strings.NewReader(string(raw))))
		return recorder.Code
	}
	if code := save(p); code != 200 {
		t.Fatalf("first update=%d", code)
	}
	if code := save(p); code != 409 {
		t.Fatalf("stale update=%d", code)
	}
	if s.repairPolicy(context.Background()).Version != 2 {
		t.Fatal("version lost while decoding JSON policy")
	}
}

func TestIntegrationRepairAgentVerifiesAndLearns(t *testing.T) {
	s := repairIntegrationServer(t)
	var diagnosisCalls, targetCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		if payload["model"] == "repair-coder" {
			diagnosisCalls.Add(1)
			proposal := `{"diagnosis":"The provider requires lower temperature","action":"set_parameter","parameter":"temperature","value":0.5,"expected_result":"Request accepted"}`
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": proposal}}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 40}})
			return
		}
		targetCalls.Add(1)
		if payload["temperature"] != float64(0.5) {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"temperature must be <= 0.5"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"actual answer"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer upstream.Close()
	ctx := context.Background()
	id, _ := newID("repair_test")
	providerID, routeID, agentID, credentialID := id+"p", id+"m", id+"a", id+"c"
	secret, _ := s.vault.Encrypt([]byte("test-key"))
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO providers(id,name,slug,base_url,allow_private_network) VALUES($1,'test',$1,$2,true)`, []any{providerID, upstream.URL}},
		{`INSERT INTO model_routes(id,provider_id,public_alias,upstream_model,capability_status) VALUES($1,$2,$1,'target','probe_verified'),($3,$2,$3,'repair-coder','probe_verified')`, []any{routeID, providerID, agentID}},
		{`INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) VALUES($1,$2,'test',$3,'test')`, []any{credentialID, providerID, secret}},
	} {
		if _, err := s.db.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	p := defaultRepairPolicy()
	p.Enabled = true
	p.Mode = "auto"
	p.ModelID = agentID
	raw, _ := json.Marshal(p)
	if _, err := s.db.Exec(ctx, `UPDATE repair_policy SET policy=$1`, raw); err != nil {
		t.Fatal(err)
	}
	route, err := s.repairRoute(ctx, routeID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.redis.Del(ctx, repairRuleKey(route, "chat"), "rr:pool:"+routeID)
	call := func() *httptest.ResponseRecorder {
		payload := map[string]any{"model": routeID, "temperature": 1, "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "keep this prompt"}}}
		raw, _ := json.Marshal(payload)
		requestID, _ := newID("req")
		request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw)))
		recorder := httptest.NewRecorder()
		s.servePooled(recorder, request, dispatchRequest{RequestID: requestID, Started: time.Now(), Endpoint: "chat", PublicMode: messageModeChat, Alias: routeID, Public: payload, Raw: raw}, []routeRuntime{route}, "")
		return recorder
	}
	first := call()
	if first.Code != 200 || !strings.Contains(first.Body.String(), "actual answer") {
		t.Fatalf("first response=%d %s", first.Code, first.Body.String())
	}
	if targetCalls.Load() != 2 || diagnosisCalls.Load() != 1 {
		t.Fatalf("calls target=%d diagnosis=%d", targetCalls.Load(), diagnosisCalls.Load())
	}
	second := call()
	if second.Code != 200 || targetCalls.Load() != 3 || diagnosisCalls.Load() != 1 {
		t.Fatalf("learned repair not reused: status=%d target=%d diagnosis=%d", second.Code, targetCalls.Load(), diagnosisCalls.Load())
	}
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM repair_incidents WHERE status='verified'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("verified incident count=%d error=%v", count, err)
	}
	// Turning the feature off must stop the cached repair as well.
	p.Enabled = false
	raw, _ = json.Marshal(p)
	_, _ = s.db.Exec(ctx, `UPDATE repair_policy SET policy=$1,version=version+1`, raw)
	third := call()
	if third.Code != 400 {
		t.Fatalf("disabled agent still repaired: %d", third.Code)
	}
}
