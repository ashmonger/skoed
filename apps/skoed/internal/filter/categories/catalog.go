// Package categories provides the static, compiled-in catalog of curated
// blocklist categories that operators can subscribe profiles to.
//
// Each category has a default upstream URL (operator-overridable via
// PATCH /api/v1/categories/{name}) and a format hint. The special "doh"
// category bundles its domain list directly into the binary so DoH
// detection works on first boot without any network fetch.
package categories

// Category describes one user-facing blocklist category.
type Category struct {
	Name        string   // canonical id used in API paths
	Description string   // human-friendly explanation
	DefaultURL  string   // operator-overridable upstream
	Format      string   // hosts | domainlist | askoed
	Bundled     []string // if non-empty, takes priority over DefaultURL (used by "doh")
}

// Catalog is the canonical category set. Keys match the API path component.
var Catalog = map[string]Category{
	"adult": {
		Name:        "adult",
		Description: "Adult content (OISD's curated set).",
		DefaultURL:  "https://small.oisd.nl/domainswild",
		Format:      "domainlist",
	},
	"gambling": {
		Name:        "gambling",
		Description: "Gambling sites (Steven Black gambling extension).",
		DefaultURL:  "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/gambling-only/hosts",
		Format:      "hosts",
	},
	"social": {
		Name:        "social",
		Description: "Social-media platforms (Steven Black social extension).",
		DefaultURL:  "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/social-only/hosts",
		Format:      "hosts",
	},
	"gaming": {
		Name:        "gaming",
		Description: "Online gaming services.",
		DefaultURL:  "https://raw.githubusercontent.com/blocklistproject/Lists/master/gambling.txt",
		Format:      "hosts",
	},
	"streaming": {
		Name:        "streaming",
		Description: "Video streaming services (Netflix, Disney+, etc.).",
		DefaultURL:  "https://raw.githubusercontent.com/blocklistproject/Lists/master/youtube.txt",
		Format:      "hosts",
	},
	"doh": {
		Name:        "doh",
		Description: "Public DNS-over-HTTPS / DNS-over-TLS resolver hostnames.",
		DefaultURL:  "",
		Format:      "domainlist",
		Bundled:     dohResolvers,
	},
}

// Names returns the catalog keys in stable lexical order.
func Names() []string {
	out := make([]string, 0, len(Catalog))
	for k := range Catalog {
		out = append(out, k)
	}
	// stable order (small slice; sort.Strings would require an import)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// BlocklistID returns the canonical id used for a category's managed
// blocklist, e.g. "doh" → "cat:doh".
func BlocklistID(category string) string {
	return "cat:" + category
}
