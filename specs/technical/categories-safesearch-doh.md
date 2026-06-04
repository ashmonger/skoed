---
x-tsid: TS-CategoriesSafeSearchDoh
x-fsid-links:
  - FS-CategoryCatalogListed
  - FS-CategoryEnableAddsBlocklist
  - FS-CategoryDisableRemovesAssociation
  - FS-CategoryRefreshRespectsManaged
  - FS-CategoryOverrideUrl
  - FS-CategoryDohEnabledByDefault
  - FS-SafeSearchGoogle
  - FS-SafeSearchBing
  - FS-SafeSearchYoutube
  - FS-SafeSearchDuckDuckGo
  - FS-SafeSearchOptInPerProfile
  - FS-SafeSearchAaaa
  - FS-DohDetectionResolverBlocklist
  - FS-DohDetectionFirefoxCanary
  - FS-DohDetectionDdrProbe
  - FS-DohDetectionTaggedInQueryLog
  - FS-DohDetectionPerClientUiSurfacing
  - FS-DohDetectionCategoryDisableable
---

# TS-CategoriesSafeSearchDoh — Category catalog, SafeSearch, DoH/DoT detection

## Category catalog

A static catalog compiled into the binary at
`internal/filter/categories/catalog.go`:

```go
type Category struct {
    Name        string
    Description string
    DefaultURL  string
    Format      string // hosts | domainlist | adblock
}

var Catalog = map[string]Category{
    "adult":     {…, DefaultURL: "https://small.oisd.nl/", Format: "domainlist"},
    "gambling":  {…, DefaultURL: "https://big.oisd.nl/", Format: "domainlist"},
    "social":    {…, DefaultURL: "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/social/hosts", Format: "hosts"},
    "gaming":    {…},
    "streaming": {…},
    "doh":       {…, DefaultURL: "", Format: "domainlist", // see below
                  Bundled: bundledDohResolvers},
}
```

For `doh` we ship a bundled, hand-curated list (~50 hostnames covering
Cloudflare, Google, Quad9, AdGuard, NextDNS, Mullvad, ControlD, OpenDNS,
plus Chrome/Firefox/Apple bootstrap variants). The bundle is in
`internal/filter/categories/doh-resolvers.go` so we never depend on an
upstream URL for the M3-critical DoH-detection feature.

The exact source list URLs for `adult`, `gambling`, etc. are operator-
overridable via `PATCH /api/v1/categories/{name}` `{url}` — see
FS-CategoryOverrideUrl.

## API

```
GET    /api/v1/categories
GET    /api/v1/categories/{name}
PATCH  /api/v1/categories/{name}       { url?, format? }   // operator overrides
POST   /api/v1/categories/{name}/enable   { profile_id }
POST   /api/v1/categories/{name}/disable  { profile_id }
```

`enable` creates (or updates) a `cat:<name>` blocklist with `managed: true`
and source.url from the catalog or operator override, then ensures the
specified profile's `Blocklists[]` contains `cat:<name>`. All goes through
Raft via the same `blocklist.upsert` + `profile.upsert` commands.

`disable` removes `cat:<name>` from the profile's blocklists; if no
profile references the category any longer, the next prune tick deletes
the underlying blocklist too.

## SafeSearch

SafeSearch is per-profile (`Profile.SafeSearch []string`). The DNS handler
evaluates BEFORE the filter engine:

```
if profile.SafeSearchEnabled("google") and q.Name endsWith "google.com":
    answer CNAME forcesafesearch.google.com  + the upstream A/AAAA for that
```

The CNAME rewrites are hard-coded mappings (no operator override at M3):

| Provider | Domains | CNAME target |
|---|---|---|
| google | `www.google.com`, `www.google.<tld>` | `forcesafesearch.google.com` |
| bing | `www.bing.com`, `bing.com` | `strict.bing.com` |
| youtube | `www.youtube.com`, `m.youtube.com`, `youtubei.googleapis.com`, `youtube.googleapis.com` | `restrict.youtube.com` |
| duckduckgo | `duckduckgo.com`, `www.duckduckgo.com` | `safe.duckduckgo.com` |

The rewrite applies to both A and AAAA queries (FS-SafeSearchAaaa).
Implementation in `internal/filter/safesearch.go` exposes a single
`MaybeRewrite(domain string, qtype uint16, profile *Profile) (cname string, ok bool)`
that the DNS handler consults before doing the regular filter walk.

## DoH/DoT detection layer

Three handler interceptions, processed in order BEFORE profile/blocklist
evaluation:

1. **Firefox canary** (`use-application-dns.net`): always NXDOMAIN,
   logged with `category: doh-canary`. Never overridable — this is the
   Mozilla-supported way for operators to opt out of network-wide DoH.

2. **DDR probe** (`_dns.resolver.arpa` with QTYPE SVCB or HTTPS):
   respond NODATA, log `category: ddr-probe`. We deliberately don't
   advertise ourselves via DDR — operators who want clients to discover
   us point them explicitly (M4: DoH server with documented URL).

3. **`cat:doh` blocklist match**: this is just the normal filter pipeline
   applying the bundled DoH-resolvers list. The log entry's `category`
   field is set to `doh-probe` when the matched blocklist id is `cat:doh`.

`category` is a new field on `log.Entry`:

```go
type Entry struct {
    …existing fields…
    Category string // "", "doh-probe", "doh-canary", "ddr-probe"
}
```

It's also stored in the per-node raw query log, returned by the
`/api/v1/query-log` endpoint, and rolled up in `HourAggregate.Categories
map[string]int` so `/api/v1/cluster/stats` can answer "how many
doh-probes today?".

## Web UI surfacing

The Stats view gains a "DoH attempts today" panel:

- Per-client table: client IP / probe count / last seen / suggested
  action (the Stats view links to `/query-log?client=<ip>&category=doh-probe`).
- A small one-line note: "These clients tried to use a public DoH/DoT
  resolver. dblock blocked the hostname lookup; harden by also blocking
  the resolver IPs at your firewall (see M3.5)."

## Cluster-wide replication

Everything follows the existing M2 patterns:

- Profile/Schedule/Binding writes go through Raft.
- The category catalog is read-only-at-runtime (compiled in); operator
  overrides are stored on `Category.Override` fields in a new
  `config_category_overrides` bucket.
- DoH detection is purely DNS-handler logic; no replication overhead.

## Acceptance test contract

- `tests/acceptance/categories_test.go` — FS-CategoryCatalogListed,
  FS-CategoryEnableAddsBlocklist, FS-CategoryDisableRemovesAssociation,
  FS-CategoryOverrideUrl, FS-CategoryDohEnabledByDefault.
- `tests/acceptance/safesearch_test.go` — FS-SafeSearchGoogle, Bing,
  Youtube, DuckDuckGo, OptInPerProfile, Aaaa.
- `tests/acceptance/doh_test.go` — FS-DohDetectionResolverBlocklist,
  FirefoxCanary, DdrProbe, TaggedInQueryLog, CategoryDisableable.

## Non-goals (explicit)

- L7 SNI inspection (M3.5 firewall recipes).
- DoH/DoT server endpoints (M4).
- Bing strict-mode header injection (HTTPS makes this impossible from a DNS layer).
