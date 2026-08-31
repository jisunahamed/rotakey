package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func validateProviderURL(raw string, allowPrivate bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("base URL is invalid")
	}
	if parsed.Scheme != "https" && !(allowPrivate && parsed.Scheme == "http") {
		return nil, errors.New("base URL must use HTTPS; HTTP is allowed only with private-network access")
	}
	// A query string is the one rejection an operator hits by following provider
	// documentation rather than by mistake: Azure's portal shows a "Target URI"
	// ending in ?api-version=..., and the generic message left no clue that the
	// query is the part to delete.
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, errors.New("base URL must not contain a query string; remove the part from \"?\" onwards, such as ?api-version=")
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("base URL must contain only scheme, host, optional port, and path")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")

	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !allowPrivate && blockedIP(ip) {
		return nil, errors.New("base URL resolves to a blocked network")
	}
	return parsed, nil
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func safeTransport(allowPrivate bool) *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	// Never route provider traffic through ambient proxies: a proxy could resolve
	// and reach a private target after our local address policy has accepted the host.
	base.Proxy = nil
	base.MaxIdleConns = 100
	base.MaxIdleConnsPerHost = 20
	base.IdleConnTimeout = 90 * time.Second
	base.TLSHandshakeTimeout = 10 * time.Second
	base.ResponseHeaderTimeout = 60 * time.Second
	base.ExpectContinueTimeout = time.Second

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range addresses {
			if !allowPrivate && blockedIP(candidate.IP) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		}
		return nil, fmt.Errorf("all resolved addresses for %s are blocked", host)
	}
	return base
}

func upstreamClient(provider Provider) (*http.Client, error) {
	if _, err := validateProviderURL(provider.BaseURL, provider.AllowPrivateNetwork); err != nil {
		return nil, err
	}
	timeout := time.Duration(provider.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &http.Client{
		Transport: safeTransport(provider.AllowPrivateNetwork),
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// providerRetryTimeout returns the deadline for a provider's entire retry
// window based on its configured timeout. Raising a provider's timeout extends
// how long a request may run; when unset it falls back to 120 seconds. Streaming
// completions can legitimately run longer than the configured request timeout,
// so they use an extended floor and the request context stays the deadline
// authority (callers should disable http.Client's competing total timeout).
func providerRetryTimeout(provider Provider, isStream bool) time.Duration {
	timeout := time.Duration(provider.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if isStream && timeout < 15*time.Minute {
		timeout = 15 * time.Minute
	}
	return timeout
}
