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

// TestBalanceRoutingDecisionNeverSkipsUntrackedKeys guards the opt-in promise of
// the whole feature: an install that never loads a balance must keep routing even
// after its keys have run up an arbitrary amount of recorded spend.
func TestBalanceRoutingDecisionNeverSkipsUntrackedKeys(t *testing.T) {
	untracked := credentialRuntime{CredentialView: balanceCredential(nil, 9_999)}
	if decision := balanceRoutingDecision(untracked); decision != nil {
		t.Fatalf("an untracked key was refused: %#v", decision)
	}
}

// TestChargeUnattributedComesOffTheRemainingCredit covers the case where a
// request ended before a key was picked: the cost still has to leave the
// provider's remaining figure, or the dashboard shows credit that is gone.
func TestChargeUnattributedComesOffTheRemainingCredit(t *testing.T) {
	var totals creditTotals
	totals.addCredit(credentialCredit(balanceCredential(usd(20), 5)))
	totals.chargeUnattributed(2.5)

	if totals.UnattributedSpentUSD != 2.5 {
		t.Fatalf("unattributed spend = %v, want 2.5", totals.UnattributedSpentUSD)
	}
	// The pooled cost is folded into SpentUSD as well, which is what lets the
	// console explain the gap between the keys' own figures and the provider's.
	if totals.SpentUSD != 7.5 || totals.RemainingUSD != 12.5 {
		t.Fatalf("totals = %#v, want spent 7.5 and remaining 12.5", totals)
	}
	if totals.BalanceUSD != 20 {
		t.Fatalf("balance = %v, want the loaded credit to be untouched", totals.BalanceUSD)
	}
}

// TestChargeUnattributedClampsRemainingAtZero keeps a provider that overspent its
// pooled credit from reading as negative, which would render as a nonsense
// balance and break the low-balance ratio.
func TestChargeUnattributedClampsRemainingAtZero(t *testing.T) {
	var totals creditTotals
	totals.addCredit(credentialCredit(balanceCredential(usd(4), 1)))
	totals.chargeUnattributed(10)

	if totals.RemainingUSD != 0 {
		t.Fatalf("remaining = %v, want 0 rather than a negative amount", totals.RemainingUSD)
	}
	// Spend is not clamped: the operator still needs to see that more was burnt
	// than was ever loaded.
	if totals.SpentUSD != 11 || totals.UnattributedSpentUSD != 10 {
		t.Fatalf("totals = %#v, want spent 11 and unattributed 10", totals)
	}
}

// TestChargeUnattributedIgnoresProvidersWithNoTrackedKeys documents that pooled
// spend is dropped when nobody on the provider tracks a balance, so an install
// that opted out never sees invented money figures.
func TestChargeUnattributedIgnoresProvidersWithNoTrackedKeys(t *testing.T) {
	var totals creditTotals
	totals.addCredit(credentialCredit(balanceCredential(nil, 50)))
	totals.chargeUnattributed(3)

	if totals != (creditTotals{}) {
		t.Fatalf("totals = %#v, want an untouched zero value", totals)
	}
}

// TestChargeUnattributedIgnoresNonPositiveAmounts matters because the provider
// row starts at zero spend, and every provider is charged unconditionally while
// building the overview.
func TestChargeUnattributedIgnoresNonPositiveAmounts(t *testing.T) {
	totals := creditTotals{TrackedKeys: 1, BalanceUSD: 10, SpentUSD: 2, RemainingUSD: 8}
	totals.chargeUnattributed(0)
	totals.chargeUnattributed(-5)
	want := creditTotals{TrackedKeys: 1, BalanceUSD: 10, SpentUSD: 2, RemainingUSD: 8}
	if totals != want {
		t.Fatalf("totals = %#v, want %#v", totals, want)
	}
}

// TestCreditTotalsMergeCarriesUnattributedSpend keeps the gateway-wide figure
// able to explain itself: without this the pooled spend would be visible per
// provider but vanish from the header totals.
func TestCreditTotalsMergeCarriesUnattributedSpend(t *testing.T) {
	gateway := creditTotals{TrackedKeys: 1, BalanceUSD: 10, SpentUSD: 4, RemainingUSD: 6, UnattributedSpentUSD: 1}
	gateway.merge(creditTotals{TrackedKeys: 1, BalanceUSD: 5, SpentUSD: 3, RemainingUSD: 2, UnattributedSpentUSD: 2.5})
	if gateway.UnattributedSpentUSD != 3.5 {
		t.Fatalf("unattributed spend = %v, want 3.5", gateway.UnattributedSpentUSD)
	}
	if gateway.SpentUSD != 7 || gateway.RemainingUSD != 8 {
		t.Fatalf("merged = %#v, want spent 7 and remaining 8", gateway)
	}
}

// TestApplyCredentialUsageAttachesTraffic covers the question the balance alone
// cannot answer, "which key is burning the credit", which the console shows next
// to the remaining figure.
func TestApplyCredentialUsageAttachesTraffic(t *testing.T) {
	credit := credentialCredit(balanceCredential(usd(10), 2))
	applyCredentialUsage(credit, credentialUsage{Requests: 41, Errors: 3, Tokens: 9_100})
	if credit.Requests != 41 || credit.Errors != 3 || credit.Tokens != 9_100 {
		t.Fatalf("credit = %#v", credit)
	}
	// Usage is traffic for the selected range only and must not disturb the money
	// figures, which accumulate on the credentials row instead.
	if credit.RemainingUSD != 8 || credit.SpentUSD != 2 {
		t.Fatalf("usage changed the balance figures: %#v", credit)
	}
}

// TestApplyCredentialUsageIsANoOpForUntrackedKeys matters because the overview
// walks every key and looks up its stats, including the untracked ones that
// carry no credit at all.
func TestApplyCredentialUsageIsANoOpForUntrackedKeys(t *testing.T) {
	credit := credentialCredit(balanceCredential(nil, 0))
	if credit != nil {
		t.Fatalf("credit = %#v, want nil", credit)
	}
	applyCredentialUsage(credit, credentialUsage{Requests: 7})
}
