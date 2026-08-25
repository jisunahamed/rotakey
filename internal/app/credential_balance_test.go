package app

import (
	"strings"
	"testing"
)

func balanceCredential(balance *float64, spent float64) CredentialView {
	return CredentialView{
		ID: "cred-1", Label: "primary", Enabled: true, Status: "healthy",
		BalanceUSD: balance, BalanceSpentUSD: spent,
	}
}

func usd(amount float64) *float64 { return &amount }

// TestBalanceRemainingSeparatesUntrackedFromSpent covers the distinction the
// whole feature rests on: a nil balance is "not tracked" and must never read as
// zero, because zero is what removes a key from routing.
func TestBalanceRemainingSeparatesUntrackedFromSpent(t *testing.T) {
	untracked := balanceCredential(nil, 12)
	if untracked.BalanceRemainingUSD() != nil {
		t.Fatalf("an untracked key reported a remaining balance")
	}
	if untracked.BalanceExhausted() {
		t.Fatalf("an untracked key was treated as exhausted, which would stop routing it")
	}

	spent := balanceCredential(usd(10), 10)
	if remaining := spent.BalanceRemainingUSD(); remaining == nil || *remaining != 0 {
		t.Fatalf("remaining = %v, want 0", spent.BalanceRemainingUSD())
	}
	if !spent.BalanceExhausted() {
		t.Fatalf("a fully spent key is still routing")
	}
}

// TestBalanceRemainingClampsToZero keeps an overspent key from reading negative,
// so the console and the router agree there is simply nothing left.
func TestBalanceRemainingClampsToZero(t *testing.T) {
	overspent := balanceCredential(usd(5), 7.5)
	remaining := overspent.BalanceRemainingUSD()
	if remaining == nil || *remaining != 0 {
		t.Fatalf("remaining = %v, want 0 rather than a negative amount", remaining)
	}
	if !overspent.BalanceExhausted() {
		t.Fatalf("an overspent key is still routing")
	}
}

func TestBalanceRemainingReportsPartialCredit(t *testing.T) {
	partial := balanceCredential(usd(20), 4.5)
	remaining := partial.BalanceRemainingUSD()
	if remaining == nil || *remaining != 15.5 {
		t.Fatalf("remaining = %v, want 15.5", remaining)
	}
	if partial.BalanceExhausted() {
		t.Fatalf("a key with credit left was excluded from routing")
	}
}

// TestRequestSpendMatchesTheOverviewCost is the guarantee that the balance and
// the dashboard cost column can never disagree about what a request cost.
func TestRequestSpendMatchesTheOverviewCost(t *testing.T) {
	model := ModelRoute{
		InputCostPerMillionUSD: 3, OutputCostPerMillionUSD: 15, RequestCostUSD: usd(0.002),
	}
	spend := requestSpendUSD(model, 1_000_000, 1_000_000)
	want := estimatedModelCost(overviewRouteStats{Requests: 1, InputTokens: 1_000_000, OutputTokens: 1_000_000}, model)
	if spend != want {
		t.Fatalf("spend = %v, overview cost = %v", spend, want)
	}
	if spend != 18.002 {
		t.Fatalf("spend = %v, want 18.002", spend)
	}
}

func TestRequestSpendIsZeroForAFreeModel(t *testing.T) {
	if spend := requestSpendUSD(ModelRoute{}, 500, 500); spend != 0 {
		t.Fatalf("spend = %v, want 0 for a model with no configured pricing", spend)
	}
}

func TestCredentialCreditIsAbsentForUntrackedKeys(t *testing.T) {
	if credit := credentialCredit(balanceCredential(nil, 3)); credit != nil {
		t.Fatalf("credit = %#v, want nil so the console stays silent about money", credit)
	}
	credit := credentialCredit(balanceCredential(usd(8), 2))
	if credit == nil {
		t.Fatalf("a tracked key reported no credit")
	}
	if credit.BalanceUSD != 8 || credit.SpentUSD != 2 || credit.RemainingUSD != 6 || credit.Exhausted {
		t.Fatalf("credit = %#v", credit)
	}
}

// TestCreditTotalsIgnoreUntrackedKeys keeps keys that opted out of tracking from
// diluting the figures the operator actually loaded.
func TestCreditTotalsIgnoreUntrackedKeys(t *testing.T) {
	var totals creditTotals
	totals.addCredit(credentialCredit(balanceCredential(usd(10), 2)))
	totals.addCredit(credentialCredit(balanceCredential(nil, 100)))
	totals.addCredit(credentialCredit(balanceCredential(usd(5), 5)))

	if totals.TrackedKeys != 2 {
		t.Fatalf("tracked keys = %d, want 2", totals.TrackedKeys)
	}
	if totals.BalanceUSD != 15 || totals.SpentUSD != 7 || totals.RemainingUSD != 8 {
		t.Fatalf("totals = %#v", totals)
	}
	if totals.ExhaustedKey != 1 {
		t.Fatalf("exhausted keys = %d, want 1", totals.ExhaustedKey)
	}
}

