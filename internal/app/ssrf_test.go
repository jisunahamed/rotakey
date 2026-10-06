package app

import (
	"testing"
	"time"
)

func TestSafeTransportLetsRequestContextOwnResponseHeaderDeadline(t *testing.T) {
	transport := safeTransport(false)

	if transport.ResponseHeaderTimeout != 0 {
		t.Fatalf("response header timeout = %s, want disabled", transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("TLS handshake timeout = %s, want 10s", transport.TLSHandshakeTimeout)
	}
}
