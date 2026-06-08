package dohresolvers

// BundledSeed returns the in-binary fallback snapshot used when no
// snapshot has ever been written and the configured `seed_path` file is
// missing or unreadable. Carrying this in-binary guarantees that
// TS-FirewallRuleGenerator's "Closing the DoH gap" surface has a usable
// list from the very first boot — air-gapped labs included.
//
// IPv4/IPv6 addresses are taken from each provider's public IP
// documentation. The list is intentionally short (the curated upstream
// covers far more); it is a *seed*, not a substitute for refresh.
//
// Provider names match the well-known list asserted by
// FS-DohResolverDbListSnapshotShape:
//   Cloudflare, Google, Quad9, NextDNS, AdGuard, Mullvad, Apple.
func BundledSeed() []ResolverEntry {
	return []ResolverEntry{
		{
			ID:        "cloudflare",
			Name:      "Cloudflare",
			IPv4:      []string{"1.1.1.1", "1.0.0.1"},
			IPv6:      []string{"2606:4700:4700::1111", "2606:4700:4700::1001"},
			SourceURL: "https://developers.cloudflare.com/1.1.1.1/ip-addresses/",
		},
		{
			ID:        "google",
			Name:      "Google",
			IPv4:      []string{"8.8.8.8", "8.8.4.4"},
			IPv6:      []string{"2001:4860:4860::8888", "2001:4860:4860::8844"},
			SourceURL: "https://developers.google.com/speed/public-dns/docs/using",
		},
		{
			ID:        "quad9",
			Name:      "Quad9",
			IPv4:      []string{"9.9.9.9", "149.112.112.112"},
			IPv6:      []string{"2620:fe::fe", "2620:fe::9"},
			SourceURL: "https://www.quad9.net/service/service-addresses-and-features/",
		},
		{
			ID:        "nextdns",
			Name:      "NextDNS",
			IPv4:      []string{"45.90.28.0", "45.90.30.0"},
			IPv6:      []string{"2a07:a8c0::", "2a07:a8c1::"},
			SourceURL: "https://nextdns.io/",
		},
		{
			ID:        "adguard",
			Name:      "AdGuard",
			IPv4:      []string{"94.140.14.14", "94.140.15.15"},
			IPv6:      []string{"2a10:50c0::ad1:ff", "2a10:50c0::ad2:ff"},
			SourceURL: "https://adguard-dns.io/en/public-dns.html",
		},
		{
			ID:        "mullvad",
			Name:      "Mullvad",
			IPv4:      []string{"194.242.2.2", "194.242.2.3"},
			IPv6:      []string{"2a07:e340::2", "2a07:e340::3"},
			SourceURL: "https://mullvad.net/en/help/dns-over-https-and-dns-over-tls",
		},
		{
			ID:        "apple",
			Name:      "Apple",
			IPv4:      []string{"17.253.144.10"},
			IPv6:      []string{"2620:149:a00::10"},
			SourceURL: "https://support.apple.com/en-us/HT202944",
		},
	}
}
