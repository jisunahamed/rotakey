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
	Models              []ModelRoute      `json:"models,omitempty"`
	Credentials         []CredentialView  `json:"credentials,omitempty"`
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
	Enabled                bool      `json:"enabled"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type CredentialView struct {
	ID            string                `json:"id"`
	ProviderID    string                `json:"provider_id"`
	Label         string                `json:"label"`
	SecretSuffix  string                `json:"secret_suffix"`
	Enabled       bool                  `json:"enabled"`
	Status        string                `json:"status"`
	CooldownUntil *time.Time            `json:"cooldown_until,omitempty"`
	Limits        RatePolicy            `json:"limits"`
	ModelLimits   map[string]RatePolicy `json:"model_limits"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
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
	CredentialID    string `json:"credential_id"`
	CredentialLabel string `json:"credential_label"`
	StatusCode      int    `json:"status_code"`
	Error           string `json:"error,omitempty"`
	Retryable       bool   `json:"retryable"`
	DurationMS      int64  `json:"duration_ms"`
}

type RequestLog struct {
	ID              string          `json:"id"`
	RequestID       string          `json:"request_id"`
	ModelAlias      string          `json:"model_alias"`
	ProviderName    string          `json:"provider_name"`
	CredentialLabel string          `json:"credential_label"`
	Endpoint        string          `json:"endpoint"`
	Attempts        json.RawMessage `json:"attempts"`
	StatusCode      int             `json:"status_code"`
	LatencyMS       int64           `json:"latency_ms"`
	InputTokens     int64           `json:"input_tokens"`
	OutputTokens    int64           `json:"output_tokens"`
	ErrorCode       string          `json:"error_code,omitempty"`
	BodyCaptured    bool            `json:"body_captured"`
	BodyTruncated   bool            `json:"body_truncated"`
	CreatedAt       time.Time       `json:"created_at"`
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
