package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var errInvalidOverviewRange = errors.New("invalid overview range")

type overviewRange struct {
	Name       string
	Span       time.Duration
	Bucket     time.Duration
	SQLSpan    string
	SQLBucket  string
	PointCount int
}

func parseOverviewRange(value string) (overviewRange, error) {
	switch value {
	case "", "24h":
		return overviewRange{Name: "24h", Span: 24 * time.Hour, Bucket: time.Hour, SQLSpan: "24 hours", SQLBucket: "1 hour", PointCount: 24}, nil
	case "1h":
		return overviewRange{Name: "1h", Span: time.Hour, Bucket: 5 * time.Minute, SQLSpan: "1 hour", SQLBucket: "5 minutes", PointCount: 12}, nil
	case "7d":
		return overviewRange{Name: "7d", Span: 7 * 24 * time.Hour, Bucket: 6 * time.Hour, SQLSpan: "7 days", SQLBucket: "6 hours", PointCount: 28}, nil
	default:
		return overviewRange{}, errInvalidOverviewRange
	}
}

type overviewSummary struct {
	ProvidersTotal  int     `json:"providers_total"`
	ProvidersReady  int     `json:"providers_ready"`
	RoutesTotal     int     `json:"routes_total"`
	RoutesReady     int     `json:"routes_ready"`
	KeysTotal       int     `json:"keys_total"`
	KeysReady       int     `json:"keys_ready"`
	KeysWarning     int     `json:"keys_warning"`
	Requests        int64   `json:"requests"`
	Tokens          int64   `json:"tokens"`
	Errors          int64   `json:"errors"`
	ErrorRate       float64 `json:"error_rate"`
	LatencyP50MS    int64   `json:"latency_p50_ms"`
	LatencyP95MS    int64   `json:"latency_p95_ms"`
	MaxWaitMS       int     `json:"max_wait_ms"`
	GatewayKeyReady bool    `json:"gateway_key_ready"`
}

type overviewPoint struct {
	Timestamp    time.Time `json:"timestamp"`
	Requests     int64     `json:"requests"`
	Errors       int64     `json:"errors"`
	Tokens       int64     `json:"tokens"`
	LatencyP95MS int64     `json:"latency_p95_ms"`
}

type overviewProvider struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Enabled       bool                     `json:"enabled"`
	ModelsTotal   int                      `json:"models_total"`
	ModelsReady   int                      `json:"models_ready"`
	KeysTotal     int                      `json:"keys_total"`
	KeysReady     int                      `json:"keys_ready"`
	KeysWarning   int                      `json:"keys_warning"`
	Capacity      map[string]overviewLimit `json:"capacity"`
	ValidationBad int                      `json:"validation_warnings"`
}

