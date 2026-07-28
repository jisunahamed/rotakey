package app

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func integrationRedis(t *testing.T) *redis.Client {
	t.Helper()
	rawURL := os.Getenv("TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		t.Fatalf("connect to Redis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRedisAtomicConcurrentReservations(t *testing.T) {
	client := integrationRedis(t)
	id := fmt.Sprintf("test_atomic_%d", time.Now().UnixNano())
	key := "limit:" + id + ":all:rpm"
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })
	engine := newLimiter(client)

	var allowed atomic.Int64
	var group sync.WaitGroup
	for range 250 {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := engine.Reserve(context.Background(), []limitConstraint{{
				Key: key, Capacity: 100, WindowMS: time.Minute.Milliseconds(), Cost: 1,
			}})
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			if result.Allowed {
				allowed.Add(1)
			}
		}()
	}
	group.Wait()
	if got := allowed.Load(); got != 100 {
		t.Fatalf("atomic limiter allowed %d requests, want exactly 100", got)
	}
}

func TestTwoCredentialsFortyRPMBalanceAndExhaust(t *testing.T) {
	if time.Now().Second() >= 57 {
		t.Skip("too close to a minute reset boundary")
	}
	client := integrationRedis(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	modelID := "mdl_balance_" + suffix
	firstID := "key_first_" + suffix
	secondID := "key_second_" + suffix
	keys := []string{
		"rr:" + modelID,
		"limit:" + firstID + ":all:rpm",
		"limit:" + secondID + ":all:rpm",
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), keys...).Err() })

	forty := int64(40)
	credentials := []credentialRuntime{
		{CredentialView: CredentialView{
			ID: firstID, Label: "first", Enabled: true, Status: "healthy",
			Limits: RatePolicy{RPM: &forty}, ModelLimits: map[string]RatePolicy{},
		}},
		{CredentialView: CredentialView{
			ID: secondID, Label: "second", Enabled: true, Status: "healthy",
			Limits: RatePolicy{RPM: &forty}, ModelLimits: map[string]RatePolicy{},
		}},
	}
	server := &Server{redis: client, limiter: newLimiter(client)}
	counts := map[string]int{}
	for request := 0; request < 80; request++ {
		selected, _, _, err := server.selectCredential(
			context.Background(), modelID, credentials, 1, map[string]bool{}, 0,
		)
		if err != nil {
			t.Fatalf("select request %d: %v", request+1, err)
		}
		if selected == nil {
			t.Fatalf("request %d unexpectedly had no capacity", request+1)
		}
		counts[selected.Label]++
	}
	if counts["first"] != 40 || counts["second"] != 40 {
		t.Fatalf("unexpected balance: %#v", counts)
	}
	selected, _, retry, err := server.selectCredential(
		context.Background(), modelID, credentials, 1, map[string]bool{}, 0,
	)
	if err != nil {
		t.Fatalf("select exhausted request: %v", err)
	}
	if selected != nil || retry <= 0 {
		t.Fatalf("81st request should be rate limited, selected=%v retry=%s", selected, retry)
	}
}
