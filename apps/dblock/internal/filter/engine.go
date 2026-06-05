package filter

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/dblock/dblock/internal/config"
	"github.com/dblock/dblock/internal/filter/parsers"
)

type BlockPolicy int

const (
	PolicyInherit  BlockPolicy = iota
	PolicyNXDOMAIN
	PolicyNULL
	PolicyNODATA
)

type Disposition int

const (
	Allow Disposition = iota
	Block
)

type Result struct {
	Disposition Disposition
	Policy      BlockPolicy
	BlocklistID string
}

// domainSet holds a set of apex domains. An entry "example.com" matches
// "example.com" itself and every subdomain at any depth. The "*.example.com"
// prefix syntax is normalised to "example.com" on insert — both forms are
// equivalent.
type domainSet struct {
	apices map[string]struct{}
}

func newDomainSet(entries []string) domainSet {
	ds := domainSet{apices: make(map[string]struct{}, len(entries))}
	for _, e := range entries {
		e = strings.ToLower(strings.TrimPrefix(e, "*."))
		if e != "" {
			ds.apices[e] = struct{}{}
		}
	}
	return ds
}

// matches returns true if domain equals or is a subdomain of any registered apex.
// It strips one DNS label at a time, so the check is O(label-depth) map lookups.
func (ds *domainSet) matches(domain string) bool {
	for d := domain; ; {
		if _, ok := ds.apices[d]; ok {
			return true
		}
		dot := strings.Index(d, ".")
		if dot < 0 {
			return false
		}
		d = d[dot+1:]
	}
}

type blocklistEntry struct {
	id      string
	policy  BlockPolicy
	set     domainSet
	managed bool
}

type Engine struct {
	mu           sync.RWMutex
	globalPolicy BlockPolicy
	allowlist    domainSet
	blocklists   []blocklistEntry

	// M3 additions — present whenever the engine is built with NewProfiled.
	profiles  []profileEntry
	schedules []config.Schedule
	bindings  []config.ScheduleBinding

	// blocklistByID lets profile evaluation locate a blocklist's set
	// without an O(N) linear walk per query.
	blocklistByID map[string]*blocklistEntry
}

// profileEntry is the engine-internal, pre-parsed form of a config.Profile.
type profileEntry struct {
	id          string
	blocklists  []string // ids
	allowlist   domainSet
	safesearch  []string
	clientIPs   []net.IP
	clientCIDRs []*net.IPNet
	// M3.6 match keys. Lowercased so callers don't have to.
	clientIDs       []string
	clientMACs      []string
	clientHostnames []string
}

func parsePolicy(s string) BlockPolicy {
	switch s {
	case "nxdomain":
		return PolicyNXDOMAIN
	case "null":
		return PolicyNULL
	case "nodata":
		return PolicyNODATA
	default:
		return PolicyInherit
	}
}

func parseDomains(raw []string, format string) []string {
	if len(raw) == 0 {
		return nil
	}

	detectedFormat := format
	if detectedFormat == "auto" || detectedFormat == "" {
		detectedFormat = detectFormat(raw)
	}

	joined := strings.Join(raw, "\n")
	r := strings.NewReader(joined)

	var domains []string
	var err error
	switch detectedFormat {
	case "hosts":
		domains, err = parsers.ParseHosts(r)
	case "adblock":
		domains, err = parsers.ParseAdblock(r)
	default:
		domains, err = parsers.ParseDomainList(r)
	}
	if err != nil {
		return nil
	}
	return domains
}

func detectFormat(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			continue
		}
		if strings.HasPrefix(trimmed, "||") {
			return "adblock"
		}
		if strings.Contains(trimmed, " ") || strings.Contains(trimmed, "\t") {
			return "hosts"
		}
		return "domainlist"
	}
	return "domainlist"
}

func New(cfg config.FilteringConfig) *Engine {
	e := &Engine{
		globalPolicy:  parsePolicy(cfg.BlockPolicy),
		allowlist:     newDomainSet(cfg.Allowlist),
		blocklistByID: map[string]*blocklistEntry{},
	}

	for _, bl := range cfg.Blocklists {
		if !bl.Enabled {
			continue
		}
		domains := parseDomains(bl.Domains, bl.Source.Format)
		entry := blocklistEntry{
			id:      bl.ID,
			policy:  parsePolicy(bl.BlockPolicy),
			set:     newDomainSet(domains),
			managed: bl.Managed,
		}
		e.blocklists = append(e.blocklists, entry)
		e.blocklistByID[entry.id] = &e.blocklists[len(e.blocklists)-1]
	}

	return e
}