func TestCreditTotalsMergeProvidersIntoTheGatewayTotal(t *testing.T) {
	first := creditTotals{TrackedKeys: 2, BalanceUSD: 30, SpentUSD: 10, RemainingUSD: 20, ExhaustedKey: 0}
	second := creditTotals{TrackedKeys: 1, BalanceUSD: 5, SpentUSD: 5, RemainingUSD: 0, ExhaustedKey: 1}
	first.merge(second)
	want := creditTotals{TrackedKeys: 3, BalanceUSD: 35, SpentUSD: 15, RemainingUSD: 20, ExhaustedKey: 1}
	if first != want {
		t.Fatalf("merged = %#v, want %#v", first, want)
	}
}

// TestBalanceAlertEscalatesWhenTheKeyIsSpent covers the three outcomes an
// operator can see: nothing, a warning, and a key that has stopped serving.
func TestBalanceAlertEscalatesWhenTheKeyIsSpent(t *testing.T) {
	if alert := balanceAlert("Azure", balanceCredential(nil, 0)); alert != nil {
		t.Fatalf("an untracked key raised an alert: %#v", alert)
	}
	if alert := balanceAlert("Azure", balanceCredential(usd(0), 0)); alert != nil {
		t.Fatalf("a zero-balance key raised an alert instead of being ignored: %#v", alert)
	}
	if alert := balanceAlert("Azure", balanceCredential(usd(10), 1)); alert != nil {
		t.Fatalf("a key with 90%% left raised an alert: %#v", alert)
	}

	warning := balanceAlert("Azure", balanceCredential(usd(10), 8))
	if warning == nil || warning.Severity != "warning" {
		t.Fatalf("a key at 20%% did not warn: %#v", warning)
	}
	if !strings.Contains(warning.Detail, "$2.00 left of $10.00") {
		t.Fatalf("the warning does not state the amounts: %q", warning.Detail)
	}

	critical := balanceAlert("Azure", balanceCredential(usd(10), 10))
	if critical == nil || critical.Severity != "critical" {
		t.Fatalf("a spent key did not raise a critical alert: %#v", critical)
	}
	if !strings.Contains(critical.Detail, "no longer receives traffic") {
		t.Fatalf("the alert does not say the key stopped serving: %q", critical.Detail)
	}
	if critical.ResourceType != "credential" || critical.ResourceID != "cred-1" {
		t.Fatalf("the alert does not point at the key: %#v", critical)
	}
}

// TestBalanceAlertsShareOneIDPerKey matters because the console keys the
// attention queue by ID: a key must not appear twice as it degrades.
func TestBalanceAlertsShareOneIDPerKey(t *testing.T) {
	warning := balanceAlert("Azure", balanceCredential(usd(10), 9))
	critical := balanceAlert("Azure", balanceCredential(usd(10), 10))
	if warning == nil || critical == nil || warning.ID != critical.ID {
		t.Fatalf("warning and critical alerts use different IDs: %#v vs %#v", warning, critical)
	}
}

func TestFormatUSDAmountReadsLikeMoney(t *testing.T) {
	cases := map[float64]string{
		0:        "$0.00",
		0.004:    "less than $0.01",
		0.01:     "$0.01",
		12.5:     "$12.50",
		1999.999: "$2000.00",
	}
	for amount, want := range cases {
		if got := formatUSDAmount(amount); got != want {
			t.Fatalf("formatUSDAmount(%v) = %q, want %q", amount, got, want)
		}
	}
}

// TestBalanceBlockedEveryCandidate decides whether the caller sees a 503 telling
// them to top up or a 429 telling them to retry, so both mixed and empty cases
// have to fall through to the rate-limit answer.
func TestBalanceBlockedEveryCandidate(t *testing.T) {
	if balanceBlockedEveryCandidate(nil) {
		t.Fatalf("no decisions at all was read as a balance problem")
	}
	if !balanceBlockedEveryCandidate([]RoutingDecision{
		{Reason: "balance_exhausted"}, {Reason: "balance_exhausted"},
	}) {
		t.Fatalf("an entirely spent pool was not recognised")
	}
	if balanceBlockedEveryCandidate([]RoutingDecision{
		{Reason: "balance_exhausted"}, {Reason: "cooldown"},
	}) {
		t.Fatalf("a pool that was partly rate limited was reported as spent")
	}
}

// TestBalanceRoutingDecisionExplainsTheSkip keeps the request log able to say
// why a key was passed over, which is the only way the operator can tell a spent
// key from a throttled one.
func TestBalanceRoutingDecisionExplainsTheSkip(t *testing.T) {
	healthy := credentialRuntime{CredentialView: balanceCredential(usd(10), 1)}
	if decision := balanceRoutingDecision(healthy); decision != nil {
		t.Fatalf("a key with credit was skipped: %#v", decision)
	}
	spent := credentialRuntime{CredentialView: balanceCredential(usd(10), 10)}
	decision := balanceRoutingDecision(spent)
	if decision == nil {
		t.Fatalf("a spent key produced no routing decision")
	}
	if decision.Reason != "balance_exhausted" || decision.CredentialID != "cred-1" || decision.CredentialLabel != "primary" {
		t.Fatalf("decision = %#v", decision)
	}
}
