package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type limiter struct {
	redis *redis.Client
}

type limitConstraint struct {
	Key      string
	Capacity int64
	WindowMS int64
	Cost     int64
	Token    bool
}

type reservation struct {
	constraints []limitConstraint
	tokenCost   int64
	reservedAt  int64
}

type reserveResult struct {
	Allowed    bool
	Retry      time.Duration
	ReservedAt int64
}

var reserveScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local states = {}
local max_wait = 0

for i, key in ipairs(KEYS) do
  local base = 2 + ((i - 1) * 3)
  local capacity = tonumber(ARGV[base])
  local window = tonumber(ARGV[base + 1])
  local cost = tonumber(ARGV[base + 2])
  local bucket = math.floor(now / window)
  local current = redis.call("HMGET", key, "count", "bucket")
  local count = tonumber(current[1])
  local stored_bucket = tonumber(current[2])
  if count == nil or stored_bucket ~= bucket then
    count = 0
  end
  local wait = 0
  if count + cost > capacity then
    wait = ((bucket + 1) * window) - now
    if wait > max_wait then max_wait = wait end
  end
  states[i] = {key, count, bucket, window, cost}
end

if max_wait > 0 then
  return {0, max_wait}
end

for i, state in ipairs(states) do
  redis.call("HSET", state[1], "count", state[2] + state[5], "bucket", state[3])
  redis.call("PEXPIRE", state[1], math.ceil(state[4] * 2))
end
return {1, 0}
`)

var adjustScript = redis.NewScript(`
local reserved_at = tonumber(ARGV[1])
local adjustment = tonumber(ARGV[2])
for i, key in ipairs(KEYS) do
  local base = 3 + ((i - 1) * 2)
  local _capacity = tonumber(ARGV[base])
  local window = tonumber(ARGV[base + 1])
  local reservation_bucket = math.floor(reserved_at / window)
  local current = redis.call("HMGET", key, "count", "bucket")
  local count = tonumber(current[1])
  local stored_bucket = tonumber(current[2])
  if count ~= nil and stored_bucket == reservation_bucket then
    count = math.max(0, count - adjustment)
    redis.call("HSET", key, "count", count)
    redis.call("PEXPIRE", key, math.ceil(window * 2))
  end
end
return 1
`)

func newLimiter(client *redis.Client) *limiter {
	return &limiter{redis: client}
}

func (l *limiter) Reserve(ctx context.Context, constraints []limitConstraint) (reserveResult, error) {
	now := time.Now().UnixMilli()
	if len(constraints) == 0 {
		if err := l.redis.Ping(ctx).Err(); err != nil {
			return reserveResult{}, err
		}
		return reserveResult{Allowed: true, ReservedAt: now}, nil
	}
	keys := make([]string, 0, len(constraints))
	args := []any{now}
	for _, constraint := range constraints {
		if constraint.Cost > constraint.Capacity {
			windowEnd := ((now / constraint.WindowMS) + 1) * constraint.WindowMS
			return reserveResult{
				Allowed: false, Retry: time.Duration(windowEnd-now) * time.Millisecond, ReservedAt: now,
			}, nil
		}
		keys = append(keys, constraint.Key)
		args = append(args, constraint.Capacity, constraint.WindowMS, constraint.Cost)
	}
	raw, err := reserveScript.Run(ctx, l.redis, keys, args...).Slice()
	if err != nil {
		return reserveResult{}, err
	}
	allowed, _ := strconv.ParseInt(fmt.Sprint(raw[0]), 10, 64)
	retryMS, _ := strconv.ParseInt(fmt.Sprint(raw[1]), 10, 64)
	return reserveResult{
		Allowed: allowed == 1, Retry: time.Duration(retryMS) * time.Millisecond, ReservedAt: now,
	}, nil
}

func (l *limiter) AdjustTokens(ctx context.Context, reservation reservation, actualTokens int64) error {
	adjustment := reservation.tokenCost - actualTokens
	if adjustment == 0 {
		return nil
	}
	tokenConstraints := make([]limitConstraint, 0)
	for _, constraint := range reservation.constraints {
		if constraint.Token {
			tokenConstraints = append(tokenConstraints, constraint)
		}
	}
	if len(tokenConstraints) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tokenConstraints))
	args := []any{reservation.reservedAt, adjustment}
	for _, constraint := range tokenConstraints {
		keys = append(keys, constraint.Key)
		args = append(args, constraint.Capacity, constraint.WindowMS)
	}
	return adjustScript.Run(ctx, l.redis, keys, args...).Err()
}

func buildConstraints(credential credentialRuntime, modelID string, tokenCost int64) ([]limitConstraint, bool) {
	policies := []struct {
		scope  string
		policy RatePolicy
	}{{scope: "all", policy: credential.Limits}}
	if modelPolicy, ok := credential.ModelLimits[modelID]; ok {
		policies = append(policies, struct {
			scope  string
			policy RatePolicy
		}{scope: "model:" + modelID, policy: modelPolicy})
	}

	constraints := make([]limitConstraint, 0, 12)
	for _, scoped := range policies {
		if scoped.policy.TPR != nil && tokenCost > *scoped.policy.TPR {
			return nil, false
		}
		prefix := "limit:" + credential.ID + ":" + scoped.scope + ":"
		add := func(name string, value *int64, window time.Duration, cost int64, token bool) {
			if value != nil {
				constraints = append(constraints, limitConstraint{
					Key: prefix + name, Capacity: *value, WindowMS: window.Milliseconds(), Cost: cost, Token: token,
				})
			}
		}
		add("rps", scoped.policy.RPS, time.Second, 1, false)
		add("rpm", scoped.policy.RPM, time.Minute, 1, false)
		add("rpd", scoped.policy.RPD, 24*time.Hour, 1, false)
		add("tps", scoped.policy.TPS, time.Second, tokenCost, true)
		add("tpm", scoped.policy.TPM, time.Minute, tokenCost, true)
		add("tpd", scoped.policy.TPD, 24*time.Hour, tokenCost, true)
	}
	return constraints, true
}
