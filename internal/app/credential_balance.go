package app

import (
	"context"
	"fmt"
	"time"
)

// A key's balance is the operator's own accounting of the credit sitting on that
// upstream account. The gateway cannot read a provider's real balance, so it
// tracks what it spent: every served request adds its estimated cost, and a key
// stops routing once the credit is used up.
//
// Only keys with a balance set are touched. A NULL balance means "not tracked",
// which is the default and routes exactly as it always did.

// requestSpendUSD prices one served request with the same arithmetic the overview
// uses for its cost column, so the balance and the dashboard can never disagree
// about what a request cost.
func requestSpendUSD(model ModelRoute, inputTokens, outputTokens int64) float64 {
	return estimatedModelCost(overviewRouteStats{
		Requests: 1, InputTokens: inputTokens, OutputTokens: outputTokens,
	}, model)
}

// recordCredentialSpend adds one request's cost to the key's running total. The
// WHERE clause skips untracked keys so an install that never sets a balance pays
// nothing for this feature, and the write is best-effort: losing a fraction of a
// cent must never fail a request that already succeeded.
func (s *Server) recordCredentialSpend(ctx context.Context, credentialID string, amount float64) {
	if credentialID == "" || amount <= 0 {
		return
	}
	writeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if _, err := s.db.Exec(writeContext, `
		UPDATE credentials
		SET balance_spent_usd = balance_spent_usd + $2, updated_at = NOW()
		WHERE id = $1 AND balance_usd IS NOT NULL
	`, credentialID, amount); err != nil {
		s.logger.Warn("credential balance write failed", "credential_id", credentialID, "error", err)
	}
}

// recordProviderSpend charges a request whose credential is not known to the
// provider instead. The gateway always knows which provider served a request even
// when the attempt ended before a key was chosen, so the cost is still subtracted
// from that account's pooled credit rather than disappearing.
func (s *Server) recordProviderSpend(ctx context.Context, providerID string, amount float64) {
	if providerID == "" || amount <= 0 {
		return
	}
	writeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if _, err := s.db.Exec(writeContext, `
		UPDATE providers
		SET balance_spent_usd = balance_spent_usd + $2, updated_at = NOW()
		WHERE id = $1
	`, providerID, amount); err != nil {
		s.logger.Warn("provider balance write failed", "provider_id", providerID, "error", err)
	}
}

// balanceRoutingDecision explains why a key was passed over, or nil when the key
// still has credit. It exists so the pooled and single-route selectors report the
// skip identically in the request log.
func balanceRoutingDecision(credential credentialRuntime) *RoutingDecision {
	if !credential.BalanceExhausted() {
		return nil
	}
	return &RoutingDecision{
		CredentialID: credential.ID, CredentialLabel: credential.Label,
		Reason: "balance_exhausted",
	}
}

// creditRemaining is the console-facing balance of one key. The console leads
// with RemainingUSD because that is the number an operator acts on; BalanceUSD is
// carried alongside only so the low-balance ratio can be computed.
type creditRemaining struct {
	BalanceUSD   float64 `json:"balance_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
	Exhausted    bool    `json:"exhausted"`
	// Requests is how many logged requests this key served in the selected range.
	// It answers "which key is burning the credit", which the balance alone cannot.
	Requests int64 `json:"requests"`
	// Errors is that key's failures in the same range, so a key that is spending
	// without succeeding is visible next to its cost.
	Errors int64 `json:"errors"`
	// Tokens is the input plus output tokens the key moved in the range.
	Tokens int64 `json:"tokens"`
}

// creditTotals sums the tracked keys in a provider or across the gateway.
// TrackedKeys is reported so the console can tell "nobody tracks a balance" from
// "every tracked key is at zero".
type creditTotals struct {
	TrackedKeys  int     `json:"tracked_keys"`
	BalanceUSD   float64 `json:"balance_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
	ExhaustedKey int     `json:"exhausted_keys"`
	// UnattributedSpentUSD is spend that was charged to the provider because the
	// request finished without a recorded key. It is already included in SpentUSD
	// and subtracted from RemainingUSD; it is reported separately so the console
	// can explain a gap between the keys' own figures and the provider's.
	UnattributedSpentUSD float64 `json:"unattributed_spent_usd"`
}

