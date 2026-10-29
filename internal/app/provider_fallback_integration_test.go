package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegrationProviderAlternativeDispatch(t *testing.T) {
	for _, mode := range []string{routingModeModel, routingModeProvider} {
		t.Run(mode, func(t *testing.T) {
			s := repairIntegrationServer(t)
			ctx := context.Background()
			id, _ := newID("fallback_test")
			alias := id + "alias"
			var primaryCalls, backupCalls atomic.Int32
			var primaryStatus atomic.Int32
			primaryStatus.Store(200)
			answer := `{"choices":[{"message":{"role":"assistant","content":"actual answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				primaryCalls.Add(1)
				code := int(primaryStatus.Load())
				w.Header().Set("Content-Type", "application/json")
				if code != 200 {
					w.Header().Set("Retry-After", "60")
					w.WriteHeader(code)
					fmt.Fprint(w, `{"error":{"message":"temporary upstream failure"}}`)
					return
				}
				fmt.Fprint(w, answer)
			}))
			defer primary.Close()
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				backupCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, answer)
			}))
			defer backup.Close()
			secret, _ := s.vault.Encrypt([]byte("test-secret"))
			// The backup is deliberately created first, with a different upstream model
			// identifier. Only the public alias determines the configured failover.
			for _, entry := range []struct{ id, url string }{{"b", backup.URL}, {"p", primary.URL}} {
				if _, err := s.db.Exec(ctx, `INSERT INTO providers(id,name,slug,base_url,allow_private_network) VALUES($1,$1,$1,$2,true)`, id+entry.id, entry.url); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(ctx, `INSERT INTO model_routes(id,provider_id,public_alias,upstream_model,capability_status) VALUES($1,$2,$3,$1,'probe_verified')`, id+entry.id+"m", id+entry.id, alias); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(ctx, `INSERT INTO credentials(id,provider_id,label,secret_cipher,secret_suffix) VALUES($1,$2,$1,$3,'test')`, id+entry.id+"k", id+entry.id, secret); err != nil {
					t.Fatal(err)
				}
			}
			save := func(from, to string) int {
				raw, _ := json.Marshal(map[string]string{"alternative_provider_id": to})
				req := httptest.NewRequest("PUT", "/api/admin/providers/"+from+"/fallback", strings.NewReader(string(raw)))
				req.SetPathValue("id", from)
				rec := httptest.NewRecorder()
				s.handleSetProviderFallback(rec, req)
				return rec.Code
			}
			if code := save(id+"p", id+"b"); code != 200 {
				t.Fatalf("save fallback=%d", code)
			}
			if code := save(id+"b", id+"p"); code != 400 {
				t.Fatalf("cycle allowed: %d", code)
			}
			call := func() {
				routes, err := s.resolveRoutes(ctx, alias, mode)
				if err != nil {
					t.Fatal(err)
				}
				payload := map[string]any{"model": alias, "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
				raw, _ := json.Marshal(payload)
				requestID, _ := newID("req")
				req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw)))
				rec := httptest.NewRecorder()
				s.servePooled(rec, req, dispatchRequest{RequestID: requestID, Started: time.Now(), Endpoint: "chat", PublicMode: messageModeChat, Alias: alias, Public: payload, Raw: raw}, routes, "")
				if rec.Code != 200 || !strings.Contains(rec.Body.String(), "actual answer") {
					t.Fatalf("dispatch=%d %s", rec.Code, rec.Body.String())
				}
			}
			for i := 0; i < 4; i++ {
				call()
			}
			if primaryCalls.Load() != 4 || backupCalls.Load() != 0 {
				t.Fatalf("backup took normal traffic: primary=%d backup=%d", primaryCalls.Load(), backupCalls.Load())
			}
			primaryStatus.Store(502)
			call()
			if backupCalls.Load() != 1 {
				t.Fatal("502 did not fail over")
			}
			// An upstream rate limit must also fail over. Cooldown remains authoritative;
			// the following call goes straight to backup without hammering the primary.
			primaryStatus.Store(200)
			_ = s.redis.Set(ctx, "cooldown:"+id+"pk", "429", 500*time.Millisecond).Err()
			call()
			if backupCalls.Load() != 1 {
				t.Fatal("primary recovery did not restore normal traffic")
			}
			if _, err := s.db.Exec(ctx, `UPDATE providers SET enabled=false WHERE id=$1`, id+"p"); err != nil {
				t.Fatal(err)
			}
			call()
			if backupCalls.Load() != 2 {
				t.Fatal("disabled primary stranded its alias")
			}
			if _, err := s.db.Exec(ctx, `UPDATE model_routes SET public_alias=$1 WHERE provider_id=$2`, alias+"backup-only", id+"b"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.resolveRoutes(ctx, alias+"backup-only", mode); err == nil {
				t.Fatal("backup-only alias became a normal route")
			}
		})
	}
}