type overviewLimit struct {
	Limit     int64      `json:"limit"`
	Remaining int64      `json:"remaining"`
	Unlimited bool       `json:"unlimited"`
	Unknown   bool       `json:"unknown"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

type overviewHeadroom struct {
	Dimension string     `json:"dimension"`
	Scope     string     `json:"scope"`
	Remaining int64      `json:"remaining"`
	Limit     int64      `json:"limit"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

type overviewCredential struct {
	ID              string            `json:"id"`
	Label           string            `json:"label"`
	SecretSuffix    string            `json:"secret_suffix"`
	Primary         bool              `json:"primary"`
	Status          string            `json:"status"`
	Cursor          bool              `json:"cursor"`
	Unknown         bool              `json:"unknown"`
	ValidationError string            `json:"validation_error,omitempty"`
	LastValidatedAt *time.Time        `json:"last_validated_at,omitempty"`
	CooldownUntil   *time.Time        `json:"cooldown_until,omitempty"`
	Request         *overviewHeadroom `json:"request_headroom,omitempty"`
	Token           *overviewHeadroom `json:"token_headroom,omitempty"`
}

type overviewRoute struct {
	ID                  string               `json:"id"`
	ProviderID          string               `json:"provider_id"`
	Alias               string               `json:"alias"`
	UpstreamModel       string               `json:"upstream_model"`
	Provider            string               `json:"provider"`
	Enabled             bool                 `json:"enabled"`
	Healthy             int                  `json:"healthy_credentials"`
	Unavailable         int                  `json:"unavailable_credentials"`
	Total               int                  `json:"total_credentials"`
	Requests            int64                `json:"requests"`
	Errors              int64                `json:"errors"`
	Tokens              int64                `json:"tokens"`
	ErrorRate           float64              `json:"error_rate"`
	LatencyP95MS        int64                `json:"latency_p95_ms"`
	LastRequestAt       *time.Time           `json:"last_request_at,omitempty"`
	Segments            []overviewCredential `json:"segments"`
	NextCredentialID    string               `json:"next_credential_id,omitempty"`
	NextRequestHeadroom *overviewHeadroom    `json:"next_request_headroom,omitempty"`
	NextTokenHeadroom   *overviewHeadroom    `json:"next_token_headroom,omitempty"`
	StripParameters     []string             `json:"strip_parameters"`
	SupportsResponses   bool                 `json:"supports_responses"`
	DefaultOutputTokens int                  `json:"default_max_output_tokens"`
}

type overviewAlert struct {
	ID           string `json:"id"`
	Severity     string `json:"severity"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Title        string `json:"title"`
	Detail       string `json:"detail"`
}

type overviewFailure struct {
	RequestID       string    `json:"request_id"`
	ModelAlias      string    `json:"model_alias"`
	ProviderName    string    `json:"provider_name"`
	CredentialLabel string    `json:"credential_label"`
	StatusCode      int       `json:"status_code"`
	ErrorCode       string    `json:"error_code,omitempty"`
	LatencyMS       int64     `json:"latency_ms"`
	CreatedAt       time.Time `json:"created_at"`
}

type adminOverview struct {
	GeneratedAt    time.Time          `json:"generated_at"`
	Range          string             `json:"range"`
	BaseURL        string             `json:"base_url"`
	Summary        overviewSummary    `json:"summary"`
	Series         []overviewPoint    `json:"series"`
	Providers      []overviewProvider `json:"providers"`
	Routes         []overviewRoute    `json:"routes"`
	Alerts         []overviewAlert    `json:"alerts"`
	RecentFailures []overviewFailure  `json:"recent_failures"`
}

type overviewRouteStats struct {
	Requests      int64
	Errors        int64
	Tokens        int64
	LatencyP95MS  int64
	LastRequestAt *time.Time
}

type redisBucket struct {
	Count   int64
	Bucket  int64
	Unknown bool
}

type overviewRedisSnapshot struct {
	Buckets         map[string]redisBucket
	Cooldowns       map[string]time.Duration
	CooldownUnknown map[string]bool
	Cursors         map[string]int64
	CursorUnknown   map[string]bool
}

func (s *Server) buildAdminOverview(ctx context.Context, rawRange string) (adminOverview, error) {
	selectedRange, err := parseOverviewRange(rawRange)
	if err != nil {
		return adminOverview{}, err
	}
	providers, err := s.listProviders(ctx)
	if err != nil {
		return adminOverview{}, fmt.Errorf("load providers: %w", err)
	}
	settings, keyHash, err := s.settings(ctx)
	if err != nil {
		return adminOverview{}, fmt.Errorf("load settings: %w", err)
	}
	now := time.Now().UTC()
	summary, err := s.overviewUsageSummary(ctx, selectedRange)
	if err != nil {
		return adminOverview{}, err
	}
	summary.MaxWaitMS = settings.MaxWaitMS
	summary.GatewayKeyReady = len(keyHash) > 0
	series, err := s.overviewSeries(ctx, selectedRange)
	if err != nil {
		return adminOverview{}, err
	}
	routeStats, err := s.overviewRouteStats(ctx, selectedRange)
	if err != nil {
		return adminOverview{}, err
	}
	failures, err := s.overviewFailures(ctx, selectedRange)
	if err != nil {
		return adminOverview{}, err
	}
	redisState := s.overviewRedisState(ctx, providers)

	result := adminOverview{
		GeneratedAt:    now,
		Range:          selectedRange.Name,
		BaseURL:        strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/v1",
		Summary:        summary,
		Series:         series,
		Providers:      make([]overviewProvider, 0, len(providers)),
		Routes:         []overviewRoute{},
		Alerts:         []overviewAlert{},
		RecentFailures: failures,
	}

	for _, provider := range providers {
		providerView := overviewProvider{
			ID: provider.ID, Name: provider.Name, Enabled: provider.Enabled,
			ModelsTotal: len(provider.Models), KeysTotal: len(provider.Credentials),
			Capacity: aggregateProviderCapacity(provider.Credentials, redisState, now),
		}
		summary.ProvidersTotal++
		for _, credential := range provider.Credentials {
			summary.KeysTotal++
			if credential.Enabled && credential.Status == "healthy" && !redisState.CooldownUnknown[credential.ID] &&
				redisState.Cooldowns[credential.ID] <= 0 {
				providerView.KeysReady++
				summary.KeysReady++
			}
			if credential.ValidationError != "" || credential.Status == "quarantined" {
				providerView.KeysWarning++
				providerView.ValidationBad++
				summary.KeysWarning++
				result.Alerts = append(result.Alerts, overviewAlert{
					ID: "credential:" + credential.ID, Severity: "critical",
					ResourceType: "credential", ResourceID: credential.ID,
					Title:  credential.Label + " needs attention",
					Detail: firstNonEmpty(credential.ValidationError, "The provider quarantined this API key."),
				})
			}
		}
		if provider.Enabled && providerView.KeysReady > 0 {
			summary.ProvidersReady++
		}
		if !provider.Enabled {
			result.Alerts = append(result.Alerts, overviewAlert{
				ID: "provider:" + provider.ID, Severity: "warning", ResourceType: "provider", ResourceID: provider.ID,
				Title: provider.Name + " is disabled", Detail: "Every public route under this provider is unavailable.",
			})
		}

		for _, model := range provider.Models {
			summary.RoutesTotal++
			route := overviewRoute{
				ID: model.ID, ProviderID: provider.ID, Alias: model.PublicAlias,
				UpstreamModel: model.UpstreamModel, Provider: provider.Name,
				Enabled: provider.Enabled && model.Enabled, Total: len(provider.Credentials),
				Segments: []overviewCredential{}, StripParameters: model.StripParameters,
				SupportsResponses:   model.SupportsResponses,
				DefaultOutputTokens: model.DefaultMaxOutputTokens,
			}
			stats := routeStats[model.ID]
			route.Requests, route.Errors, route.Tokens = stats.Requests, stats.Errors, stats.Tokens
			route.LatencyP95MS, route.LastRequestAt = stats.LatencyP95MS, stats.LastRequestAt
			if route.Requests > 0 {
				route.ErrorRate = float64(route.Errors) / float64(route.Requests)
			}
			primaryIndex := -1
			fallbackIndexes := []int{}
			for index, credential := range provider.Credentials {
				segment := cachedCredentialCapacity(credential, model.ID, redisState, now)
				route.Segments = append(route.Segments, segment)
				if segment.Status == "healthy" {
					route.Healthy++
					if credential.IsPrimary && primaryIndex == -1 {
						primaryIndex = index
					} else {
						fallbackIndexes = append(fallbackIndexes, index)
					}
				} else {
					route.Unavailable++
				}
			}
			cursorIndex := primaryIndex
			if cursorIndex == -1 && len(fallbackIndexes) > 0 {
				cursor := redisState.Cursors[model.ID]
				cursorIndex = fallbackIndexes[int(cursor)%len(fallbackIndexes)]
			}
			if cursorIndex >= 0 {
				route.Segments[cursorIndex].Cursor = true
				route.NextCredentialID = route.Segments[cursorIndex].ID
				route.NextRequestHeadroom = route.Segments[cursorIndex].Request
				route.NextTokenHeadroom = route.Segments[cursorIndex].Token
			}
			if route.Enabled && route.Healthy > 0 {
				summary.RoutesReady++
				providerView.ModelsReady++
			} else if route.Enabled {
				result.Alerts = append(result.Alerts, overviewAlert{
					ID: "route:" + model.ID, Severity: "critical", ResourceType: "route", ResourceID: model.ID,
					Title:  model.PublicAlias + " cannot route",
					Detail: "No healthy API key currently has capacity for this model.",
				})
			} else {
				result.Alerts = append(result.Alerts, overviewAlert{
					ID: "route:" + model.ID, Severity: "warning", ResourceType: "route", ResourceID: model.ID,
					Title: model.PublicAlias + " is disabled", Detail: "Requests using this public alias will be rejected.",
				})
			}
			appendLowCapacityAlerts(&result.Alerts, route)
			result.Routes = append(result.Routes, route)
		}
		result.Providers = append(result.Providers, providerView)
	}
	result.Summary = summary
	sortOverviewAlerts(result.Alerts)
	if len(result.Alerts) > 20 {
		result.Alerts = result.Alerts[:20]
	}
	return result, nil
}

func (s *Server) overviewUsageSummary(ctx context.Context, selected overviewRange) (overviewSummary, error) {
	var result overviewSummary
	var p50, p95 float64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400),
		       COALESCE(SUM(input_tokens + output_tokens), 0),
		       COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)
		FROM request_logs
		WHERE created_at >= NOW() - $1::interval
	`, selected.SQLSpan).Scan(&result.Requests, &result.Errors, &result.Tokens, &p50, &p95)
	if err != nil {
		return overviewSummary{}, fmt.Errorf("load overview summary: %w", err)
	}
	if result.Requests > 0 {
		result.ErrorRate = float64(result.Errors) / float64(result.Requests)
	}
	result.LatencyP50MS, result.LatencyP95MS = int64(p50), int64(p95)
	return result, nil
}