func credentialCredit(credential CredentialView) *creditRemaining {
	remaining := credential.BalanceRemainingUSD()
	if remaining == nil {
		return nil
	}
	return &creditRemaining{
		BalanceUSD: *credential.BalanceUSD, SpentUSD: credential.BalanceSpentUSD,
		RemainingUSD: *remaining, Exhausted: *remaining <= 0,
	}
}

// applyCredentialUsage attaches the key's traffic for the selected range to its
// balance figures, so "how much is left" and "who is spending it" arrive in the
// console as one object. Untracked keys carry no credit and so carry no usage.
func applyCredentialUsage(credit *creditRemaining, usage credentialUsage) {
	if credit == nil {
		return
	}
	credit.Requests, credit.Errors, credit.Tokens = usage.Requests, usage.Errors, usage.Tokens
}

// addCredit folds one key into a running total. Untracked keys are ignored so
// they cannot dilute the numbers the operator loaded.
func (t *creditTotals) addCredit(credit *creditRemaining) {
	if credit == nil {
		return
	}
	t.TrackedKeys++
	t.BalanceUSD += credit.BalanceUSD
	t.SpentUSD += credit.SpentUSD
	t.RemainingUSD += credit.RemainingUSD
	if credit.Exhausted {
		t.ExhaustedKey++
	}
}

// merge accumulates a provider's totals into the gateway-wide totals.
func (t *creditTotals) merge(other creditTotals) {
	t.TrackedKeys += other.TrackedKeys
	t.BalanceUSD += other.BalanceUSD
	t.SpentUSD += other.SpentUSD
	t.RemainingUSD += other.RemainingUSD
	t.ExhaustedKey += other.ExhaustedKey
	t.UnattributedSpentUSD += other.UnattributedSpentUSD
}

// chargeUnattributed folds a provider's pooled spend into its totals. It is the
// cost of requests that no single key could be charged for, so it comes off the
// remaining credit exactly as a key's own spend does, and never drives the
// remaining figure below zero.
func (t *creditTotals) chargeUnattributed(amount float64) {
	if amount <= 0 || t.TrackedKeys == 0 {
		return
	}
	t.UnattributedSpentUSD += amount
	t.SpentUSD += amount
	if t.RemainingUSD -= amount; t.RemainingUSD < 0 {
		t.RemainingUSD = 0
	}
}

// lowBalanceRatio is the share of the loaded credit left that still counts as
// worth warning about. It matches the 20% threshold the rate-limit headroom
// alerts already use, so one mental model covers both.
const lowBalanceRatio = 0.20

// balanceAlert describes a tracked key that is out of credit or close to it, or
// returns nil when there is nothing to say.
func balanceAlert(providerName string, credential CredentialView) *overviewAlert {
	credit := credentialCredit(credential)
	if credit == nil {
		return nil
	}
	// A tracked balance of zero is the state that most needs saying: the key is
	// switched off by its own accounting and no request will reach it, yet nothing
	// about the key itself looks wrong. It is usually the mark of a per-key balance
	// applied across a provider before an amount was set.
	if credit.BalanceUSD <= 0 {
		return &overviewAlert{
			ID: "balance:" + credential.ID, Severity: "critical",
			ResourceType: "credential", ResourceID: credential.ID,
			Title: credential.Label + " has no balance",
			Detail: fmt.Sprintf("%s · %s is tracked with a balance of $0.00, so it receives no traffic. Set a balance, or stop tracking one on this key.",
				providerName, credential.Label),
		}
	}
	if credit.Exhausted {
		return &overviewAlert{
			ID: "balance:" + credential.ID, Severity: "critical",
			ResourceType: "credential", ResourceID: credential.ID,
			Title: credential.Label + " is out of balance",
			Detail: fmt.Sprintf("%s · %s has spent its %s of credit and no longer receives traffic. Raise its balance to bring it back.",
				providerName, credential.Label, formatUSDAmount(credit.BalanceUSD)),
		}
	}
	if credit.RemainingUSD/credit.BalanceUSD > lowBalanceRatio {
		return nil
	}
	return &overviewAlert{
		ID: "balance:" + credential.ID, Severity: "warning",
		ResourceType: "credential", ResourceID: credential.ID,
		Title: credential.Label + " is low on balance",
		Detail: fmt.Sprintf("%s · %s has %s left of %s.",
			providerName, credential.Label, formatUSDAmount(credit.RemainingUSD), formatUSDAmount(credit.BalanceUSD)),
	}
}

