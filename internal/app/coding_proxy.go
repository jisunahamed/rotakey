package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// A separate prefix for every upstream key prevents the proxy from rotating
// across keys behind Rotakey's quota reservations and accounting.
func codingProxyPrefix(baseURL, secret string) string {
	digest := sha256.Sum256([]byte(strings.TrimRight(baseURL, "/") + "\x00" + secret))
	return fmt.Sprintf("rk-%x", digest[:16])
}

type codingProxyTransport struct {
	base     http.RoundTripper
	provider Provider
	origin   *url.URL
	key      string
}

func newCodingProxyTransport(provider Provider) (http.RoundTripper, error) {
	origin, err := validateProviderURL(os.Getenv("CODING_PROXY_URL"), true)
	if err != nil || origin.Path != "" || os.Getenv("CODING_PROXY_KEY") == "" {
		return nil, errors.New("coding proxy is not configured on this server")
	}
	return &codingProxyTransport{base: safeTransport(true), provider: provider, origin: origin, key: os.Getenv("CODING_PROXY_KEY")}, nil
}

func (t *codingProxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	models := r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models")
	inference := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/responses")
	if !models && !inference {
		return nil, errors.New("coding proxy does not support this endpoint; direct fallback is disabled")
	}
	want, _ := url.Parse(t.provider.BaseURL)
	if r.URL.Host != want.Host || r.URL.Scheme != want.Scheme || (r.URL.Path != strings.TrimRight(want.Path, "/")+"/responses" && r.URL.Path != strings.TrimRight(want.Path, "/")+"/models") {
		return nil, errors.New("request is outside the configured coding provider")
	}
	header := valueOr(t.provider.AuthHeader, "Authorization")
	secret := r.Header.Get(header)
	if scheme := strings.TrimSpace(t.provider.AuthScheme); scheme != "" {
		secret = strings.TrimPrefix(secret, scheme+" ")
	}
	if secret == "" {
		return nil, errors.New("coding proxy requires a selected provider key")
	}
	var payload map[string]any
	model := ""
	if inference {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		_ = r.Body.Close()
		if json.Unmarshal(raw, &payload) != nil {
			return nil, errors.New("coding proxy requires a JSON Responses request")
		}
		model, _ = payload["model"].(string)
		if model == "" {
			return nil, errors.New("coding proxy requires a model")
		}
	}
	s, _ := r.Context().Value(admissionServerKey{}).(*Server)
	if s == nil {
		return nil, errors.New("coding proxy configuration context is unavailable")
	}
	if err := s.syncCodingProxy(r.Context(), t.provider, secret, model); err != nil {
		return nil, err
	}
	clone := r.Clone(r.Context())
	u := *t.origin
	u.Path = "/v1/responses"
	if models {
		u.Path = "/v1/models"
	}
	clone.URL = &u
	clone.Host = ""
	if inference {
		payload["model"] = codingProxyPrefix(t.provider.BaseURL, secret) + "/" + model
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		clone.Body = io.NopCloser(bytes.NewReader(encoded))
		clone.ContentLength = int64(len(encoded))
		clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(encoded)), nil }
	}
	clone.Header.Del(header)
	clone.Header.Del("X-Api-Key")
	clone.Header.Del("Content-Length")
	clone.Header.Set("Authorization", "Bearer "+t.key)
	response, err := t.base.RoundTrip(clone)
	if err != nil || !models || response.StatusCode/100 != 2 {
		return response, err
	}
	defer response.Body.Close()
	var catalog struct {
		Data []map[string]any `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&catalog) != nil {
		return nil, errors.New("coding proxy returned invalid model catalog")
	}
	prefix := codingProxyPrefix(t.provider.BaseURL, secret) + "/"
	items := []map[string]any{}
	for _, item := range catalog.Data {
		id, _ := item["id"].(string)
		if strings.HasPrefix(id, prefix) {
			item["id"] = strings.TrimPrefix(id, prefix)
			items = append(items, item)
		}
	}
	body, _ := json.Marshal(map[string]any{"object": "list", "data": items})
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-Length")
	response.Header.Set("Content-Type", "application/json")
	return response, nil
}

type codingProxyGroup struct {
	Name    string              `json:"name"`
	Prefix  string              `json:"prefix"`
	BaseURL string              `json:"base-url"`
	Retry   int                 `json:"request-retry"`
	Headers map[string]string   `json:"headers,omitempty"`
	Models  []map[string]string `json:"models"`
	Keys    []map[string]string `json:"keys"`
}

// The config is local to the VPS, mode 0600, and serialized for hot reload. CLIProxyAPI
// watches the mounted directory for changes. It never receives a pool it may
// independently rotate: each prefix is a group containing exactly one key.
func (s *Server) syncCodingProxy(ctx context.Context, current Provider, secret, model string) error {
	s.codingProxyMu.Lock()
	defer s.codingProxyMu.Unlock()
	path := os.Getenv("CODING_PROXY_CONFIG_PATH")
	token := os.Getenv("CODING_PROXY_KEY")
	if path == "" || token == "" {
		return errors.New("coding proxy configuration storage is unavailable")
	}
	groups := map[string]*codingProxyGroup{}
	add := func(provider Provider, key, model string) {
		prefix := codingProxyPrefix(provider.BaseURL, key)
		group := groups[prefix]
		if group == nil {
			group = &codingProxyGroup{Name: prefix, Prefix: prefix, BaseURL: provider.BaseURL, Headers: provider.ExtraHeaders, Keys: []map[string]string{{"api-key": key}}}
			groups[prefix] = group
		}
		for _, m := range group.Models {
			if m["name"] == model {
				return
			}
		}
		group.Models = append(group.Models, map[string]string{"name": model, "alias": model})
	}
	rows, err := s.db.Query(ctx, `SELECT p.base_url,p.extra_headers,c.secret_cipher,m.upstream_model
 FROM providers p JOIN credentials c ON c.provider_id=p.id JOIN model_routes m ON m.provider_id=p.id
 WHERE p.coding_proxy_enabled AND p.enabled AND p.api_format='openai' AND c.enabled AND m.enabled
 ORDER BY p.id,c.id,m.id`)
	if err != nil {
		return errors.New("coding proxy keys could not be loaded")
	}
	for rows.Next() {
		var base, upstream string
		var headers, cipher []byte
		if err = rows.Scan(&base, &headers, &cipher, &upstream); err != nil {
			break
		}
		var key []byte
		key, err = s.vault.Decrypt(cipher)
		if err != nil {
			break
		}
		provider := Provider{BaseURL: base}
		_ = json.Unmarshal(headers, &provider.ExtraHeaders)
		if audioModelType(upstream, "") == "" {
			add(provider, string(key), upstream)
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.New("coding proxy configuration could not be prepared")
	}
	if model != "" {
		add(current, secret, model)
	} else {
		// A new key is not in credentials yet. Give its verification request
		// the provider's saved model catalog before asking the proxy to load it.
		var names []string
		for _, group := range groups {
			if group.BaseURL == current.BaseURL {
				for _, item := range group.Models {
					names = append(names, item["name"])
				}
			}
		}
		for _, name := range names {
			add(current, secret, name)
		}
	}
	ordered := make([]*codingProxyGroup, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Models, func(i, j int) bool { return g.Models[i]["name"] < g.Models[j]["name"] })
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Prefix < ordered[j].Prefix })
	config := map[string]any{
		"config-version": 8, "server": map[string]any{"host": "", "port": 8317, "shutdown-timeout": 900},
		"management": map[string]any{"allow-remote": false, "secret-key": "", "disable-control-panel": true},
		"access":     map[string]any{"api-keys": []string{token}},
		"multimedia": map[string]any{"disable-image-generation": "passthrough"},
		"routing":    map[string]any{"force-model-prefix": true, "retry": map[string]any{"request-retry": 0, "max-retry-credentials": 1, "max-retry-interval": 0}},
		"oauth":      map[string]any{"auth-dir": "/tmp/auth"}, "api-keys": map[string]any{"codex": ordered},
	}
	encoded, _ := json.Marshal(config)
	previous, _ := os.ReadFile(path)
	if !bytes.Equal(encoded, previous) {
		// CLIProxyAPI watches the existing config inode. Replacing it with a
		// rename leaves its watcher on the old file, so keep the inode and write
		// the complete JSON in one operation. Its debounced loader retains the
		// previous config on read errors; readiness below confirms activation.
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return errors.New("coding proxy configuration is not writable")
		}
		if _, err = file.Write(encoded); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return errors.New("coding proxy configuration could not be saved")
		}

	}
	// Wait for a hot reload before using a newly introduced key/model prefix.
	wanted := codingProxyPrefix(current.BaseURL, secret) + "/" + model
	origin := strings.TrimRight(os.Getenv("CODING_PROXY_URL"), "/")
	client := &http.Client{Transport: safeTransport(true), Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, callErr := client.Do(req)
		if callErr == nil {
			var catalog struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&catalog)
			_ = response.Body.Close()
			for _, m := range catalog.Data {
				if m.ID == wanted || (model == "" && strings.HasPrefix(m.ID, wanted)) {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("coding proxy did not load the selected key/model")
		case <-time.After(150 * time.Millisecond):
		}
	}
}
