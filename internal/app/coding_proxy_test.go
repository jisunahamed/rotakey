package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCodingProxyPrefixSeparatesKeysAndOrigins(t *testing.T) {
	a := codingProxyPrefix("https://example.com/v1", "key-a")
	if a != codingProxyPrefix("https://example.com/v1/", "key-a") || a == codingProxyPrefix("https://example.com/v1", "key-b") || a == codingProxyPrefix("https://other.example/v1", "key-a") {
		t.Fatal("incorrect key affinity")
	}
	if providerInferenceEndpoint(Provider{APIFormat: "openai", CodingProxyEnabled: true, InferenceEndpoint: "chat"}) != "responses" {
		t.Fatal("proxy must use Responses")
	}
}

func TestCodingProxyFailsClosedWithoutConfiguration(t *testing.T) {
	t.Setenv("CODING_PROXY_URL", "")
	t.Setenv("CODING_PROXY_KEY", "")
	_, err := upstreamClient(Provider{APIFormat: "openai", BaseURL: "https://example.com/v1", CodingProxyEnabled: true})
	if err == nil {
		t.Fatal("missing proxy silently allowed a direct connection")
	}
}

func TestIntegrationCodingProxyNormalCallsAndModelChecks(t *testing.T) {
	var direct, proxied atomic.Int32
	s, id, _, _ := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) { direct.Add(1); w.WriteHeader(500) })
	_, err := s.db.Exec(context.Background(), `UPDATE providers SET coding_proxy_enabled=true WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("CODING_PROXY_CONFIG_PATH", configPath)
	t.Setenv("CODING_PROXY_KEY", "internal-test-key")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer internal-test-key" {
			t.Error("upstream key leaked to proxy request header")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			raw, _ := os.ReadFile(configPath)
			var config struct {
				APIKeys struct {
					Codex []codingProxyGroup `json:"codex"`
				} `json:"api-keys"`
			}
			_ = json.Unmarshal(raw, &config)
			items := []map[string]string{}
			for _, group := range config.APIKeys.Codex {
				for _, m := range group.Models {
					items = append(items, map[string]string{"id": group.Prefix + "/" + m["name"]})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Error("unexpected proxy path", r.URL.Path)
		}
		proxied.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasSuffix(body["model"].(string), "/test-model") {
			t.Error("missing key-affinity model prefix", body["model"])
		}
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","status":"completed","model":"test-model","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`))
	}))
	defer proxy.Close()
	t.Setenv("CODING_PROXY_URL", proxy.URL)
	ctx := context.WithValue(context.Background(), admissionServerKey{}, s)
	routes, err := s.resolveRoutes(ctx, id+"m", routingModeModel)
	if err != nil {
		t.Fatal(err)
	}
	// Actual pooled client request is translated to Responses and proxied.
	w := dispatchQueueTest(t, s, id+"m", ctx, false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// The exact model-section "Check it" path uses the same proxy transport.
	input := modelInput{UpstreamModel: "test-model", SupportsChat: true}
	status, _, _, err := s.probeProviderModel(ctx, id, &input)
	if err != nil || status != "probe_verified" {
		t.Fatal(status, err)
	}
	provider := routes[0].Provider
	client, err := upstreamClient(provider)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", provider.BaseURL+"/models", nil)
	applyProviderHeaders(req, provider, []byte("test-secret"))
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(response.Body).Decode(&catalog)
	if len(catalog.Data) != 1 || catalog.Data[0].ID != "test-model" {
		t.Fatal("catalog prefix isolation failed", catalog)
	}
	if direct.Load() != 0 || proxied.Load() != 2 {
		t.Fatal("direct fallback or missing proxy call", direct.Load(), proxied.Load())
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("proxy config is not private", err)
	}
}
