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

func TestRedisReservationReportsEveryBlockingBucket(t *testing.T) {
	client := integrationRedis(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	rpmKey := "limit:key_diag_" + suffix + ":all:rpm"
	tpmKey := "limit:key_diag_" + suffix + ":model:model_diag:tpm"
	t.Cleanup(func() { _ = client.Del(context.Background(), rpmKey, tpmKey).Err() })
	engine := newLimiter(client)
	constraints := []limitConstraint{
		{Key: rpmKey, Scope: "shared", Dimension: "rpm", Capacity: 1, WindowMS: time.Minute.Milliseconds(), Cost: 1},
		{Key: tpmKey, Scope: "model", Dimension: "tpm", Capacity: 100, WindowMS: time.Minute.Milliseconds(), Cost: 60, Token: true},
	}
	first, err := engine.Reserve(context.Background(), constraints)
	if err != nil || !first.Allowed {
		t.Fatalf("first reservation should pass: result=%#v err=%v", first, err)
	}
	second, err := engine.Reserve(context.Background(), constraints)
	if err != nil || second.Allowed {
		t.Fatalf("second reservation should be blocked: result=%#v err=%v", second, err)
	}
	if len(second.Blocked) != 2 {
		t.Fatalf("got %d blocking buckets, want 2", len(second.Blocked))
	}
	if second.Blocked[0].Constraint.Dimension != "rpm" || second.Blocked[0].Used != 1 {
		t.Fatalf("unexpected RPM blocker: %#v", second.Blocked[0])
	}
	if second.Blocked[1].Constraint.Scope != "model" || second.Blocked[1].Used != 60 {
		t.Fatalf("unexpected TPM blocker: %#v", second.Blocked[1])
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

func TestPrimaryCredentialFillsBeforeFallback(t *testing.T) {
	if time.Now().Second() >= 57 {
		t.Skip("too close to a minute reset boundary")
	}
	client := integrationRedis(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	modelID := "mdl_primary_" + suffix
	primaryID := "key_primary_" + suffix
	fallbackID := "key_fallback_" + suffix
	keys := []string{
		"rr:" + modelID,
		"limit:" + primaryID + ":all:rpm",
		"limit:" + fallbackID + ":all:rpm",
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), keys...).Err() })

	two := int64(2)
	credentials := []credentialRuntime{
		{CredentialView: CredentialView{
			ID: primaryID, Label: "primary", IsPrimary: true, Enabled: true, Status: "healthy",
			Limits: RatePolicy{RPM: &two}, ModelLimits: map[string]RatePolicy{},
		}},
		{CredentialView: CredentialView{
			ID: fallbackID, Label: "fallback", Enabled: true, Status: "healthy",
			Limits: RatePolicy{RPM: &two}, ModelLimits: map[string]RatePolicy{},
		}},
	}
	server := &Server{redis: client, limiter: newLimiter(client)}
	got := make([]string, 0, 4)
	for request := 0; request < 4; request++ {
		selected, _, _, err := server.selectCredential(
			context.Background(), modelID, credentials, 1, map[string]bool{}, 0,
		)
		if err != nil || selected == nil {
			t.Fatalf("select request %d: selected=%v err=%v", request+1, selected, err)
		}
		got = append(got, selected.Label)
	}
	want := []string{"primary", "primary", "fallback", "fallback"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("selection order = %v, want %v", got, want)
		}
	}
}

func TestSharedCredentialLimitSpansModels(t *testing.T) {
	if time.Now().Second() >= 57 {
		t.Skip("too close to a minute reset boundary")
	}
	client := integrationRedis(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	credentialID := "key_shared_" + suffix
	modelA := "mdl_a_" + suffix
	modelB := "mdl_b_" + suffix
	keys := []string{
		"rr:" + modelA,
		"rr:" + modelB,
		"limit:" + credentialID + ":all:rpm",
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), keys...).Err() })

	two := int64(2)
	credentials := []credentialRuntime{{CredentialView: CredentialView{
		ID: credentialID, Label: "shared", Enabled: true, Status: "healthy",
		Limits: RatePolicy{RPM: &two}, ModelLimits: map[string]RatePolicy{},
	}}}
	server := &Server{redis: client, limiter: newLimiter(client)}
	for _, modelID := range []string{modelA, modelB} {
		selected, _, _, err := server.selectCredential(
			context.Background(), modelID, credentials, 1, map[string]bool{}, 0,
		)
		if err != nil || selected == nil {
			t.Fatalf("model %s should share available capacity: selected=%v err=%v", modelID, selected, err)
		}
	}
	selected, _, retry, err := server.selectCredential(
		context.Background(), modelA, credentials, 1, map[string]bool{}, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if selected != nil || retry <= 0 {
		t.Fatalf("third cross-model request should exhaust the shared limit: selected=%v retry=%s", selected, retry)
	}
}

func TestProviderCapacityAddsAndRemovesAPIKeys(t *testing.T) {
	client := integrationRedis(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	firstID := "key_capacity_first_" + suffix
	secondID := "key_capacity_second_" + suffix
	keys := []string{
		"limit:" + firstID + ":all:rpm",
		"limit:" + secondID + ":all:rpm",
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), keys...).Err() })
	bucket := time.Now().UnixMilli() / time.Minute.Milliseconds()
	if err := client.HSet(context.Background(), keys[0], "count", 4, "bucket", bucket).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(context.Background(), keys[1], "count", 6, "bucket", bucket).Err(); err != nil {
		t.Fatal(err)
	}
	forty := int64(40)
	credentials := []CredentialView{
		{ID: firstID, Enabled: true, Status: "healthy", Limits: RatePolicy{RPM: &forty}},
		{ID: secondID, Enabled: true, Status: "healthy", Limits: RatePolicy{RPM: &forty}},
	}
	server := &Server{redis: client}
	capacity := server.providerCapacity(context.Background(), credentials)
	rpm := capacity.Limits["rpm"]
	if rpm.Limit != 80 || rpm.Remaining != 70 || capacity.ReadyKeys != 2 {
		t.Fatalf("two-key capacity = %#v", capacity)
	}
	capacity = server.providerCapacity(context.Background(), credentials[:1])
	rpm = capacity.Limits["rpm"]
	if rpm.Limit != 40 || rpm.Remaining != 36 || capacity.TotalKeys != 1 {
		t.Fatalf("one-key capacity = %#v", capacity)
	}
}