func (s *Server) overviewSeries(ctx context.Context, selected overviewRange) ([]overviewPoint, error) {
	rows, err := s.db.Query(ctx, `
		WITH bounds AS (
			SELECT date_bin($2::interval, NOW(), TIMESTAMPTZ '1970-01-01') AS end_bucket
		), buckets AS (
			SELECT generate_series(
				end_bucket - (($3 - 1) * $2::interval),
				end_bucket,
				$2::interval
			) AS bucket
			FROM bounds
		)
		SELECT b.bucket,
		       COUNT(l.id),
		       COUNT(l.id) FILTER (WHERE l.status_code >= 400),
		       COALESCE(SUM(l.input_tokens + l.output_tokens), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY l.latency_ms), 0)
		FROM buckets b
		LEFT JOIN request_logs l
		  ON l.created_at >= b.bucket
		 AND l.created_at < b.bucket + $2::interval
		 AND l.created_at >= NOW() - $1::interval
		GROUP BY b.bucket
		ORDER BY b.bucket
	`, selected.SQLSpan, selected.SQLBucket, selected.PointCount)
	if err != nil {
		return nil, fmt.Errorf("load overview series: %w", err)
	}
	defer rows.Close()
	result := make([]overviewPoint, 0, selected.PointCount)
	for rows.Next() {
		var point overviewPoint
		var latency float64
		if err := rows.Scan(&point.Timestamp, &point.Requests, &point.Errors, &point.Tokens, &latency); err != nil {
			return nil, err
		}
		point.LatencyP95MS = int64(latency)
		result = append(result, point)
	}
	return result, rows.Err()
}

