package app

import (
	"testing"
	"time"
)

func TestParseOverviewRange(t *testing.T) {
	tests := []struct {
		input  string
		name   string
		points int
		bucket time.Duration
	}{
		{input: "", name: "all", points: 1218, bucket: 30 * 24 * time.Hour},
		{input: "1h", name: "1h", points: 12, bucket: 5 * time.Minute},
		{input: "24h", name: "24h", points: 24, bucket: time.Hour},
		{input: "7d", name: "7d", points: 28, bucket: 6 * time.Hour},
		{input: "all", name: "all", points: 1218, bucket: 30 * 24 * time.Hour},
	}
	for _, test := range tests {
		got, err := parseOverviewRange(test.input)
		if err != nil {
			t.Fatalf("parseOverviewRange(%q): %v", test.input, err)
		}
		if got.Name != test.name || got.PointCount != test.points || got.Bucket != test.bucket {
			t.Fatalf("parseOverviewRange(%q) = %#v", test.input, got)
		}
	}
	if _, err := parseOverviewRange("30d"); err == nil {
		t.Fatal("unsupported range should fail")
	}
}

func TestTighterOverviewHeadroom(t *testing.T) {
	current := &overviewHeadroom{Dimension: "RPM", Remaining: 20, Limit: 40}
	candidate := &overviewHeadroom{Dimension: "RPD", Remaining: 10, Limit: 100}
	if got := tighterOverviewHeadroom(current, candidate); got != candidate {
		t.Fatalf("expected 10%% headroom to be tighter, got %#v", got)
	}
	if got := tighterOverviewHeadroom(candidate, current); got != candidate {
		t.Fatalf("expected existing 10%% headroom to remain tighter, got %#v", got)
	}
}

func TestAggregateProviderCapacityUsesReadyKeysAndLiveCounts(t *testing.T) {
	forty := int64(40)
	now := time.Date(2026, 7, 29, 15, 30, 10, 0, time.UTC)
	credentials := []CredentialView{
		{ID: "key-1", Enabled: true, Status: "healthy", Limits: RatePolicy{RPM: &forty}},
		{ID: "key-2", Enabled: true, Status: "healthy", Limits: RatePolicy{RPM: &forty}},
		{ID: "key-3", Enabled: true, Status: "quarantined", Limits: RatePolicy{RPM: &forty}},
	}
	bucket := now.UnixMilli() / time.Minute.Milliseconds()
	snapshot := overviewRedisSnapshot{
		Buckets: map[string]redisBucket{
			"limit:key-1:all:rpm": {Count: 4, Bucket: bucket},
			"limit:key-2:all:rpm": {Count: 6, Bucket: bucket},
		},
		Cooldowns:       map[string]time.Duration{},
		CooldownUnknown: map[string]bool{},
	}
	got := aggregateProviderCapacity(credentials, snapshot, now)["rpm"]
	if got.Limit != 80 || got.Remaining != 70 || got.Unlimited || got.Unknown {
		t.Fatalf("RPM aggregate = %#v, want 70/80", got)
	}
	if got.ResetAt == nil || !got.ResetAt.Equal(time.Date(2026, 7, 29, 15, 31, 0, 0, time.UTC)) {
		t.Fatalf("RPM reset = %v", got.ResetAt)
	}
}

func TestAggregateProviderCapacityUnlimitedKeyWins(t *testing.T) {
	forty := int64(40)
	now := time.Now().UTC()
	credentials := []CredentialView{
		{ID: "limited", Enabled: true, Status: "healthy", Limits: RatePolicy{RPM: &forty}},
		{ID: "unlimited", Enabled: true, Status: "healthy", Limits: RatePolicy{}},
	}
	snapshot := overviewRedisSnapshot{
		Buckets: map[string]redisBucket{
			"limit:limited:all:rpm": {},
		},
		Cooldowns:       map[string]time.Duration{},
		CooldownUnknown: map[string]bool{},
	}
	if got := aggregateProviderCapacity(credentials, snapshot, now)["rpm"]; !got.Unlimited {
		t.Fatalf("RPM aggregate should be unlimited, got %#v", got)
	}
}

func TestCredentialCapacityUsesModelSpecificBottleneck(t *testing.T) {
	sharedRPM, modelRPM := int64(40), int64(10)
	now := time.Date(2026, 7, 29, 15, 30, 10, 0, time.UTC)
	bucket := now.UnixMilli() / time.Minute.Milliseconds()
	credential := CredentialView{
		ID: "key-1", Label: "key one", Enabled: true, Status: "healthy",
		Limits:      RatePolicy{RPM: &sharedRPM},
		ModelLimits: map[string]RatePolicy{"model-1": {RPM: &modelRPM}},
	}
	got := cachedCredentialCapacity(credential, "model-1", overviewRedisSnapshot{
		Buckets: map[string]redisBucket{
			"limit:key-1:all:rpm":           {Count: 5, Bucket: bucket},
			"limit:key-1:model:model-1:rpm": {Count: 8, Bucket: bucket},
		},
		Cooldowns:       map[string]time.Duration{},
		CooldownUnknown: map[string]bool{},
	}, now)
	if got.Request == nil || got.Request.Scope != "model" || got.Request.Remaining != 2 || got.Request.Limit != 10 {
		t.Fatalf("model-specific bottleneck = %#v, want 2/10 model RPM", got.Request)
	}
}

func TestCredentialCapacityMarksRedisUncertainty(t *testing.T) {
	rpm := int64(40)
	got := cachedCredentialCapacity(CredentialView{
		ID: "key-1", Label: "key one", Enabled: true, Status: "healthy",
		Limits: RatePolicy{RPM: &rpm},
	}, "model-1", overviewRedisSnapshot{
		Buckets: map[string]redisBucket{
			"limit:key-1:all:rpm": {Unknown: true},
		},
		Cooldowns:       map[string]time.Duration{},
		CooldownUnknown: map[string]bool{},
	}, time.Now().UTC())
	if got.Status != "unknown" || !got.Unknown {
		t.Fatalf("Redis uncertainty should mark credential unknown, got %#v", got)
	}
}

func TestLowCapacityAlertThreshold(t *testing.T) {
	alerts := []overviewAlert{}
	route := overviewRoute{
		ID: "route-1", Alias: "provider/model", Enabled: true,
		Segments: []overviewCredential{{
			ID: "key-1", Label: "key one", Status: "healthy",
			Request: &overviewHeadroom{Dimension: "RPM", Remaining: 8, Limit: 40},
		}},
	}
	appendLowCapacityAlerts(&alerts, route)
	if len(alerts) != 1 || alerts[0].Severity != "warning" {
		t.Fatalf("20%% remaining should create a warning: %#v", alerts)
	}
	alerts = nil
	route.Segments[0].Request.Remaining = 9
	appendLowCapacityAlerts(&alerts, route)
	if len(alerts) != 0 {
		t.Fatalf("more than 20%% remaining should not alert: %#v", alerts)
	}
}
