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

// creditRemaining is the console-facing balance of one key.
type creditRemaining struct {
	BalanceUSD   float64 `json:"balance_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
	Exhausted    bool    `json:"exhausted"`
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
}

// lowBalanceRatio is the share of the loaded credit left that still counts as
// worth warning about. It matches the 20% threshold the rate-limit headroom
// alerts already use, so one mental model covers both.
const lowBalanceRatio = 0.20

// balanceAlert describes a tracked key that is out of credit or close to it, or
// returns nil when there is nothing to say.
func balanceAlert(providerName string, credential CredentialView) *overviewAlert {
	credit := credentialCredit(credential)
	if credit == nil || credit.BalanceUSD <= 0 {
		return nil
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
	if len(decisions) == 0 {
		return false
	}
	for _, decision := range decisions {
		if decision.Reason != "balance_exhausted" {
			return false
		}
	}
	return true
}
