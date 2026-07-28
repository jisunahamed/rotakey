package app

import "testing"

func TestEstimateInputTokensHeuristic(t *testing.T) {
	body := []byte(`{"model":"demo","messages":[{"role":"user","content":"hello"}]}`)
	want := int64(len(body)/3 + 16)
	if got := estimateInputTokens(body, "heuristic"); got != want {
		t.Fatalf("estimateInputTokens() = %d, want %d", got, want)
	}
}

func TestEstimateInputTokensKnownProfiles(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"ঢাকায় আজ আবহাওয়া কেমন? 🌦️ Please answer briefly."}]}`)
	heuristic := estimateInputTokens(body, "heuristic")

	for _, profile := range []string{"cl100k_base", "o200k_base"} {
		t.Run(profile, func(t *testing.T) {
			first := estimateInputTokens(body, profile)
			second := estimateInputTokens(body, profile)
			if first <= 0 {
				t.Fatalf("estimateInputTokens() = %d, want a positive estimate", first)
			}
			if first != second {
				t.Fatalf("estimateInputTokens() is not deterministic: %d then %d", first, second)
			}
			if first == heuristic {
				t.Fatalf("profile %q unexpectedly used the heuristic estimate %d", profile, heuristic)
			}
		})
	}
}
