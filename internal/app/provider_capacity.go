package app

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

type aggregateDimension struct {
	name   string
	window time.Duration
	value  func(RatePolicy) *int64
	max    bool
}

var aggregateDimensions = []aggregateDimension{
	{name: "rps", window: time.Second, value: func(policy RatePolicy) *int64 { return policy.RPS }},
	{name: "rpm", window: time.Minute, value: func(policy RatePolicy) *int64 { return policy.RPM }},
	{name: "rpd", window: 24 * time.Hour, value: func(policy RatePolicy) *int64 { return policy.RPD }},
	{name: "tps", window: time.Second, value: func(policy RatePolicy) *int64 { return policy.TPS }},
	{name: "tpm", window: time.Minute, value: func(policy RatePolicy) *int64 { return policy.TPM }},
	{name: "tpd", window: 24 * time.Hour, value: func(policy RatePolicy) *int64 { return policy.TPD }},
	{name: "tpr", value: func(policy RatePolicy) *int64 { return policy.TPR }, max: true},
}

func (s *Server) providerCapacity(ctx context.Context, credentials []CredentialView) *ProviderCapacity {
	result := &ProviderCapacity{
		TotalKeys: len(credentials),
		Limits:    make(map[string]AggregateLimit, len(aggregateDimensions)),
	}
	ready := make([]CredentialView, 0, len(credentials))
	for _, credential := range credentials {
		// A key with no credit left is not ready: the router skips it, so counting
		// its rate limits as available capacity would overstate the provider.
		if credential.Enabled && credential.Status == "healthy" && !credential.BalanceExhausted() {
			ready = append(ready, credential)
		}
	}
	result.ReadyKeys = len(ready)
	for _, dimension := range aggregateDimensions {
		aggregate := AggregateLimit{}
		for _, credential := range ready {
			value := dimension.value(credential.Limits)
			if value == nil {
				aggregate.Unlimited = true
				aggregate.Limit = 0
				aggregate.Remaining = 0
				break
			}
			if dimension.max {
				if *value > aggregate.Limit {
					aggregate.Limit = *value
					aggregate.Remaining = *value
				}
				continue
			}
			aggregate.Limit += *value
			remaining, err := s.sharedLimitRemaining(ctx, credential.ID, dimension.name, *value, dimension.window)
			if err != nil {
				aggregate.Unknown = true
				continue
			}
			aggregate.Remaining += remaining
		}
		result.Limits[dimension.name] = aggregate
	}
	return result
}

func (s *Server) sharedLimitRemaining(
	ctx context.Context,
	credentialID string,
	dimension string,
	capacity int64,
	window time.Duration,
) (int64, error) {
	key := "limit:" + credentialID + ":all:" + dimension
	values, err := s.redis.HMGet(ctx, key, "count", "bucket").Result()
	if err != nil {
		return 0, err
	}
	var count, bucket int64
	if len(values) == 2 {
		count, _ = strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
		bucket, _ = strconv.ParseInt(fmt.Sprint(values[1]), 10, 64)
	}
	now := time.Now().UnixMilli()
	if bucket != now/window.Milliseconds() {
		count = 0
	}
	remaining := capacity - count
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}
