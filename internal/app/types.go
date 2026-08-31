package app

import (
	"encoding/json"
	"time"
)

type RatePolicy struct {
	RPS *int64 `json:"rps"`
	RPM *int64 `json:"rpm"`
	RPD *int64 `json:"rpd"`
	TPS *int64 `json:"tps"`
	TPM *int64 `json:"tpm"`
	TPD *int64 `json:"tpd"`
	TPR *int64 `json:"tpr"`
}

func (p RatePolicy) Valid() bool {
	values := []*int64{p.RPS, p.RPM, p.RPD, p.TPS, p.TPM, p.TPD, p.TPR}
	for _, value := range values {
		if value != nil && *value <= 0 {
			return false
		}
	}
	return true
}

type Provider struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Slug                string            `json:"slug"`
	BaseURL             string            `json:"base_url"`
	AuthHeader          string            `json:"auth_header"`
	AuthScheme          string            `json:"auth_scheme"`
	ExtraHeaders        map[string]string `json:"extra_headers"`
	TimeoutSeconds      int               `json:"timeout_seconds"`
	Enabled             bool              `json:"enabled"`
	AllowPrivateNetwork bool              `json:"allow_private_network"`
	APIFormat           string            `json:"api_format"`
	AnthropicVersion    string            `json:"anthropic_version"`
	// DefaultKeyBalanceUSD is the credit every new key on this provider starts
	// with. An operator with dozens of keys behind one account sets the figure once
	// here instead of typing it into each key. Nil means the provider seeds
	// nothing, so keys stay untracked and route forever.
	DefaultKeyBalanceUSD *float64 `json:"default_key_balance_usd,omitempty"`
	// BalanceSpentUSD collects spend that could not be charged to a single key,
	// which happens when a request finished without a recorded credential. It is
	// counted against the provider's pooled credit so the remaining figure on the
	// dashboard stays honest instead of quietly losing the cost.
	BalanceSpentUSD float64           `json:"balance_spent_usd"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Models          []ModelRoute      `json:"models"`
	Credentials     []CredentialView  `json:"credentials"`
	Capacity        *ProviderCapacity `json:"capacity,omitempty"`
}

type AggregateLimit struct {
	Limit     int64 `json:"limit"`
	Remaining int64 `json:"remaining"`
	Unlimited bool  `json:"unlimited"`
	Unknown   bool  `json:"unknown"`
}

type ProviderCapacity struct {
	TotalKeys int                       `json:"total_keys"`
	ReadyKeys int                       `json:"ready_keys"`
	Limits    map[string]AggregateLimit `json:"limits"`
}

type DiscoveredModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type ModelRoute struct {
	ID                      string            `json:"id"`
	ProviderID              string            `json:"provider_id"`
	PublicAlias             string            `json:"public_alias"`
	UpstreamModel           string            `json:"upstream_model"`
	SupportsChat            bool              `json:"supports_chat"`
	SupportsResponses       bool              `json:"supports_responses"`
	SupportsMessages        bool              `json:"supports_messages"`
	DefaultMaxOutputTokens  int               `json:"default_max_output_tokens"`
	InputCostPerMillionUSD  float64           `json:"input_cost_per_million_usd"`
	OutputCostPerMillionUSD float64           `json:"output_cost_per_million_usd"`
	RequestCostUSD          *float64          `json:"request_cost_usd,omitempty"`
	Tokenizer               string            `json:"tokenizer"`
	CaptureBodies           bool              `json:"capture_bodies"`
	StripParameters         []string          `json:"strip_parameters"`
	CapabilityStatus        string            `json:"capability_status"`
	CapabilityProfile       map[string]string `json:"capability_profile"`
	CapabilitiesCheckedAt   *time.Time        `json:"capabilities_checked_at,omitempty"`
	CapabilityError         string            `json:"capability_error,omitempty"`
	Enabled                 bool              `json:"enabled"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

