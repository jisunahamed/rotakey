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
