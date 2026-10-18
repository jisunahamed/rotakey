package app

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAdminCreatedGatewayKeyLimits(t *testing.T) {
	if adminCreatedKeyRPM != 2000 {
		t.Fatalf("RPM = %d, want 2000", adminCreatedKeyRPM)
	}
	if adminCreatedKeyConcurrency != 100 {
		t.Fatalf("concurrency = %d, want 100", adminCreatedKeyConcurrency)
	}
}

func TestNormalizeGatewayKeyLabel(t *testing.T) {
	if got := normalizeGatewayKeyLabel("  mobile app  "); got != "mobile app" {
		t.Fatalf("label = %q", got)
	}
	if got := normalizeGatewayKeyLabel("   "); got != "API key" {
		t.Fatalf("empty label = %q", got)
	}
}

func TestGatewayAccessEnforcesConcurrencyAndRPM(t *testing.T) {
	client := integrationRedis(t)
	id := fmt.Sprintf("gateway_access_%d", time.Now().UnixNano())
	base := "gateway-access:" + id
	ctx := context.Background()
	client.Set(ctx, base+":last-used-write", "1", time.Minute)
	t.Cleanup(func() {
		client.Del(ctx, base+":active", base+":last-used-write", base+":rpm:"+fmt.Sprint(time.Now().UTC().Unix()/60))
	})
	server := &Server{redis: client}
	access := gatewayAccessRuntime{GatewayAccessKey: GatewayAccessKey{ID: id, RPM: 2, ConcurrencyLimit: 1}}

	releaseFirst, blocked, err := server.reserveGatewayAccess(ctx, access)
	if err != nil || blocked != "" {
		t.Fatalf("first reservation: blocked=%q err=%v", blocked, err)
	}
	if _, blocked, err = server.reserveGatewayAccess(ctx, access); err != nil || blocked != "concurrency" {
		t.Fatalf("parallel reservation: blocked=%q err=%v", blocked, err)
	}
	releaseFirst()

	releaseSecond, blocked, err := server.reserveGatewayAccess(ctx, access)
	if err != nil || blocked != "" {
		t.Fatalf("second RPM reservation: blocked=%q err=%v", blocked, err)
	}
	releaseSecond()
	if _, blocked, err = server.reserveGatewayAccess(ctx, access); err != nil || blocked != "rpm" {
		t.Fatalf("third RPM reservation: blocked=%q err=%v", blocked, err)
	}
}