func (s *Server) overviewRouteStats(ctx context.Context, selected overviewRange) (map[string]overviewRouteStats, error) {
	rows, err := s.db.Query(ctx, `
		SELECT model_id, COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400),
		       COALESCE(SUM(input_tokens + output_tokens), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
		       MAX(created_at)
		FROM request_logs
		WHERE created_at >= NOW() - $1::interval AND model_id IS NOT NULL
		GROUP BY model_id
	`, selected.SQLSpan)
	if err != nil {
		return nil, fmt.Errorf("load route overview stats: %w", err)
	}
	defer rows.Close()
	result := map[string]overviewRouteStats{}
	for rows.Next() {
		var id string
		var stats overviewRouteStats
		var latency float64
		if err := rows.Scan(&id, &stats.Requests, &stats.Errors, &stats.Tokens, &latency, &stats.LastRequestAt); err != nil {
			return nil, err
		}
		stats.LatencyP95MS = int64(latency)
		result[id] = stats
	}
	return result, rows.Err()
}

func (s *Server) overviewFailures(ctx context.Context, selected overviewRange) ([]overviewFailure, error) {
	rows, err := s.db.Query(ctx, `
		SELECT request_id, model_alias, provider_name, credential_label,
		       status_code, COALESCE(error_code, ''), latency_ms, created_at
		FROM request_logs
		WHERE created_at >= NOW() - $1::interval AND status_code >= 400
		ORDER BY created_at DESC LIMIT 8
	`, selected.SQLSpan)
	if err != nil {
		return nil, fmt.Errorf("load overview failures: %w", err)
	}
	defer rows.Close()
	result := []overviewFailure{}
	for rows.Next() {
		var failure overviewFailure
		if err := rows.Scan(
			&failure.RequestID, &failure.ModelAlias, &failure.ProviderName,
			&failure.CredentialLabel, &failure.StatusCode, &failure.ErrorCode,
			&failure.LatencyMS, &failure.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, failure)
	}
	return result, rows.Err()
}

