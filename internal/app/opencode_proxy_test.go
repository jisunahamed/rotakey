package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenCodeProxyPreservesBodyAndStatus(t *testing.T) {
	body := `{"model":"big-pickle","max_tokens":321,"messages":[{"role":"user","content":"hello"}],"stream":true}`
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/chat/completions" || string(got) != body || r.Header.Get("Authorization") != "Bearer local-test-key" || r.Header.Get("X-Api-Key") != "" {
			t.Errorf("request changed or secret leaked: path=%s body=%s", r.URL.Path, got)
		}
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer proxy.Close()
	t.Setenv("OPENCODE_PROXY_URL", proxy.URL)
	t.Setenv("OPENCODE_PROXY_KEY", "local-test-key")
	client, err := upstreamClient(Provider{BaseURL: "https://opencode.ai/zen/v1", AuthHeader: "X-Api-Key"})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://opencode.ai/zen/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Api-Key", "original-secret")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 429 {
		t.Fatal("provider status changed")
	}
	if req.Header.Get("X-Api-Key") != "original-secret" {
		t.Fatal("caller headers mutated")
	}
}

func TestOpenCodeProxySelectionAndFailClosed(t *testing.T) {
	for _, raw := range []string{"https://opencode.ai/zen/v1", "https://opencode.ai/zen/v1/models", "https://opencode.ai"} {
		if !isOpenCodeZenURL(raw) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"https://opencode.ai.evil/zen/v1", "https://example.com/zen/v1", "http://opencode.ai/zen/v1", "https://opencode.ai/other", "https://opencode.ai:444/zen/v1"} {
		if isOpenCodeZenURL(raw) {
			t.Fatal(raw)
		}
	}
	t.Setenv("OPENCODE_PROXY_URL", "")
	t.Setenv("OPENCODE_PROXY_KEY", "")
	if _, err := upstreamClient(Provider{BaseURL: "https://opencode.ai/zen/v1"}); err == nil {
		t.Fatal("direct Zen call permitted without proxy")
	}
	if got := normalizeProviderCompatibilityURL("https://opencode.ai/zen/v1/models", "anthropic"); got != "https://opencode.ai/zen/v1" {
		t.Fatal(got)
	}
}
