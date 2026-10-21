package app

import "testing"

func TestConfiguredFallbackBypassesOtherPrimaryKeysAfterFailure(t *testing.T) {
	a := poolCandidate("a", "ma", "a1", true)
	a.Route.Provider.AlternativeProviderID = "b"
	a2 := poolCandidate("a", "ma", "a2", false)
	b := poolCandidate("b", "mb", "b1", true)
	b.Route.FallbackOnly = true
	skipped := map[string]bool{}
	skipFailedPrimary([]routeCandidate{a, a2, b}, a, skipped)
	if !skipped[a.key()] || !skipped[a2.key()] || skipped[b.key()] {
		t.Fatalf("wrong failover skips: %v", skipped)
	}
}

func TestProviderAlternativesNeverReceiveNormalRotation(t *testing.T) {
	primary := poolCandidate("primary", "model-primary", "primary-key", true)
	backup := poolCandidate("backup", "model-backup", "backup-key", true)
	other := poolCandidate("other", "model-other", "other-key", true)
	routes := filterProviderFallbackRoutes([]routeRuntime{backup.Route, primary.Route, other.Route}, map[string]string{"primary": "backup"}, map[string]bool{"primary": true})
	candidates := []routeCandidate{}
	for _, route := range routes {
		c := poolCandidate(route.Provider.ID, route.Model.ID, route.Provider.ID+"-key", true)
		c.Route = route
		candidates = append(candidates, c)
	}
	// Even an agent preference and a rotated cursor cannot promote a backup
	// above a healthy primary. The selector skips exhausted primary candidates
	// before reaching this final tier.
	candidates[0].repairPreferred = true
	for cursor := int64(1); cursor <= 12; cursor++ {
		order := candidateSelectionOrder(candidates, cursor)
		if len(order) != 3 || candidates[order[0]].Route.FallbackOnly || !candidates[order[2]].Route.FallbackOnly {
			t.Fatalf("backup promoted into normal rotation: %v", order)
		}
	}
}

func TestProviderAlternativeRequiresMatchingPrimaryAlias(t *testing.T) {
	backup := poolCandidate("backup", "backup-model", "key", true).Route
	links := map[string]string{"primary": "backup"}
	if got := filterProviderFallbackRoutes([]routeRuntime{backup}, links, map[string]bool{}); len(got) != 0 {
		t.Fatal("unmatched backup alias became a normal route")
	}
	// A disabled provider is absent from the live route list, but its configured
	// matching alias still authorizes failover.
	got := filterProviderFallbackRoutes([]routeRuntime{backup}, links, map[string]bool{"primary": true})
	if len(got) != 1 || !got[0].FallbackOnly {
		t.Fatal("disabled primary stranded its alternative")
	}
}