func (s *Server) overviewRedisState(ctx context.Context, providers []Provider) overviewRedisSnapshot {
	result := overviewRedisSnapshot{
		Buckets: map[string]redisBucket{}, Cooldowns: map[string]time.Duration{},
		CooldownUnknown: map[string]bool{}, Cursors: map[string]int64{}, CursorUnknown: map[string]bool{},
	}
	constraints := map[string]limitConstraint{}
	credentialIDs := map[string]bool{}
	modelIDs := map[string]bool{}
	for _, provider := range providers {
		for _, credential := range provider.Credentials {
			credentialIDs[credential.ID] = true
			for _, model := range provider.Models {
				modelIDs[model.ID] = true
				runtime := credentialRuntime{CredentialView: credential}
				items, _ := buildConstraints(runtime, model.ID, 0)
				for _, item := range items {
					constraints[item.Key] = item
				}
			}
		}
	}
	pipe := s.redis.Pipeline()
	bucketCommands := map[string]*redis.SliceCmd{}
	ttlCommands := map[string]*redis.DurationCmd{}
	cursorCommands := map[string]*redis.StringCmd{}
	for key := range constraints {
		bucketCommands[key] = pipe.HMGet(ctx, key, "count", "bucket")
	}
	for id := range credentialIDs {
		ttlCommands[id] = pipe.TTL(ctx, "cooldown:"+id)
	}
	for id := range modelIDs {
		cursorCommands[id] = pipe.Get(ctx, "rr:"+id)
	}
	_, pipelineErr := pipe.Exec(ctx)
	globalUnknown := pipelineErr != nil && !errors.Is(pipelineErr, redis.Nil)
	for key, command := range bucketCommands {
		values, err := command.Result()
		state := redisBucket{Unknown: globalUnknown || err != nil}
		if len(values) == 2 {
			state.Count, _ = strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
			state.Bucket, _ = strconv.ParseInt(fmt.Sprint(values[1]), 10, 64)
		}
		result.Buckets[key] = state
	}
	for id, command := range ttlCommands {
		ttl, err := command.Result()
		result.Cooldowns[id] = ttl
		result.CooldownUnknown[id] = globalUnknown || err != nil
	}
	for id, command := range cursorCommands {
		value, err := command.Int64()
		if errors.Is(err, redis.Nil) {
			value, err = 0, nil
		}
		result.Cursors[id] = value
		result.CursorUnknown[id] = globalUnknown || err != nil
	}
	return result
}

func cachedCredentialCapacity(
	credential CredentialView,
	modelID string,
	snapshot overviewRedisSnapshot,
	now time.Time,
) overviewCredential {
	result := overviewCredential{
		ID: credential.ID, Label: credential.Label, SecretSuffix: credential.SecretSuffix,
		Primary: credential.IsPrimary, Status: "healthy", ValidationError: credential.ValidationError,
		LastValidatedAt: credential.LastValidatedAt,
	}
	if !credential.Enabled {
		result.Status = "disabled"
		return result
	}
	if credential.Status == "quarantined" {
		result.Status = "quarantined"
		return result
	}
	if snapshot.CooldownUnknown[credential.ID] {
		result.Status, result.Unknown = "unknown", true
		return result
	}
	if ttl := snapshot.Cooldowns[credential.ID]; ttl > 0 {
		result.Status = "cooldown"
		until := now.Add(ttl)
		result.CooldownUntil = &until
	}
	runtime := credentialRuntime{CredentialView: credential}
	constraints, _ := buildConstraints(runtime, modelID, 0)
	nowMS := now.UnixMilli()
	for _, constraint := range constraints {
		state := snapshot.Buckets[constraint.Key]
		if state.Unknown {
			result.Status, result.Unknown = "unknown", true
			return result
		}
		count := state.Count
		currentBucket := nowMS / constraint.WindowMS
		if state.Bucket != currentBucket {
			count = 0
		}
		remaining := constraint.Capacity - count
		if remaining < 0 {
			remaining = 0
		}
		parts := strings.Split(constraint.Key, ":")
		scope := "shared"
		if strings.Contains(constraint.Key, ":model:") {
			scope = "model"
		}
		reset := time.UnixMilli((currentBucket + 1) * constraint.WindowMS).UTC()
		candidate := &overviewHeadroom{
			Dimension: strings.ToUpper(parts[len(parts)-1]), Scope: scope,
			Remaining: remaining, Limit: constraint.Capacity, ResetAt: &reset,
		}
		if constraint.Token {
			result.Token = tighterOverviewHeadroom(result.Token, candidate)
		} else {
			result.Request = tighterOverviewHeadroom(result.Request, candidate)
		}
	}
	for _, scoped := range []struct {
		scope  string
		policy RatePolicy
	}{{scope: "shared", policy: credential.Limits}, {scope: "model", policy: credential.ModelLimits[modelID]}} {
		if scoped.policy.TPR != nil {
			result.Token = tighterOverviewHeadroom(result.Token, &overviewHeadroom{
				Dimension: "TPR", Scope: scoped.scope, Remaining: *scoped.policy.TPR, Limit: *scoped.policy.TPR,
			})
		}
	}
	if result.Status == "healthy" && ((result.Request != nil && result.Request.Remaining == 0) ||
		(result.Token != nil && result.Token.Remaining == 0)) {
		result.Status = "exhausted"
	}
	return result
}