// NewProfiled is the M3 entry point. It returns an engine that also knows
// about per-client profiles, schedule bindings, and the allowlist embedded
// in each profile. Calls to EvaluateForClient walk per-profile blocklists;
// the legacy Evaluate is preserved for the no-client-info code paths.
func NewProfiled(cfg *config.Config) *Engine {
	e := New(cfg.Filtering)
	e.profiles = make([]profileEntry, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		pe := profileEntry{
			id:         p.ID,
			blocklists: append([]string(nil), p.Blocklists...),
			allowlist:  newDomainSet(p.Allowlist),
			safesearch: append([]string(nil), p.SafeSearch...),
		}
		for _, s := range p.ClientIPs {
			if ip := net.ParseIP(s); ip != nil {
				pe.clientIPs = append(pe.clientIPs, ip)
			}
		}
		for _, s := range p.ClientCIDRs {
			if _, ipnet, err := net.ParseCIDR(s); err == nil {
				pe.clientCIDRs = append(pe.clientCIDRs, ipnet)
			}
		}
		for _, s := range p.ClientIDs {
			pe.clientIDs = append(pe.clientIDs, s)
		}
		for _, s := range p.ClientMACs {
			pe.clientMACs = append(pe.clientMACs, strings.ToLower(s))
		}
		for _, s := range p.ClientHostnames {
			pe.clientHostnames = append(pe.clientHostnames, s)
		}
		e.profiles = append(e.profiles, pe)
	}
	e.schedules = append([]config.Schedule(nil), cfg.Schedules...)
	e.bindings = append([]config.ScheduleBinding(nil), cfg.Bindings...)
	return e
}

// ClientIdentity bundles the optional M3.6 identity fields for a query.
// All fields can be empty; the engine falls back to IP-only matching.
type ClientIdentity struct {
	ClientID string
	MAC      string
	Hostname string
}

// ProfilesMatching returns every profile id matching the given client.
// Match priority: ClientID > MAC > hostname > IP/CIDR. Caller passes
// the optional identity (zero value = IP-only behavior, the M3 default).
//
// At most one profile is returned per query EXCEPT for the legacy
// IP/CIDR pre-M3.6 union behavior, which is preserved. The returned
// list is "default" only when nothing matches.
func (e *Engine) ProfilesMatching(clientIP net.IP, id ClientIdentity) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// M3.6: prefer the highest-priority identity match. Walk profiles
	// in priority tiers; the first non-empty tier wins.
	if id.ClientID != "" {
		for _, p := range e.profiles {
			for _, x := range p.clientIDs {
				if x == id.ClientID {
					return []string{p.id}
				}
			}
		}
	}
	if id.MAC != "" {
		mac := strings.ToLower(id.MAC)
		for _, p := range e.profiles {
			for _, x := range p.clientMACs {
				if x == mac {
					return []string{p.id}
				}
			}
		}
	}
	if id.Hostname != "" {
		for _, p := range e.profiles {
			for _, x := range p.clientHostnames {
				if x == id.Hostname {
					return []string{p.id}
				}
			}
		}
	}

	// Fall back to legacy IP/CIDR matching (M3 behavior).
	var out []string
	for _, p := range e.profiles {
		if matchesProfileIP(p, clientIP) {
			out = append(out, p.id)
		}
	}
	if len(out) == 0 {
		out = []string{"default"}
	}
	return out
}

func matchesProfileIP(p profileEntry, ip net.IP) bool {
	if ip == nil {
		return p.id == "default"
	}
	for _, x := range p.clientIPs {
		if x.Equal(ip) {
			return true
		}
	}
	for _, n := range p.clientCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	// A profile with NO explicit client identification AND id == "default"
	// matches every client implicitly.
	if p.id == "default" && len(p.clientIPs) == 0 && len(p.clientCIDRs) == 0 &&
		len(p.clientIDs) == 0 && len(p.clientMACs) == 0 && len(p.clientHostnames) == 0 {
		return true
	}
	return false
}

// EvaluateForClient is the M3 query-time entry. For each profile that
// matches the client IP, it checks the profile's allowlist (allow wins),
// then walks the profile's blocklists, consulting schedule bindings to
// decide whether each (profile, blocklist) pair is currently active.
//
// `domain` should be the FQDN with any trailing dot stripped, lower-cased.
// `now` is the wall-clock for schedule evaluation; pass filter.Now() in
// production.
func (e *Engine) EvaluateForClient(domain string, clientIP net.IP, now time.Time) Result {
	return e.EvaluateForClientID(domain, clientIP, ClientIdentity{}, now)
}

// EvaluateForClientID is the M3.6 form: same as EvaluateForClient but
// also honours the optional Client-ID / MAC / hostname for profile
// matching priority.
func (e *Engine) EvaluateForClientID(domain string, clientIP net.IP, id ClientIdentity, now time.Time) Result {
	e.mu.RLock()
	defer e.mu.RUnlock()

	domain = strings.ToLower(domain)

	// Global allowlist (legacy / non-profile) always wins first.
	if e.allowlist.matches(domain) {
		return Result{Disposition: Allow}
	}

	matched := e.profilesMatchingLockedWithIdentity(clientIP, id)

	// Profile-level allowlists (any matching profile allows → allow).
	for _, pid := range matched {
		p := e.findProfile(pid)
		if p != nil && p.allowlist.matches(domain) {
			return Result{Disposition: Allow}
		}
	}

	// Walk each matched profile's blocklists. First applicable block wins.
	for _, pid := range matched {
		p := e.findProfile(pid)
		// "default" implicit profile (no Profile object): fall back to the
		// engine's global blocklists.
		if p == nil {
			if pid == "default" {
				if r := e.walkGlobalBlocklists(domain); r.Disposition == Block {
					return r
				}
			}
			continue
		}
		for _, blID := range p.blocklists {
			bl := e.blocklistByID[blID]
			if bl == nil || !bl.set.matches(domain) {
				continue
			}
			// Schedule evaluation — if a binding tells us to suppress,
			// skip this blocklist for this profile.
			res := EvaluateSchedules(e.bindings, e.schedules, pid, blID, now)
			if res == ScheduleSuppresses {
				continue
			}
			return Result{Disposition: Block, Policy: bl.policy, BlocklistID: bl.id}
		}
	}

	// Backward-compat: blocklists NOT referenced by any profile apply
	// cluster-wide (M1/M2 semantics — operators who never authored
	// profiles still expect every blocklist to block every client).
	if r := e.walkOrphanBlocklists(domain); r.Disposition == Block {
		return r
	}

	return Result{Disposition: Allow}
}

