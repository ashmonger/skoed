package upgrade

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"0.4.1", "0.4.0", true},
		{"v0.4.1", "v0.4.0", true},
		{"0.4.0", "0.4.0", false},
		{"0.4.0", "0.4.1", false},
		{"1.0.0", "0.9.9", true},
		{"0.4.1-rc1", "0.4.0", true}, // pre-release suffix stripped
		// Dev/hash "current" (git describe --always with no tags fetched):
		// any real release must be considered an available upgrade. Regression
		// guard for the CI failure where "24091d2" parsed as [24091,0,0] and
		// made 99.0.0 look older.
		{"99.0.0", "24091d2", true},
		{"0.4.1", "89e0b29", true},
		{"0.4.1", "abcdef0", true},
		// A non-release candidate is never an upgrade.
		{"deadbee", "0.4.0", false},
		{"", "0.4.0", false},
	}
	for _, c := range cases {
		if got := isNewer(c.candidate, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

func TestSplitVersionValidity(t *testing.T) {
	valid := []string{"0.4.1", "v1.2.3", "10.20.30", "0.4.1-rc1"}
	invalid := []string{"24091d2", "89e0b29", "", "dev", "1.2", "1.2.3.4", "1.x.3"}
	for _, s := range valid {
		if _, ok := splitVersion(s); !ok {
			t.Errorf("splitVersion(%q): ok=false, want true", s)
		}
	}
	for _, s := range invalid {
		if _, ok := splitVersion(s); ok {
			t.Errorf("splitVersion(%q): ok=true, want false", s)
		}
	}
}
