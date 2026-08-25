package app

import (
	"strings"
	"testing"
)

// TestProviderDisableWarningsNamesTheStrandedAliases covers the point of the
// warning: turning a provider off is safe when something else serves the same
// alias, and a real outage when nothing does.
func TestProviderDisableWarningsNamesTheStrandedAliases(t *testing.T) {
	warnings := providerDisableWarnings("Azure", []string{"opus-5", "sonnet-5"}, false)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly one", warnings)
	}
	if !strings.Contains(warnings[0], "2 model aliases") ||
		!strings.Contains(warnings[0], "opus-5, sonnet-5") {
		t.Fatalf("the warning does not name the stranded aliases: %q", warnings[0])
	}
}

func TestProviderDisableWarningsStaySilentWhenPooled(t *testing.T) {
	if warnings := providerDisableWarnings("Azure", []string{}, false); len(warnings) != 0 {
		t.Fatalf("a fully pooled provider produced warnings: %#v", warnings)
	}
}

func TestProviderDisableWarningsUseSingularForOneAlias(t *testing.T) {
	warnings := providerDisableWarnings("Azure", []string{"opus-5"}, false)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "1 model alias will stop serving") {
		t.Fatalf("warnings = %#v", warnings)
	}
	if strings.Contains(warnings[0], "aliases") {
		t.Fatalf("a single alias was described in the plural: %q", warnings[0])
	}
}

// TestProviderDisableWarningsTruncateALongList keeps the message readable when a
// provider carries hundreds of aliases; the count still has to be exact.
func TestProviderDisableWarningsTruncateALongList(t *testing.T) {
	// Multi-character aliases, so asserting the tail is absent cannot accidentally
	// match a letter inside the sentence itself.
	aliases := []string{"m01", "m02", "m03", "m04", "m05", "m06", "m07", "m08"}
	warnings := providerDisableWarnings("Azure", aliases, false)
	if !strings.Contains(warnings[0], "8 model aliases") {
		t.Fatalf("the exact count was lost: %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "and 2 more") {
		t.Fatalf("the list was not truncated: %q", warnings[0])
	}
	if strings.Contains(warnings[0], "m07") || strings.Contains(warnings[0], "m08") {
		t.Fatalf("the truncated tail leaked into the message: %q", warnings[0])
	}
}

// TestProviderDisableWarningsFlagTheAnthropicDefault covers the second way a
// disable breaks traffic: Files and Batches resolve through one chosen provider.
func TestProviderDisableWarningsFlagTheAnthropicDefault(t *testing.T) {
	warnings := providerDisableWarnings("Azure", []string{}, true)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Files and Batches") {
		t.Fatalf("warnings = %#v", warnings)
	}
	both := providerDisableWarnings("Azure", []string{"opus-5"}, true)
	if len(both) != 2 {
		t.Fatalf("both impacts should be reported together: %#v", both)
	}
}
