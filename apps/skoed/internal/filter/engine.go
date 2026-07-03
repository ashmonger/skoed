package filter

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter/parsers"
)

type BlockPolicy int

const (
	PolicyInherit  BlockPolicy = iota
	PolicyNXDOMAIN
	PolicyNULL
	PolicyNODATA
	PolicyRedirect
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
	PauseActive bool
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

// scheduledAllowEntry is a per-schedule bucket of allowed domains. The allow
// only applies when scheduleActive(schedule, now) is true (M36).
type scheduledAllowEntry struct {
	set        domainSet
	scheduleID string
}

// splitAllowlistEntries divides AllowlistEntries into static (domainSet) and
// schedule-gated ([]scheduledAllowEntry) parts. Entries whose ExpiresAt has
// already passed are dropped entirely.
func splitAllowlistEntries(entries []config.AllowlistEntry, now time.Time) (domainSet, []scheduledAllowEntry) {
	static := domainSet{apices: make(map[string]struct{})}
	bySchedule := map[string]*scheduledAllowEntry{}
	for _, e := range entries {
		if e.ExpiresAt != nil && !now.Before(*e.ExpiresAt) {
			continue // expired
		}
		d := strings.ToLower(strings.TrimPrefix(e.Domain, "*."))
		if d == "" {
			continue
		}
		if e.ScheduleID == "" {
			static.apices[d] = struct{}{}
		} else {
			if _, ok := bySchedule[e.ScheduleID]; !ok {
				bySchedule[e.ScheduleID] = &scheduledAllowEntry{
					scheduleID: e.ScheduleID,
					set:        domainSet{apices: make(map[string]struct{})},
				}
			}
			bySchedule[e.ScheduleID].set.apices[d] = struct{}{}
		}
	}
	sched := make([]scheduledAllowEntry, 0, len(bySchedule))
	for _, se := range bySchedule {
		sched = append(sched, *se)
	}
	return static, sched
}

// checkScheduledAllowlist returns true if any scheduled entry's schedule is
// currently active and the domain matches.
func checkScheduledAllowlist(entries []scheduledAllowEntry, schedules []config.Schedule, domain string, now time.Time) bool {
	for i := range entries {
		if !entries[i].set.matches(domain) {
			continue
		}
		s := findSchedule(schedules, entries[i].scheduleID)
		if s != nil && scheduleActive(s, now) {
			return true
		}
	}
	return false
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

// sharedAllowEntry is the engine-internal form of a config.SharedAllowlist (M36).
type sharedAllowEntry struct {
	static     domainSet
	scheduled  []scheduledAllowEntry
	profileIDs map[string]struct{}
}

type Engine struct {
	mu           sync.RWMutex
	globalPolicy BlockPolicy
	allowlist    domainSet
	// M36: schedule-gated global allowlist entries.
	scheduledAllowlist []scheduledAllowEntry
	blocklists   []blocklistEntry
	// M30.5 — cluster-wide custom rules (allow > block; checked before allowlist).
	customRules  customRuleSet

	// M3 additions — present whenever the engine is built with NewProfiled.
	profiles  []profileEntry
	schedules []config.Schedule
	bindings  []config.ScheduleBinding
	// M36: shared allowlists (cross-profile).
	sharedAllowlists []sharedAllowEntry

	// blocklistByID lets profile evaluation locate a blocklist's set
	// without an O(N) linear walk per query.
	blocklistByID map[string]*blocklistEntry

	// M6.5 (TS-BlockDyn): optional lease-origin lookup. When non-nil,
	// tier-4 profile matching also checks block_dynamic_clients profiles.
	// Returns the DHCP Origin of the IP ("dhcp_dynamic", "dhcp_static",
	// ..., or "" when no lease exists).
	leaseOriginFn func(ip string) string

	// M35.5 (TS-DeviceRegistry): optional device-registry lookup. When non-nil,
	// it is evaluated first (tier 0), before all profile selectors. If a device
	// matches any of the supplied identifiers, its ProfileID is returned immediately.
	// Returns profileID and matched=true, or ("", false) when no device matches.
	deviceLookupFn func(mac, ip, hostname, clientID string) (profileID string, matched bool)

	// M13: pause deadlines, replicated via Raft through config.PauseState.
	globalPauseUntil      time.Time
	globalPauseProfileIDs map[string]bool // nil/empty = all profiles; non-empty = only those IDs
	profilePauseUntil     map[string]time.Time
	// M35: per-profile, per-client pause. When non-nil, only listed IPs are paused.
	profilePauseClientIPs map[string][]net.IP
}

// profileEntry is the engine-internal, pre-parsed form of a config.Profile.
type profileEntry struct {
	id          string
	blocklists  []string // ids
	allowlist   domainSet
	// M36: schedule-gated per-profile allowlist entries.
	scheduledAllowlist []scheduledAllowEntry
	safesearch  []string
	clientIPs   []net.IP
	clientCIDRs []*net.IPNet
	// M3.6 match keys. Lowercased so callers don't have to.
	clientIDs       []string
	clientMACs      []string
	clientHostnames []string
	// M6.5 (TS-BlockDyn): when true this profile matches any client
	// whose DHCP lease Origin is exactly "dhcp_dynamic".
	blockDynamicClients bool
	// M38: per-profile DNSSEC mode. "" or "inherit" → use global default.
	dnssecMode string
}

func parsePolicy(s string) BlockPolicy {
	switch s {
	case "nxdomain":
		return PolicyNXDOMAIN
	case "null":
		return PolicyNULL
	case "nodata":
		return PolicyNODATA
	case "redirect":
		return PolicyRedirect
	default:
		return PolicyInherit
	}
}

func parseDomains(raw []string, format string) []string {
	if len(raw) == 0 {
		return nil
	}

	// bl.Domains stores pre-parsed plain domain names produced by Download().
	// Re-applying the original source format (e.g. "hosts") would fail because
	// the stored entries no longer have the IP-address prefix. Always detect the
	// actual content format so plain domains pass through as "domainlist",
	// regardless of what the source format hint says.
	detectedFormat := detectFormat(raw)

	joined := strings.Join(raw, "\n")
	r := strings.NewReader(joined)

	var domains []string
	var err error
	switch detectedFormat {
	case "hosts":
		domains, err = parsers.ParseHosts(r)
	case "askoed":
		domains, err = parsers.ParseAskoed(r)
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
			return "askoed"
		}
		if strings.Contains(trimmed, " ") || strings.Contains(trimmed, "\t") {
			return "hosts"
		}
		return "domainlist"
	}
	return "domainlist"
}

func New(cfg config.FilteringConfig) *Engine {
	staticSet, scheduled := splitAllowlistEntries(cfg.AllowlistEntries, Now())
	// Merge legacy plain-string allowlist into the static set.
	for _, d := range cfg.Allowlist {
		d = strings.ToLower(strings.TrimPrefix(d, "*."))
		if d != "" {
			staticSet.apices[d] = struct{}{}
		}
	}
	e := &Engine{
		globalPolicy:       parsePolicy(cfg.BlockPolicy),
		allowlist:          staticSet,
		scheduledAllowlist: scheduled,
		blocklistByID:      map[string]*blocklistEntry{},
	}
	if cfg.CustomRules != "" {
		if rs, err := ParseCustomRules(cfg.CustomRules); err == nil {
			e.customRules = rs
		}
		// Parsing errors are silently ignored here: the PUT handler already
		// validated the text before committing it via Raft.
	}

	if cfg.GlobalPause != nil {
		e.globalPauseUntil = cfg.GlobalPause.ResumesAt
		if len(cfg.GlobalPause.ProfileIDs) > 0 {
			e.globalPauseProfileIDs = make(map[string]bool, len(cfg.GlobalPause.ProfileIDs))
			for _, id := range cfg.GlobalPause.ProfileIDs {
				e.globalPauseProfileIDs[id] = true
			}
		}
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
	now := Now()
	for _, p := range cfg.Profiles {
		staticSet, sched := splitAllowlistEntries(p.AllowlistEntries, now)
		// Merge legacy plain-string per-profile allowlist.
		for _, d := range p.Allowlist {
			d = strings.ToLower(strings.TrimPrefix(d, "*."))
			if d != "" {
				staticSet.apices[d] = struct{}{}
			}
		}
		pe := profileEntry{
			id:                 p.ID,
			blocklists:         append([]string(nil), p.Blocklists...),
			allowlist:          staticSet,
			scheduledAllowlist: sched,
			safesearch:         append([]string(nil), p.SafeSearch...),
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
		pe.blockDynamicClients = p.BlockDynamicClients
		pe.dnssecMode = p.DnssecMode
		e.profiles = append(e.profiles, pe)
	}
	e.schedules = append([]config.Schedule(nil), cfg.Schedules...)
	e.bindings = append([]config.ScheduleBinding(nil), cfg.Bindings...)

	// M36: build shared allowlist index.
	for _, sal := range cfg.Filtering.SharedAllowlists {
		staticSet, sched := splitAllowlistEntries(sal.Entries, now)
		pids := make(map[string]struct{}, len(sal.Profiles))
		for _, pid := range sal.Profiles {
			pids[pid] = struct{}{}
		}
		e.sharedAllowlists = append(e.sharedAllowlists, sharedAllowEntry{
			static:     staticSet,
			scheduled:  sched,
			profileIDs: pids,
		})
	}

	e.profilePauseUntil = make(map[string]time.Time, len(cfg.Profiles))
	e.profilePauseClientIPs = make(map[string][]net.IP)
	for _, p := range cfg.Profiles {
		if p.Pause != nil {
			e.profilePauseUntil[p.ID] = p.Pause.ResumesAt
			if len(p.Pause.ClientIPs) > 0 {
				parsed := make([]net.IP, 0, len(p.Pause.ClientIPs))
				for _, s := range p.Pause.ClientIPs {
					if ip := net.ParseIP(s); ip != nil {
						parsed = append(parsed, ip)
					}
				}
				e.profilePauseClientIPs[p.ID] = parsed
			}
		}
	}

	return e
}

// SetLeaseOriginLookup wires the M6.5 block-dynamic-clients lease lookup.
// fn receives the client IP string and returns the DHCP Origin ("dhcp_dynamic",
// "dhcp_static", "", ...). Safe to call concurrently or after construction.
func (e *Engine) SetLeaseOriginLookup(fn func(ip string) string) {
	e.mu.Lock()
	e.leaseOriginFn = fn
	e.mu.Unlock()
}

// SetDeviceLookup wires the M35.5 named device registry lookup (tier 0).
// fn receives the known identifiers for the current client and returns the
// profile ID to use if a device matches, plus a boolean indicating a match.
// Short-circuits all profile selectors when matched. Safe to call concurrently.
func (e *Engine) SetDeviceLookup(fn func(mac, ip, hostname, clientID string) (profileID string, matched bool)) {
	e.mu.Lock()
	e.deviceLookupFn = fn
	e.mu.Unlock()
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
	return e.profilesMatchingLockedWithIdentity(clientIP, id)
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

	// Global pause targeting ALL profiles: short-circuit immediately.
	if !e.globalPauseUntil.IsZero() && now.Before(e.globalPauseUntil) && len(e.globalPauseProfileIDs) == 0 {
		return Result{Disposition: Allow, PauseActive: true}
	}

	// M30.5: cluster-wide custom rules (allow > block; highest priority after pause).
	if matched, isAllow := e.customRules.evaluate(domain); matched {
		if isAllow {
			return Result{Disposition: Allow, BlocklistID: "custom_rule"}
		}
		return Result{Disposition: Block, BlocklistID: "custom_rule"}
	}

	// Global allowlist (static) always wins first.
	if e.allowlist.matches(domain) {
		return Result{Disposition: Allow}
	}
	// Global scheduled allowlist (M36): allow if schedule is currently active.
	if checkScheduledAllowlist(e.scheduledAllowlist, e.schedules, domain, now) {
		return Result{Disposition: Allow}
	}

	matched := e.profilesMatchingLockedWithIdentity(clientIP, id)

	// Profile-level allowlists (any matching profile allows → allow).
	for _, pid := range matched {
		p := e.findProfile(pid)
		if p != nil {
			if p.allowlist.matches(domain) {
				return Result{Disposition: Allow}
			}
			if checkScheduledAllowlist(p.scheduledAllowlist, e.schedules, domain, now) {
				return Result{Disposition: Allow}
			}
		}
	}
	// M36: shared allowlists that apply to any matched profile.
	for i := range e.sharedAllowlists {
		sal := &e.sharedAllowlists[i]
		for _, pid := range matched {
			if _, ok := sal.profileIDs[pid]; ok {
				if sal.static.matches(domain) {
					return Result{Disposition: Allow}
				}
				if checkScheduledAllowlist(sal.scheduled, e.schedules, domain, now) {
					return Result{Disposition: Allow}
				}
				break // checked this sal for matched profiles; move to next sal
			}
		}
	}

	profileWasPaused := false
	// Walk each matched profile's blocklists. First applicable block wins.
	for _, pid := range matched {
		// Check per-profile pause and global pause targeting specific profiles.
		profileIsPaused := false
		if until, ok := e.profilePauseUntil[pid]; ok && !until.IsZero() && now.Before(until) {
			// M35: if per-client IPs are configured, pause ONLY for those clients.
			// The map holds an entry exactly when the pause was scoped to specific
			// IPs, so a present-but-empty list means "no client matched" (fail
			// closed) — never "pause everyone", which would silently disable
			// filtering for the whole profile.
			if ips, hasPerClient := e.profilePauseClientIPs[pid]; hasPerClient {
				for _, pausedIP := range ips {
					if pausedIP.Equal(clientIP) {
						profileIsPaused = true
						break
					}
				}
			} else {
				profileIsPaused = true
			}
		}
		if !profileIsPaused && !e.globalPauseUntil.IsZero() && now.Before(e.globalPauseUntil) && e.globalPauseProfileIDs[pid] {
			profileIsPaused = true
		}
		if profileIsPaused {
			profileWasPaused = true
			continue
		}

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

	return Result{Disposition: Allow, PauseActive: profileWasPaused}
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
	// M35.5 (TS-DeviceRegistry): tier 0 — device registry lookup short-circuits
	// all profile selectors. Evaluated before ClientID/MAC/hostname/IP tiers.
	if e.deviceLookupFn != nil {
		if profileID, matched := e.deviceLookupFn(id.MAC, ip.String(), id.Hostname, id.ClientID); matched {
			return []string{profileID}
		}
	}
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
	// M6.5 (TS-BlockDyn): add any block_dynamic_clients profile that
	// matches this client's DHCP origin. Only the exact string
	// "dhcp_dynamic" triggers the rule.
	if e.leaseOriginFn != nil {
		origin := e.leaseOriginFn(ip.String())
		if origin == "dhcp_dynamic" {
			for _, p := range e.profiles {
				if !p.blockDynamicClients {
					continue
				}
				already := false
				for _, existing := range out {
					if existing == p.id {
						already = true
						break
					}
				}
				if !already {
					out = append(out, p.id)
				}
			}
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

	// M30.5: custom rules have highest priority (allow > block within the set).
	if matched, isAllow := e.customRules.evaluate(domain); matched {
		if isAllow {
			return Result{Disposition: Allow, BlocklistID: "custom_rule"}
		}
		return Result{Disposition: Block, BlocklistID: "custom_rule"}
	}

	if e.allowlist.matches(domain) {
		return Result{Disposition: Allow}
	}
	if checkScheduledAllowlist(e.scheduledAllowlist, e.schedules, domain, Now()) {
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

// DNSSECModeForClient returns the effective DNSSEC mode for the given client.
// It looks up the first matched profile; if that profile has a non-inherit
// dnssec_mode it is returned directly, otherwise the provided globalDefault
// is returned. Callers pass the cluster-wide dns.dnssec_mode as globalDefault.
func (e *Engine) DNSSECModeForClient(clientIP net.IP, id ClientIdentity, globalDefault string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	profileIDs := e.profilesMatchingLockedWithIdentity(clientIP, id)
	for _, pid := range profileIDs {
		if pe := e.findProfile(pid); pe != nil {
			switch pe.dnssecMode {
			case "validate", "transparent":
				return pe.dnssecMode
			}
		}
	}
	return globalDefault
}
