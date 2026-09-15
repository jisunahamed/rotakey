package app

import (
	"reflect"
	"testing"
)

func TestNormalizeRoutingMode(t *testing.T) {
	cases := map[string]string{
		"":         routingModeProvider,
		"provider": routingModeProvider,
		"model":    routingModeModel,
		" MODEL ":  routingModeModel,
		"pool":     "",
	}
	for input, want := range cases {
		if got := normalizeRoutingMode(input); got != want {
			t.Fatalf("normalizeRoutingMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAliasPrefixRoundTrip(t *testing.T) {
	if got := aliasWithoutProviderPrefix("azure/opus-5", "azure"); got != "opus-5" {
		t.Fatalf("stripped alias = %q", got)
	}
	// A bare alias is already model-wise, so stripping is a no-op.
	if got := aliasWithoutProviderPrefix("opus-5", "azure"); got != "opus-5" {
		t.Fatalf("bare alias was rewritten: %q", got)
	}
	// An alias that is only the prefix would strip to nothing, so it is kept.
	if got := aliasWithoutProviderPrefix("azure/", "azure"); got != "azure/" {
		t.Fatalf("empty result was accepted: %q", got)
	}
	if got := aliasWithProviderPrefix("opus-5", "azure"); got != "azure/opus-5" {
		t.Fatalf("prefixed alias = %q", got)
	}
	if got := aliasWithProviderPrefix("azure/opus-5", "azure"); got != "azure/opus-5" {
		t.Fatalf("prefix was applied twice: %q", got)
	}
	if got := aliasWithProviderPrefix("opus-5", ""); got != "opus-5" {
		t.Fatalf("missing slug changed the alias: %q", got)
	}
}

func TestModelFamilyKeyMatchesProviderCatalogVariants(t *testing.T) {
	cases := map[string]string{
		"DeepSeek-V4-Flash":                      "deepseek-v4-flash",
		"deepseek-v4-flash-0731":                 "deepseek-v4-flash",
		"vendor/deepseek_v4_flash_2026-07-31":    "deepseek-v4-flash",
		"accounts/acme/models/gemini-3.0-latest": "gemini-3-0",
		"deepseek-v4-flash-vision-exp":           "deepseek-v4-flash-vision-exp",
	}
	for input, want := range cases {
		if got := modelFamilyKey(input); got != want {
			t.Errorf("modelFamilyKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestConsolidateBulkModelsInModelMode(t *testing.T) {
	models := []modelInput{
		{PublicAlias: "deepseek-v4-flash", UpstreamModel: "deepseek-v4-flash-0731"},
		{PublicAlias: "deepseek-v4-flash", UpstreamModel: "DeepSeek-V4-Flash"},
		{PublicAlias: "deepseek-v4-flash-vision-exp", UpstreamModel: "deepseek-v4-flash-vision-exp"},
	}
	got, skipped, err := consolidateBulkModels(models, routingModeModel)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(got) != 2 {
		t.Fatalf("got %d models and %d skipped, want 2 and 1", len(got), skipped)
	}
	if got[0].UpstreamModel != "DeepSeek-V4-Flash" {
		t.Fatalf("kept %q, want the base catalog ID", got[0].UpstreamModel)
	}
}

func TestConsolidateBulkModelsRejectsUnrelatedCollision(t *testing.T) {
	models := []modelInput{
		{PublicAlias: "fast", UpstreamModel: "deepseek-v4-flash"},
		{PublicAlias: "fast", UpstreamModel: "qwen3.8-flash"},
	}
	if _, _, err := consolidateBulkModels(models, routingModeModel); err == nil {
		t.Fatal("unrelated models sharing an alias were accepted")
	}
	if _, _, err := consolidateBulkModels(models[:1], routingModeProvider); err != nil {
		t.Fatalf("one provider-wise model failed: %v", err)
	}
}

func TestPlanCanonicalAliasesPoolsProvidersAndConsolidatesDuplicates(t *testing.T) {
	rows := []routeAliasRow{
		{ModelID: "m1", ProviderID: "p1", Alias: "nvidia/DeepSeek-V4-Flash", UpstreamModel: "DeepSeek-V4-Flash"},
		{ModelID: "m2", ProviderID: "p1", Alias: "nvidia/deepseek-v4-flash-0731", UpstreamModel: "deepseek-v4-flash-0731"},
		{ModelID: "m3", ProviderID: "p2", Alias: "openrouter/deepseek-v4-flash", UpstreamModel: "deepseek/deepseek-v4-flash"},
		{ModelID: "m4", ProviderID: "p2", Alias: "vision", UpstreamModel: "deepseek-v4-flash-vision-exp"},
	}
	plan := planCanonicalAliases(rows)
	if !reflect.DeepEqual(plan.RemoveIDs, []string{"m2"}) {
		t.Fatalf("remove IDs = %#v", plan.RemoveIDs)
	}
	want := []aliasRewrite{
		{ModelID: "m1", From: "nvidia/DeepSeek-V4-Flash", To: "deepseek-v4-flash"},
		{ModelID: "m3", From: "openrouter/deepseek-v4-flash", To: "deepseek-v4-flash"},
		{ModelID: "m4", From: "vision", To: "deepseek-v4-flash-vision-exp"},
	}
	if !reflect.DeepEqual(plan.Rewrites, want) {
		t.Fatalf("rewrites = %#v, want %#v", plan.Rewrites, want)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("conflicts = %#v", plan.Conflicts)
	}
}

func TestPlanCanonicalAliasesKeepsUnrelatedOwner(t *testing.T) {
	rows := []routeAliasRow{
		{ModelID: "m1", ProviderID: "p1", Alias: "deepseek-v4-flash", UpstreamModel: "some-other-model"},
		{ModelID: "m2", ProviderID: "p1", Alias: "old-name", UpstreamModel: "deepseek-v4-flash-0731"},
	}
	plan := planCanonicalAliases(rows)
	if !reflect.DeepEqual(plan.Conflicts, []string{"deepseek-v4-flash"}) {
		t.Fatalf("conflicts = %#v", plan.Conflicts)
	}
	for _, rewrite := range plan.Rewrites {
		if rewrite.ModelID == "m2" {
			t.Fatalf("conflicting route was rewritten: %#v", rewrite)
		}
	}
}

func TestPlanAliasRewritesToModelMode(t *testing.T) {
	rows := []routeAliasRow{
		{ModelID: "m1", ProviderID: "p1", ProviderSlug: "azure", Alias: "azure/opus-5"},
		{ModelID: "m2", ProviderID: "p2", ProviderSlug: "bedrock", Alias: "bedrock/opus-5"},
		{ModelID: "m3", ProviderID: "p2", ProviderSlug: "bedrock", Alias: "sonnet-5"},
	}
	rewrites, conflicts := planAliasRewrites(rows, routingModeModel)
	want := []aliasRewrite{
		{ModelID: "m1", From: "azure/opus-5", To: "opus-5"},
		{ModelID: "m2", From: "bedrock/opus-5", To: "opus-5"},
	}
	if !reflect.DeepEqual(rewrites, want) {
		t.Fatalf("rewrites = %#v, want %#v", rewrites, want)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %#v", conflicts)
	}
}

func TestPlanAliasRewritesKeepsCollidingAliases(t *testing.T) {
	// Both routes belong to one provider, so collapsing the prefix would make two
	// different upstream models share one alias on the same provider.
	rows := []routeAliasRow{
		{ModelID: "m1", ProviderID: "p1", ProviderSlug: "azure", Alias: "opus-5"},
		{ModelID: "m2", ProviderID: "p1", ProviderSlug: "azure", Alias: "azure/opus-5"},
	}
	rewrites, conflicts := planAliasRewrites(rows, routingModeModel)
	if len(rewrites) != 0 {
		t.Fatalf("colliding rewrite was applied: %#v", rewrites)
	}
	if !reflect.DeepEqual(conflicts, []string{"azure/opus-5"}) {
		t.Fatalf("conflicts = %#v", conflicts)
	}
}

func TestPlanAliasRewritesToProviderMode(t *testing.T) {
	rows := []routeAliasRow{
		{ModelID: "m1", ProviderID: "p1", ProviderSlug: "azure", Alias: "opus-5"},
		{ModelID: "m2", ProviderID: "p2", ProviderSlug: "bedrock", Alias: "opus-5"},
		{ModelID: "m3", ProviderID: "p1", ProviderSlug: "azure", Alias: "azure/sonnet-5"},
	}
	rewrites, conflicts := planAliasRewrites(rows, routingModeProvider)
	want := []aliasRewrite{
		{ModelID: "m1", From: "opus-5", To: "azure/opus-5"},
		{ModelID: "m2", From: "opus-5", To: "bedrock/opus-5"},
	}
	if !reflect.DeepEqual(rewrites, want) {
		t.Fatalf("rewrites = %#v, want %#v", rewrites, want)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %#v", conflicts)
	}
}