// walkOrphanBlocklists evaluates blocklists that are NOT referenced by any
// profile. Without this, an operator who creates a blocklist via the M2
// API but hasn't authored profiles would see their blocklist silently
// orphaned (and never block anything). We treat unreferenced blocklists
// as applying to every client — EXCEPT managed-category blocklists
// (cat:*), which exist to be explicitly enabled per profile and should
// disappear cleanly when disabled.
func (e *Engine) walkOrphanBlocklists(domain string) Result {
	for _, bl := range e.blocklists {
		if bl.managed {
			continue
		}
		if e.isReferenced(bl.id) {
			continue
		}
		if bl.set.matches(domain) {
			return Result{Disposition: Block, Policy: bl.policy, BlocklistID: bl.id}
		}
	}
	return Result{Disposition: Allow}
}

func (e *Engine) isReferenced(blID string) bool {
	for _, p := range e.profiles {
		for _, b := range p.blocklists {
			if b == blID {
				return true
			}
		}
	}
	return false
}

func (e *Engine) profilesMatchingLocked(ip net.IP) []string {
	return e.profilesMatchingLockedWithIdentity(ip, ClientIdentity{})
}

func (e *Engine) profilesMatchingLockedWithIdentity(ip net.IP, id ClientIdentity) []string {
	// M3.6: priority lookup (Client-ID > MAC > hostname > IP/CIDR).
	if id.ClientID != "" {
		for _, p := range e.profiles {
			for _, x := range p.clientIDs {
				if x == id.ClientID {
					return []string{p.id}
				}
			}
		}
	}
	if id.MAC != "" {
		mac := strings.ToLower(id.MAC)
		for _, p := range e.profiles {
			for _, x := range p.clientMACs {
				if x == mac {
					return []string{p.id}
				}
			}
		}
	}
	if id.Hostname != "" {
		for _, p := range e.profiles {
			for _, x := range p.clientHostnames {
				if x == id.Hostname {
					return []string{p.id}
				}
			}
		}
	}
	var out []string
	for _, p := range e.profiles {
		if matchesProfileIP(p, ip) {
			out = append(out, p.id)
		}
	}
	if len(out) == 0 {
		out = []string{"default"}
	}
	return out
}

func (e *Engine) findProfile(id string) *profileEntry {
	for i := range e.profiles {
		if e.profiles[i].id == id {
			return &e.profiles[i]
		}
	}
	return nil
}

func (e *Engine) walkGlobalBlocklists(domain string) Result {
	for _, bl := range e.blocklists {
		if bl.set.matches(domain) {
			return Result{Disposition: Block, Policy: bl.policy, BlocklistID: bl.id}
		}
	}
	return Result{Disposition: Allow}
}

// SafeSearchProvidersForClient returns the union of SafeSearch providers
// enabled by every profile matching the given client IP. Used by the DNS
// handler to decide whether to inject a SafeSearch CNAME rewrite.
func (e *Engine) SafeSearchProvidersForClient(ip net.IP) []string {
	return e.SafeSearchProvidersForClientID(ip, ClientIdentity{})
}

// SafeSearchProvidersForClientID is the M3.6 form honouring identity.
func (e *Engine) SafeSearchProvidersForClientID(ip net.IP, id ClientIdentity) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	seen := map[string]struct{}{}
	for _, pid := range e.profilesMatchingLockedWithIdentity(ip, id) {
		p := e.findProfile(pid)
		if p == nil {
			continue
		}
		for _, prov := range p.safesearch {
			seen[strings.ToLower(prov)] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func (e *Engine) Evaluate(domain string) Result {
	e.mu.RLock()
	defer e.mu.RUnlock()

	domain = strings.ToLower(domain)

	if e.allowlist.matches(domain) {
		return Result{Disposition: Allow}
	}

	for _, bl := range e.blocklists {
		if bl.set.matches(domain) {
			return Result{
				Disposition: Block,
				Policy:      bl.policy,
				BlocklistID: bl.id,
			}
		}
	}

	return Result{Disposition: Allow}
}

func (e *Engine) EffectivePolicy(r Result) BlockPolicy {
	if r.Policy != PolicyInherit {
		return r.Policy
	}
	return e.globalPolicy
}
