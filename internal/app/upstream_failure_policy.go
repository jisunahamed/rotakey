package app

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Stop key rotation after two identical 5xx replies to the same payload.
// A changed repair payload or another route
// gets its own budget; capacity waits and credential rejections are excluded.
func repeatedFailureKey(candidate routeCandidate, plan upstreamPlan, outcome attemptOutcome) string {
	if outcome.Done || outcome.Compatibility || outcome.Status < 500 || outcome.Record.ErrorMessage == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s:%x:%d:%s", candidate.Route.Model.ID, plan.Path, sha256.Sum256(plan.Encoded), outcome.Status, outcome.Record.ErrorMessage)
}

// These errors describe the model/request contract, not the credential.
func credentialIndependentFailure(status int, message string) bool {
	if status < 400 {
		return false
	}
	message = strings.ToLower(message)
	for _, signal := range []string{"failed to apply prompt template", "requires --tool-call-parser", "requires --enable-auto-tool-choice"} {
		if strings.Contains(message, signal) {
			return true
		}
	}
	return false
}

func providerBackoffKey(providerID string) string {
	return "provider:admission:" + providerID + ":backoff"
}

func boundedCallerRequestID(value string) string {
	if len(value) > 128 {
		return ""
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			return ""
		}
	}
	return value
}

// A provider worker cap cannot be bypassed by rotating keys. Pause the provider
// and re-admit through its FIFO without treating busy capacity as an outage.
func providerConcurrencyHold(status int, headers http.Header, body []byte) (time.Duration, bool) {
	if status != http.StatusServiceUnavailable && status != http.StatusTooManyRequests {
		return 0, false
	}
	message := strings.ToLower(upstreamErrorMessage(body, nil))
	if !providerConcurrencyMessage(message) {
		return 0, false
	}
	hold := parseRetryAfter(headers.Get("Retry-After"))
	if hold <= 0 {
		hold = 2 * time.Second
	}
	return min(hold, 24*time.Hour), true
}

func providerConcurrencyMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "worker local total request limit reached") ||
		strings.Contains(message, "concurrency limit exceeded for user")
}
