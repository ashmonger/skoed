package handlers

import "testing"

func TestVersionFromAssetURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/ashmonger/skoed/releases/download/v0.4.0/skoed_0.4.0_linux_amd64.tar.gz": "0.4.0",
		"https://example.test/download/v1.2.3/skoed_1.2.3_linux_arm64.tar.gz":                        "1.2.3",
		"https://example.test/v0.4.0-rc1/skoed.tar.gz":                                               "0.4.0-rc1",
		"https://example.test/no-version/skoed.tar.gz":                                               "",
		"":                                                                                           "",
	}
	for url, want := range cases {
		if got := versionFromAssetURL(url); got != want {
			t.Errorf("versionFromAssetURL(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestSameVersion(t *testing.T) {
	yes := [][2]string{{"0.4.0", "0.4.0"}, {"v0.4.0", "0.4.0"}, {"0.4.0", "v0.4.0"}, {" 0.4.0 ", "0.4.0"}}
	no := [][2]string{{"0.3.4", "0.4.0"}, {"", ""}, {"", "0.4.0"}, {"0.4.0", ""}}
	for _, p := range yes {
		if !sameVersion(p[0], p[1]) {
			t.Errorf("sameVersion(%q,%q) = false, want true", p[0], p[1])
		}
	}
	for _, p := range no {
		if sameVersion(p[0], p[1]) {
			t.Errorf("sameVersion(%q,%q) = true, want false", p[0], p[1])
		}
	}
}
