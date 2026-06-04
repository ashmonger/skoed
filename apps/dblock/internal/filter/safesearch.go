package filter

import "strings"

// SafeSearchProvider is a normalised provider name. Profiles enable
// SafeSearch per-provider via the canonical strings below.
const (
	SafeSearchGoogle     = "google"
	SafeSearchBing       = "bing"
	SafeSearchYoutube    = "youtube"
	SafeSearchDuckDuckGo = "duckduckgo"
)

// safeSearchMap drives the hostname → CNAME-target rewrite per provider.
// The DNS handler consults this map BEFORE running the filter walk; if a
// match is found AND the active profile has the provider enabled, the
// response is a CNAME to the SafeSearch endpoint plus the upstream A/AAAA
// for that CNAME.
//
// Hostnames are lower-cased and match the FULL query name (no suffix
// stripping). All Google ccTLDs share the same forcesafesearch target.
var safeSearchMap = map[string]struct {
	provider string
	target   string
}{
	// Google — covers ccTLDs at lookup time via a separate suffix check below.
	"www.google.com": {SafeSearchGoogle, "forcesafesearch.google.com"},
	"google.com":     {SafeSearchGoogle, "forcesafesearch.google.com"},

	"www.bing.com": {SafeSearchBing, "strict.bing.com"},
	"bing.com":     {SafeSearchBing, "strict.bing.com"},

	"www.youtube.com":            {SafeSearchYoutube, "restrict.youtube.com"},
	"m.youtube.com":              {SafeSearchYoutube, "restrict.youtube.com"},
	"youtubei.googleapis.com":    {SafeSearchYoutube, "restrict.youtube.com"},
	"youtube.googleapis.com":     {SafeSearchYoutube, "restrict.youtube.com"},
	"youtube.com":                {SafeSearchYoutube, "restrict.youtube.com"},

	"duckduckgo.com":     {SafeSearchDuckDuckGo, "safe.duckduckgo.com"},
	"www.duckduckgo.com": {SafeSearchDuckDuckGo, "safe.duckduckgo.com"},
}

// SafeSearchRewrite reports whether the given domain should be rewritten
// for the supplied enabled-provider set. Returns the CNAME target and
// true on match; "", false otherwise.
//
// `enabled` is typically the profile's SafeSearch list. A nil/empty slice
// disables SafeSearch entirely.
func SafeSearchRewrite(domain string, enabled []string) (cname string, ok bool) {
	if len(enabled) == 0 {
		return "", false
	}
	d := strings.ToLower(strings.TrimSuffix(domain, "."))

	// Exact-host map.
	if hit, found := safeSearchMap[d]; found {
		if providerEnabled(hit.provider, enabled) {
			return hit.target, true
		}
		return "", false
	}

	// Google ccTLDs: www.google.<tld>, google.<tld>. Use a coarse suffix
	// rule so we cover .fr, .co.uk, .de, etc. without enumerating them all.
	if (strings.HasPrefix(d, "www.google.") || strings.HasPrefix(d, "google.")) {
		if providerEnabled(SafeSearchGoogle, enabled) {
			return "forcesafesearch.google.com", true
		}
	}
	return "", false
}

func providerEnabled(p string, enabled []string) bool {
	for _, e := range enabled {
		if strings.EqualFold(e, p) {
			return true
		}
	}
	return false
}