// formatUSDAmount keeps cents for small amounts and drops to whole cents for
// larger ones, so an alert reads like money rather than like a float.
func formatUSDAmount(amount float64) string {
	if amount > 0 && amount < 0.01 {
		return "less than $0.01"
	}
	return fmt.Sprintf("$%.2f", amount)
}

// balanceBlockedEveryCandidate reports whether an out-of-credit balance, and
// nothing else, is why no candidate could serve. It turns the generic "at
// capacity" answer into one the operator can act on, because waiting will not
// help: only a top-up will.
func balanceBlockedEveryCandidate(decisions []RoutingDecision) bool {
	return soleBlockingReason(decisions) == "balance_exhausted"
}

// soleBlockingReason names the one reason every candidate was passed over, or ""
// when the pool was blocked for more than one reason or for none.
//
// It exists because "no key could serve" has several very different causes and
// only some of them are worth waiting out. A rate limit resolves itself, so it is
// backpressure and answers 429. A quarantined, spent or switched-off pool does
// not: retrying forever is the wrong advice, and the caller needs to be told
// which of the three it was. Anything mixed stays backpressure, because at least
// one candidate could come back on its own.
func soleBlockingReason(decisions []RoutingDecision) string {
	if len(decisions) == 0 {
		return ""
	}
	reason := decisions[0].Reason
	for _, decision := range decisions[1:] {
		if decision.Reason != reason {
			return ""
		}
	}
	return reason
}

// unavailablePoolMessage turns a terminal blocking reason into the sentence the
// caller receives, or returns ok=false when the reason is one that time will
// clear and the request should be reported as backpressure instead.
func unavailablePoolMessage(reason, providerName string) (string, bool) {
	where := "for this model"
	if providerName != "" {
		where = "on " + providerName
	}
	switch reason {
	case "quarantined":
		return "Every API key " + where + " was rejected by the provider. Replace one, or check it again from the console.", true
	case "disabled":
		return "Every API key " + where + " is turned off.", true
	}
	return "", false
}

// credentialBlockDecisions describes why each key in a list cannot be reached for
// a reason that will not clear on its own. A key that could serve, or that is only
// held for a cooldown or a limit, contributes nothing — so a full-length result
// means the whole list is terminally blocked and soleBlockingReason can name it.
//
// The batch and resource paths need this because their selectors skip unusable
// keys silently rather than recording a routing decision: without it, a pool of
// rejected keys answers "no capacity for this batch", which reads as a rate limit
// and invites a retry that cannot succeed.
func credentialBlockDecisions(credentials []credentialRuntime) []RoutingDecision {
	decisions := make([]RoutingDecision, 0, len(credentials))
	for _, credential := range credentials {
		reason := ""
		switch {
		case !credential.Enabled:
			reason = "disabled"
		case credential.Status == "quarantined":
			reason = "quarantined"
		case credential.BalanceExhausted():
			reason = "balance_exhausted"
		default:
			continue
		}
		decisions = append(decisions, RoutingDecision{
			CredentialID: credential.ID, CredentialLabel: credential.Label, Reason: reason,
		})
	}
	if len(decisions) != len(credentials) {
		return nil
	}
	return decisions
}
