package app

import "testing"

func TestCompareVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"v0.2.0", "0.1.9", 1},
		{"1.0.0", "1.0.0", 0},
		{"0.9.1", "0.10.0", -1},
		{"1.2.0-beta.1", "1.1.9", 1},
	} {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}
