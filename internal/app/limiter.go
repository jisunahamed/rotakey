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
	Key       string
	Scope     string
	Dimension string
	Capacity  int64
	WindowMS  int64
	Cost      int64
	Token     bool
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
	Blocked    []blockedLimit
}

type blockedLimit struct {
	Constraint limitConstraint
	Used       int64
	Retry      time.Duration
}

var reserveScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local states = {}
local max_wait = 0
local blockers = {}

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
    table.insert(blockers, i)
    table.insert(blockers, count)
    table.insert(blockers, wait)
  end
  states[i] = {key, count, bucket, window, cost}
end

if max_wait > 0 then
  local result = {0, max_wait}
  for _, value in ipairs(blockers) do table.insert(result, value) end
  return result
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
	directBlockers := make([]blockedLimit, 0)
	var directRetry time.Duration
	for _, constraint := range constraints {
		if constraint.Cost > constraint.Capacity {
			windowEnd := ((now / constraint.WindowMS) + 1) * constraint.WindowMS
			retry := time.Duration(windowEnd-now) * time.Millisecond
			if retry > directRetry {
				directRetry = retry
			}
			directBlockers = append(directBlockers, blockedLimit{Constraint: constraint, Retry: retry})
		}
		keys = append(keys, constraint.Key)
		args = append(args, constraint.Capacity, constraint.WindowMS, constraint.Cost)
	}
	if len(directBlockers) > 0 {
		return reserveResult{
			Allowed: false, Retry: directRetry, ReservedAt: now, Blocked: directBlockers,
		}, nil
	}
	raw, err := reserveScript.Run(ctx, l.redis, keys, args...).Slice()
	if err != nil {
		return reserveResult{}, err
	}
	allowed, _ := strconv.ParseInt(fmt.Sprint(raw[0]), 10, 64)
	retryMS, _ := strconv.ParseInt(fmt.Sprint(raw[1]), 10, 64)
	blocked := make([]blockedLimit, 0, (len(raw)-2)/3)
	for index := 2; index+2 < len(raw); index += 3 {
		constraintIndex, _ := strconv.ParseInt(fmt.Sprint(raw[index]), 10, 64)
		used, _ := strconv.ParseInt(fmt.Sprint(raw[index+1]), 10, 64)
		blockedRetryMS, _ := strconv.ParseInt(fmt.Sprint(raw[index+2]), 10, 64)
		if constraintIndex < 1 || int(constraintIndex) > len(constraints) {
			continue
		}
		blocked = append(blocked, blockedLimit{
			Constraint: constraints[constraintIndex-1], Used: used,
			Retry: time.Duration(blockedRetryMS) * time.Millisecond,
		})
	}
	return reserveResult{
		Allowed: allowed == 1, Retry: time.Duration(retryMS) * time.Millisecond,
		ReservedAt: now, Blocked: blocked,
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
	constraints, rejected := buildConstraintsWithDiagnostics(credential, modelID, tokenCost)
	return constraints, len(rejected) == 0
}

func buildConstraintsWithDiagnostics(credential credentialRuntime, modelID string, tokenCost int64) ([]limitConstraint, []RoutingDecision) {
	return buildConstraintsWithCosts(credential, modelID, 1, tokenCost, tokenCost)
}

func buildConstraintsWithCosts(credential credentialRuntime, modelID string, requestCost, tokenCost, tprCost int64) ([]limitConstraint, []RoutingDecision) {
	policies := []struct {
		keyScope string
		scope    string
		policy   RatePolicy
	}{{keyScope: "all", scope: "shared", policy: credential.Limits}}
	if modelPolicy, ok := credential.ModelLimits[modelID]; ok {
		policies = append(policies, struct {
			keyScope string
			scope    string
			policy   RatePolicy
		}{keyScope: "model:" + modelID, scope: "model", policy: modelPolicy})
	}

	constraints := make([]limitConstraint, 0, 12)
	rejected := make([]RoutingDecision, 0, 2)
	for _, scoped := range policies {
		if scoped.policy.TPR != nil && tprCost > *scoped.policy.TPR {
			rejected = append(rejected, RoutingDecision{
				CredentialID: credential.ID, CredentialLabel: credential.Label,
				Reason: "tpr_exceeded", Scope: scoped.scope, Dimension: "tpr",
				Limit: *scoped.policy.TPR, Remaining: *scoped.policy.TPR, Required: tprCost,
			})
		}
		prefix := "limit:" + credential.ID + ":" + scoped.keyScope + ":"
		add := func(name string, value *int64, window time.Duration, cost int64, token bool) {
			if value != nil {
				constraints = append(constraints, limitConstraint{
					Key: prefix + name, Scope: scoped.scope, Dimension: name,
					Capacity: *value, WindowMS: window.Milliseconds(), Cost: cost, Token: token,
				})
			}
		}
		add("rps", scoped.policy.RPS, time.Second, requestCost, false)
		add("rpm", scoped.policy.RPM, time.Minute, requestCost, false)
		add("rpd", scoped.policy.RPD, 24*time.Hour, requestCost, false)
		add("tps", scoped.policy.TPS, time.Second, tokenCost, true)
		add("tpm", scoped.policy.TPM, time.Minute, tokenCost, true)
		add("tpd", scoped.policy.TPD, 24*time.Hour, tokenCost, true)
	}
	return constraints, rejected
}
