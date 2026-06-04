package categories

// dohResolvers is the bundled list of known public DoH / DoT resolver
// hostnames that dblock blocks by default via the "doh" category.
//
// Sources: hand-curated from operator-facing public documentation of each
// provider as of M3 (2026). Hardcoded-IP DoH clients (e.g. Chrome pinned
// to 1.1.1.1) still bypass this list — see M3.5 firewall recipes for the
// network-layer counterpart.
//
// Maintenance: add a new line per resolver; format-checker in
// internal/filter/parsers/domainlist.go will validate at parse time.
var dohResolvers = []string{
	// Cloudflare
	"cloudflare-dns.com",
	"one.one.one.one",
	"1.1.1.1",
	"chrome.cloudflare-dns.com",
	"mozilla.cloudflare-dns.com",
	"family.cloudflare-dns.com",

	// Google
	"dns.google",
	"dns.google.com",

	// Quad9
	"dns.quad9.net",
	"dns9.quad9.net",
	"dns10.quad9.net",
	"dns11.quad9.net",
	"dns12.quad9.net",

	// AdGuard
	"dns.adguard.com",
	"dns-family.adguard.com",
	"dns-unfiltered.adguard.com",
	"dns.adguard-dns.com",

	// NextDNS
	"dns.nextdns.io",

	// Mullvad
	"adblock.dns.mullvad.net",
	"base.dns.mullvad.net",
	"extended.dns.mullvad.net",
	"family.dns.mullvad.net",
	"all.dns.mullvad.net",
	"doh.mullvad.net",

	// ControlD
	"dns.controld.com",
	"freedns.controld.com",

	// OpenDNS / Cisco
	"doh.opendns.com",
	"doh.familyshield.opendns.com",
	"doh.umbrella.com",

	// CleanBrowsing
	"doh.cleanbrowsing.org",
	"family-filter-dns.cleanbrowsing.org",

	// Brave
	"dns.brave.com",

	// Comcast Xfinity (yes, they run a DoH)
	"doh.xfinity.com",

	// LibreDNS / dns.sb
	"doh.libredns.gr",
	"doh.dns.sb",

	// DNS.WATCH / Tiarap / NJALLA
	"doh.tiarap.org",
	"dns.njal.la",

	// Yandex
	"common.dot.dns.yandex.net",
	"family.dot.dns.yandex.net",
	"safe.dot.dns.yandex.net",
}

// FirefoxCanary is the hostname Mozilla's clients probe before enabling
// DoH-by-default. Returning NXDOMAIN here makes Firefox auto-disable DoH —
// the spec-blessed network-operator opt-out. This is HARDCODED, never
// overridable by allowlist or profile — see FS-DohDetectionFirefoxCanary.
const FirefoxCanary = "use-application-dns.net"

// DDRProbeDomain is the RFC 9462 Discovery of Designated Resolvers root.
// SVCB/HTTPS queries here are how clients auto-discover a network-blessed
// DoH/DoT endpoint. dblock answers NODATA so clients don't get a public
// resolver pointer they might trust over their configured DNS.
const DDRProbeDomain = "_dns.resolver.arpa"