type CredentialView struct {
	ID              string                `json:"id"`
	ProviderID      string                `json:"provider_id"`
	Label           string                `json:"label"`
	SecretSuffix    string                `json:"secret_suffix"`
	IsPrimary       bool                  `json:"is_primary"`
	Enabled         bool                  `json:"enabled"`
	Status          string                `json:"status"`
	CooldownUntil   *time.Time            `json:"cooldown_until,omitempty"`
	LastValidatedAt *time.Time            `json:"last_validated_at,omitempty"`
	ValidationError string                `json:"validation_error,omitempty"`
	Limits          RatePolicy            `json:"limits"`
	ModelLimits     map[string]RatePolicy `json:"model_limits"`
	// BalanceUSD is the credit the operator loaded onto this key, or nil when the
	// key's balance is not tracked. Nil and zero mean different things: untracked
	// keys route forever, a tracked key stops routing once its credit is used up.
	BalanceUSD *float64 `json:"balance_usd,omitempty"`
	// BalanceSpentUSD accumulates the estimated cost of every request this key
	// served. It is kept on the row rather than derived from request_logs so a
	// retention sweep cannot silently refill the key.
	BalanceSpentUSD float64   `json:"balance_spent_usd"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// BalanceRemainingUSD reports the credit left on the key, or nil when the
// balance is not tracked. It never goes negative: an overspent key reads as zero
// so both the console and the routing check see the same "nothing left".
func (c CredentialView) BalanceRemainingUSD() *float64 {
	if c.BalanceUSD == nil {
		return nil
	}
	remaining := *c.BalanceUSD - c.BalanceSpentUSD
	if remaining < 0 {
		remaining = 0
	}
	return &remaining
}

// BalanceExhausted reports whether a tracked key has no credit left, which is
// what removes it from routing.
func (c CredentialView) BalanceExhausted() bool {
	remaining := c.BalanceRemainingUSD()
	return remaining != nil && *remaining <= 0
}

type credentialRuntime struct {
	CredentialView
	Secret []byte
}

type routeRuntime struct {
	Model    ModelRoute
	Provider Provider
}

type AttemptRecord struct {
	CredentialID       string            `json:"credential_id"`
	CredentialLabel    string            `json:"credential_label"`
	StatusCode         int               `json:"status_code"`
	Error              string            `json:"error,omitempty"`
	ErrorMessage       string            `json:"error_message,omitempty"`
	Retryable          bool              `json:"retryable"`
	DurationMS         int64             `json:"duration_ms"`
	RemovedParameters  []string          `json:"removed_parameters,omitempty"`
	ReplacedParameters map[string]string `json:"replaced_parameters,omitempty"`
	// SwitchedEndpoint names the upstream endpoint the retry moved to, so a
	// repaired request reads as plainly as a removed or replaced parameter.
	SwitchedEndpoint string `json:"switched_endpoint,omitempty"`
}

type RoutingDecision struct {
	CredentialID    string     `json:"credential_id,omitempty"`
	CredentialLabel string     `json:"credential_label,omitempty"`
	Reason          string     `json:"reason"`
	Scope           string     `json:"scope,omitempty"`
	Dimension       string     `json:"dimension,omitempty"`
	Limit           int64      `json:"limit,omitempty"`
	Used            int64      `json:"used,omitempty"`
	Remaining       int64      `json:"remaining,omitempty"`
	Required        int64      `json:"required,omitempty"`
	RetryAfterMS    int64      `json:"retry_after_ms,omitempty"`
	ResetAt         *time.Time `json:"reset_at,omitempty"`
}

type RequestLog struct {
	ID                string          `json:"id"`
	RequestID         string          `json:"request_id"`
	ModelAlias        string          `json:"model_alias"`
	ProviderName      string          `json:"provider_name"`
	CredentialLabel   string          `json:"credential_label"`
	Endpoint          string          `json:"endpoint"`
	PublicProtocol    string          `json:"public_protocol"`
	UpstreamProtocol  string          `json:"upstream_protocol"`
	UpstreamRequestID string          `json:"upstream_request_id,omitempty"`
	Attempts          json.RawMessage `json:"attempts"`
	RoutingDecisions  json.RawMessage `json:"routing_decisions"`
	StatusCode        int             `json:"status_code"`
	LatencyMS         int64           `json:"latency_ms"`
	InputTokens       int64           `json:"input_tokens"`
	OutputTokens      int64           `json:"output_tokens"`
	ErrorCode         string          `json:"error_code,omitempty"`
	ErrorMessage      string          `json:"error_message,omitempty"`
	Running           bool            `json:"running,omitempty"`
	BodyCaptured      bool            `json:"body_captured"`
	BodyTruncated     bool            `json:"body_truncated"`
	CreatedAt         time.Time       `json:"created_at"`
}

type AppSettings struct {
	GatewayKeyPrefix           string `json:"gateway_key_prefix"`
	MetadataRetentionDays      int    `json:"metadata_retention_days"`
	BodyRetentionDays          int    `json:"body_retention_days"`
	MaxWaitMS                  int    `json:"max_wait_ms"`
	DefaultProviderTimeoutSecs int    `json:"default_provider_timeout_seconds"`
	DefaultAnthropicProviderID string `json:"default_anthropic_provider_id"`
	RoutingMode                string `json:"routing_mode"`
	BaseURL                    string `json:"base_url"`
}

type AnthropicResource struct {
	ID           string          `json:"id"`
	ResourceType string          `json:"resource_type"`
	UpstreamID   string          `json:"-"`
	ProviderID   string          `json:"provider_id"`
	CredentialID string          `json:"credential_id"`
	State        json.RawMessage `json:"state"`
	ModelAliases json.RawMessage `json:"model_aliases"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type sessionData struct {
	AdminID string `json:"admin_id"`
	CSRF    string `json:"csrf"`
}