func tighterOverviewHeadroom(current, candidate *overviewHeadroom) *overviewHeadroom {
	if current == nil {
		return candidate
	}
	if current.Limit <= 0 {
		return candidate
	}
	if candidate.Limit <= 0 {
		return current
	}
	if float64(candidate.Remaining)/float64(candidate.Limit) <
		float64(current.Remaining)/float64(current.Limit) {
		return candidate
	}
	return current
}

func aggregateProviderCapacity(
	credentials []CredentialView,
	snapshot overviewRedisSnapshot,
	now time.Time,
) map[string]overviewLimit {
	result := make(map[string]overviewLimit, len(aggregateDimensions))
	for _, dimension := range aggregateDimensions {
		aggregate := overviewLimit{}
		for _, credential := range credentials {
			if !credential.Enabled || credential.Status != "healthy" ||
				snapshot.CooldownUnknown[credential.ID] || snapshot.Cooldowns[credential.ID] > 0 {
				continue
			}
			value := dimension.value(credential.Limits)
			if value == nil {
				aggregate = overviewLimit{Unlimited: true}
				break
			}
			if dimension.max {
				if *value > aggregate.Limit {
					aggregate.Limit, aggregate.Remaining = *value, *value
				}
				continue
			}
			aggregate.Limit += *value
			windowMS := dimension.window.Milliseconds()
			key := "limit:" + credential.ID + ":all:" + dimension.name
			state := snapshot.Buckets[key]
			if state.Unknown {
				aggregate.Unknown = true
				continue
			}
			currentBucket := now.UnixMilli() / windowMS
			count := state.Count
			if state.Bucket != currentBucket {
				count = 0
			}
			remaining := *value - count
			if remaining < 0 {
				remaining = 0
			}
			aggregate.Remaining += remaining
			reset := time.UnixMilli((currentBucket + 1) * windowMS).UTC()
			aggregate.ResetAt = &reset
		}
		result[dimension.name] = aggregate
	}
	return result
}

func appendLowCapacityAlerts(alerts *[]overviewAlert, route overviewRoute) {
	if !route.Enabled {
		return
	}
	for _, segment := range route.Segments {
		for _, headroom := range []*overviewHeadroom{segment.Request, segment.Token} {
			if segment.Status != "healthy" || headroom == nil || headroom.Limit <= 0 {
				continue
			}
			ratio := float64(headroom.Remaining) / float64(headroom.Limit)
			if ratio <= 0.20 {
				*alerts = append(*alerts, overviewAlert{
					ID:       "capacity:" + route.ID + ":" + segment.ID + ":" + strings.ToLower(headroom.Dimension),
					Severity: "warning", ResourceType: "route", ResourceID: route.ID,
					Title:  route.Alias + " has low " + headroom.Dimension + " headroom",
					Detail: fmt.Sprintf("%s has %d of %d remaining.", segment.Label, headroom.Remaining, headroom.Limit),
				})
			}
		}
	}
}

func sortOverviewAlerts(alerts []overviewAlert) {
	priority := map[string]int{"critical": 0, "warning": 1, "info": 2}
	sort.SliceStable(alerts, func(i, j int) bool {
		left, right := priority[alerts[i].Severity], priority[alerts[j].Severity]
		if left != right {
			return left < right
		}
		return alerts[i].Title < alerts[j].Title
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
