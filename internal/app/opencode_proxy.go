package app

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func isOpenCodeZenURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "opencode.ai") || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	path := strings.TrimRight(u.Path, "/")
	return path == "" || path == "/zen" || path == "/zen/v1" || strings.HasPrefix(path, "/zen/v1/")
}

// Only recognized Zen URLs go to the administrator-configured sidecar.
// Provider keys are replaced, never forwarded to a different host.
type openCodeProxyTransport struct {
	base       http.RoundTripper
	url        *url.URL
	key        string
	authHeader string
}

func (t *openCodeProxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !isOpenCodeZenURL(r.URL.String()) || !strings.HasPrefix(r.URL.Path, "/zen/v1/") {
		return nil, errors.New("OpenCode request is outside the proxy route")
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = t.url.Scheme, t.url.Host
	u.Path = strings.TrimRight(t.url.Path, "/") + strings.TrimPrefix(r.URL.Path, "/zen")
	u.RawPath = ""
	clone.URL, clone.Host = &u, ""
	clone.Header.Del(t.authHeader)
	clone.Header.Del("X-Api-Key")
	clone.Header.Set("Authorization", "Bearer "+t.key)
	return t.base.RoundTrip(clone)
}

func openCodeTransport(provider Provider) (http.RoundTripper, error) {
	proxyURL, key := os.Getenv("OPENCODE_PROXY_URL"), os.Getenv("OPENCODE_PROXY_KEY")
	if proxyURL == "" || key == "" {
		return nil, errors.New("OpenCode proxy is not configured; refusing a direct Zen call")
	}
	u, err := validateProviderURL(proxyURL, true)
	if err != nil || u.Path != "" {
		return nil, errors.New("OpenCode proxy URL must be a trusted service origin without a path")
	}
	return &openCodeProxyTransport{base: safeTransport(true), url: u, key: key, authHeader: provider.AuthHeader}, nil
}
