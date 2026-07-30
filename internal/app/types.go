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
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	Models              []ModelRoute      `json:"models"`
	Credentials         []CredentialView  `json:"credentials"`
	Capacity            *ProviderCapacity `json:"capacity,omitempty"`
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
	ID                     string    `json:"id"`
	ProviderID             string    `json:"provider_id"`
	PublicAlias            string    `json:"public_alias"`
	UpstreamModel          string    `json:"upstream_model"`
	SupportsChat           bool      `json:"supports_chat"`
	SupportsResponses      bool      `json:"supports_responses"`
	DefaultMaxOutputTokens int       `json:"default_max_output_tokens"`
	Tokenizer              string    `json:"tokenizer"`
	CaptureBodies          bool      `json:"capture_bodies"`
	StripParameters        []string  `json:"strip_parameters"`
	Enabled                bool      `json:"enabled"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
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
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
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
	ID               string          `json:"id"`
	RequestID        string          `json:"request_id"`
	ModelAlias       string          `json:"model_alias"`
	ProviderName     string          `json:"provider_name"`
	CredentialLabel  string          `json:"credential_label"`
	Endpoint         string          `json:"endpoint"`
	Attempts         json.RawMessage `json:"attempts"`
	RoutingDecisions json.RawMessage `json:"routing_decisions"`
	StatusCode       int             `json:"status_code"`
	LatencyMS        int64           `json:"latency_ms"`
	InputTokens      int64           `json:"input_tokens"`
	OutputTokens     int64           `json:"output_tokens"`
	ErrorCode        string          `json:"error_code,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	Running          bool            `json:"running,omitempty"`
	BodyCaptured     bool            `json:"body_captured"`
	BodyTruncated    bool            `json:"body_truncated"`
	CreatedAt        time.Time       `json:"created_at"`
}

type AppSettings struct {
	GatewayKeyPrefix      string `json:"gateway_key_prefix"`
	MetadataRetentionDays int    `json:"metadata_retention_days"`
	BodyRetentionDays     int    `json:"body_retention_days"`
	MaxWaitMS             int    `json:"max_wait_ms"`
}

type sessionData struct {
	AdminID string `json:"admin_id"`
	CSRF    string `json:"csrf"`
}
