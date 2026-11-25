// Package refresh implements the M5.4 leader-only blocklist refresh
// scheduler. Started on every node; only the current Raft leader
// actually fetches.
//
// M50 (TS-BlocklistContentDistribution §4): the leader writes the
// downloaded domains to its node-local cache and replicates only their
// version through a small `blocklist.content_set` entry; followers copy the
// file from the leader (internal/blocklistsync). Until every cluster member
// supports this (capability blocklist-content-v1), the leader keeps the
// previous behavior and replicates the domains with `blocklist.upsert`, so
// nodes running a previous version keep filtering during a rolling upgrade.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/blocklistcache"
	"github.com/skoed/skoed/internal/cluster"
	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter"
)

// Scheduler periodically refreshes URL-source blocklists on the leader.
type Scheduler struct {
	c             *cluster.Cluster
	cache         *blocklistcache.Cache
	tick          time.Duration
	defaultEvery  time.Duration
	httpTimeout   time.Duration
	maxConcurrent int
	retryAfter    time.Duration

	cancel context.CancelFunc
	done   chan struct{}

	// failuresMu guards the per-blocklist failure counter map exposed via
	// PerBlocklistFailures(). Bumped each time the scheduler observes a
	// non-OK refresh; never reset.
	failuresMu sync.RWMutex
	failures   map[string]uint64

	// attemptsMu guards attempts, the last download attempt of each list
	// on this node. A failed download is recorded once in the replicated
	// state, so LastRefreshAt stops moving while the error stays the same;
	// the interval and retry rules use this local time instead.
	attemptsMu sync.Mutex
	attempts   map[string]attempt

	// idLocks serializes the refreshes of one list (tick and manual
	// refresh).
	idLocksMu sync.Mutex
	idLocks   map[string]*sync.Mutex

	// gateLogged is set once the "capability gate not met" message was
	// logged during the current leadership term.
	gateLogged bool

	// download, upsertBlocklist and setBlocklistContent are the downloads
	// and Raft writes of the scheduler. New sets them to filter.Download
	// and the cluster methods; unit tests replace them.
	download            func(url, format string, timeout time.Duration) ([]string, error)
	upsertBlocklist     func(bl config.Blocklist) error
	setBlocklistContent func(p cluster.BlocklistContentSetPayload) error

	// OnDownloadFailed is an optional hook called after a blocklist download
	// fails. Receives the blocklist ID, its human-readable name, and the
	// short error string. Nil disables the hook. Set before calling Start.
	OnDownloadFailed func(id, name, errStr string)
}

type attempt struct {
	at     time.Time
	failed bool
	legacy bool // made by refreshLegacy (capability gate not met)
}

// Options bundles configuration knobs.
type Options struct {
	Tick            time.Duration // default 10s
	DefaultInterval time.Duration // default 24h
	HTTPTimeout     time.Duration // default 30s
	MaxConcurrent   int           // default 4
	// Cache is this node's copy of URL list content (M50). Nil keeps the
	// behavior of previous versions: domains replicated through Raft.
	Cache *blocklistcache.Cache
}

// New creates a scheduler. Caller starts/stops it.
func New(c *cluster.Cluster, opts Options) *Scheduler {
	if opts.Tick <= 0 {
		opts.Tick = 10 * time.Second
	}
	if opts.DefaultInterval <= 0 {
		opts.DefaultInterval = 24 * time.Hour
	}
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = 30 * time.Second
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 4
	}
	return &Scheduler{
		c:             c,
		cache:         opts.Cache,
		tick:          opts.Tick,
		defaultEvery:  opts.DefaultInterval,
		httpTimeout:   opts.HTTPTimeout,
		maxConcurrent: opts.MaxConcurrent,
		// A list with no copy is due on every tick; after a failed
		// download, wait 6 ticks (1 min in production) before retrying.
		retryAfter: 6 * opts.Tick,
		failures:   map[string]uint64{},
		attempts:   map[string]attempt{},
		idLocks:    map[string]*sync.Mutex{},

		download:            filter.Download,
		upsertBlocklist:     c.UpsertBlocklist,
		setBlocklistContent: c.SetBlocklistContent,
	}
}

// Start spawns the worker goroutine. Idempotent: a second Start is a no-op.
func (s *Scheduler) Start() {
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.loop(ctx)
}

// Stop signals the worker to exit and waits for it.
func (s *Scheduler) Stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
	s.cancel = nil
}

// PerBlocklistFailures returns a snapshot of the lifetime failure
// counter for each blocklist id. Used by the metrics exporter.
func (s *Scheduler) PerBlocklistFailures() map[string]uint64 {
	s.failuresMu.RLock()
	defer s.failuresMu.RUnlock()
	out := make(map[string]uint64, len(s.failures))
	for k, v := range s.failures {
		out[k] = v
	}
	return out
}

// UsesContent reports whether URL list content is kept out of the
// replicated state: the node has a cache and every cluster member has the
// blocklist-content-v1 capability.
func (s *Scheduler) UsesContent() bool {
	return s.cache != nil && s.c.AllMembersHaveCapability(cluster.CapabilityBlocklistContentV1)
}

// FetchContent downloads a URL list for a list being created, writes it
// to this node's cache under id, and returns the content version to commit
// with the list. Called on the leader by the create handlers.
func (s *Scheduler) FetchContent(id, url, format string) (*config.BlocklistContent, error) {
	if s.cache == nil {
		return nil, fmt.Errorf("no blocklist cache on this node")
	}
	domains, err := s.download(url, format, s.httpTimeout)
	if err != nil {
		return nil, err
	}
	e, err := s.cache.Write(id, domains)
	if err != nil {
		return nil, fmt.Errorf("write copy of %q: %w", id, err)
	}
	return &config.BlocklistContent{
		SHA256:      e.SHA256,
		DomainCount: e.Count,
		FetchedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ErrBlocklistNotFound is returned by RefreshNow when the list does not
// exist, e.g. it was deleted after the handler looked it up.
var ErrBlocklistNotFound = errors.New("blocklist not found")

// DownloadError is returned by RefreshNow when the list could not be
// downloaded; the previous version is kept.
type DownloadError struct{ Err error }

func (e *DownloadError) Error() string { return e.Err.Error() }
func (e *DownloadError) Unwrap() error { return e.Err }

// RefreshNow downloads list id synchronously and records the result like
// the scheduler does (POST /api/v1/blocklists/{id}/refresh). It must run on
// the leader. It returns:
//
//   - an error wrapping cluster.ErrNotLeader when this node is not the
//     leader, or lost the leadership during the call, so the result may not
//     be recorded;
//   - ErrBlocklistNotFound when the list does not exist;
//   - a *DownloadError when the download failed;
//   - any other error when the result could not be written (cache or Raft).
func (s *Scheduler) RefreshNow(id string) error {
	if !s.c.IsLeader() {
		return cluster.ErrNotLeader
	}
	snap, err := s.c.Store().Snapshot()
	if err != nil {
		return err
	}
	for _, bl := range snap.Filtering.Blocklists {
		if bl.ID != id {
			continue
		}
		if s.UsesContent() {
			if bl.IsLegacyURLContent() {
				s.seedLegacy(bl)
				if fresh, ok := s.storedList(id); ok {
					bl = fresh
				}
			}
			err = s.refreshContent(bl)
		} else {
			err = s.refreshLegacy(bl)
		}
		if !s.c.IsLeader() {
			return fmt.Errorf("%w: leadership lost during the refresh of %q", cluster.ErrNotLeader, id)
		}
		return err
	}
	return fmt.Errorf("%w: %q", ErrBlocklistNotFound, id)
}

func (s *Scheduler) loop(ctx context.Context) {
	defer close(s.done)
	// First tick immediately so a freshly-created URL blocklist gets its
	// initial fetch within a few seconds of being added.
	s.refreshDueOnLeader()
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refreshDueOnLeader()
		}
	}
}

// refreshDueOnLeader is the per-tick body. Returns immediately on
// followers; otherwise picks up to maxConcurrent due blocklists and
// fetches them in parallel. A node that becomes leader evaluates the due
// rules on its first tick, so a failover needs no other hook.
func (s *Scheduler) refreshDueOnLeader() {
	if !s.c.IsLeader() {
		s.gateLogged = false
		return
	}
	snap, err := s.c.Store().Snapshot()
	if err != nil {
		log.Printf("refresh: snapshot: %v", err)
		return
	}
	now := time.Now()

	useContent := s.UsesContent()
	if !useContent && s.cache != nil && !s.gateLogged {
		log.Printf("refresh: members without capability %s: %s; downloaded blocklist domains stay in the replicated state",
			cluster.CapabilityBlocklistContentV1,
			strings.Join(s.c.MembersLackingCapability(cluster.CapabilityBlocklistContentV1), ", "))
		s.gateLogged = true
	}

	var due []config.Blocklist
	if useContent {
		for _, bl := range snap.Filtering.Blocklists {
			if bl.IsLegacyURLContent() {
				s.seedLegacy(bl)
			} else {
				s.clearLegacyAttempt(bl.ID)
			}
		}
		due = s.dueContent(snap, now)
	} else {
		due = s.dueLegacy(snap, now)
	}
	if len(due) == 0 {
		return
	}
	if len(due) > s.maxConcurrent {
		due = due[:s.maxConcurrent]
	}
	var wg sync.WaitGroup
	for _, bl := range due {
		wg.Add(1)
		go func(bl config.Blocklist) {
			defer wg.Done()
			if useContent {
				_ = s.refreshContent(bl)
			} else {
				_ = s.refreshLegacy(bl)
			}
		}(bl)
	}
	wg.Wait()
}

// lastRun returns the later of the replicated LastRefreshAt and this
// node's last attempt.
func (s *Scheduler) lastRun(bl config.Blocklist) (time.Time, attempt) {
	var last time.Time
	if bl.LastRefreshAt != "" {
		if t, err := time.Parse(time.RFC3339, bl.LastRefreshAt); err == nil {
			last = t
		}
	}
	s.attemptsMu.Lock()
	a := s.attempts[bl.ID]
	s.attemptsMu.Unlock()
	if a.at.After(last) {
		last = a.at
	}
	return last, a
}

func (s *Scheduler) recordAttempt(id string, failed bool) {
	s.setAttempt(id, attempt{at: time.Now(), failed: failed})
}

func (s *Scheduler) setAttempt(id string, a attempt) {
	s.attemptsMu.Lock()
	s.attempts[id] = a
	s.attemptsMu.Unlock()
}

// clearLegacyAttempt marks the last attempt on list id as not made by
// refreshLegacy, keeping its time: the content path now handles the list,
// so if the gate is lost again, dueLegacy must download it at once instead
// of waiting for its interval. The time is kept because dueContent
// measures the interval from it (lastRun).
func (s *Scheduler) clearLegacyAttempt(id string) {
	s.attemptsMu.Lock()
	if a, ok := s.attempts[id]; ok && a.legacy {
		a.legacy = false
		s.attempts[id] = a
	}
	s.attemptsMu.Unlock()
}

// mostOverdue is the age given to a list that is due whatever its
// interval, so it sorts before every list due on its interval.
const mostOverdue = time.Duration(math.MaxInt64)

// dueContent returns the URL lists the leader must download (§4): the
// interval has elapsed, or the list has no content version yet, or the
// leader holds no copy of that version (whatever the interval, including
// 0). Legacy entries are not due: the leader records their version from
// the replicated domains instead (seedLegacy). Lists with no copy come
// first, then the most overdue.
func (s *Scheduler) dueContent(snap *config.Config, now time.Time) []config.Blocklist {
	type item struct {
		bl  config.Blocklist
		age time.Duration
	}
	var items []item
	for _, bl := range snap.Filtering.Blocklists {
		if bl.Source.Type != "url" || bl.Source.URL == "" || bl.IsLegacyURLContent() {
			continue
		}
		last, a := s.lastRun(bl)
		missing := bl.Content == nil || !s.cache.Has(bl.ID, bl.Content.SHA256)
		if missing {
			if a.failed && now.Sub(a.at) < s.retryAfter {
				continue
			}
			items = append(items, item{bl: bl, age: mostOverdue})
			continue
		}
		if bl.RefreshIntervalSeconds <= 0 {
			continue
		}
		next := last.Add(time.Duration(bl.RefreshIntervalSeconds) * time.Second)
		if now.Before(next) {
			continue
		}
		items = append(items, item{bl: bl, age: now.Sub(next)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].age > items[j].age })
	out := make([]config.Blocklist, len(items))
	for i, it := range items {
		out[i] = it.bl
	}
	return out
}

// lockID serializes the refreshes of one list and returns the unlock.
func (s *Scheduler) lockID(id string) func() {
	s.idLocksMu.Lock()
	m, ok := s.idLocks[id]
	if !ok {
		m = &sync.Mutex{}
		s.idLocks[id] = m
	}
	s.idLocksMu.Unlock()
	m.Lock()
	return m.Unlock
}

// storedList reads the current replicated state of list id.
func (s *Scheduler) storedList(id string) (config.Blocklist, bool) {
	snap, err := s.c.Store().Snapshot()
	if err != nil {
		return config.Blocklist{}, false
	}
	for _, bl := range snap.Filtering.Blocklists {
		if bl.ID == id {
			return bl, true
		}
	}
	return config.Blocklist{}, false
}

// refreshContent downloads one URL list and records the result with a
// `blocklist.content_set` entry (§4 refreshOne). The entry carries the
// source used, so it is ignored if the source changed meanwhile:
//
//   - on error, the previous copy is kept and an entry is written only when
//     the stored status or error message differs, so a list that keeps
//     failing writes one entry, not one per tick;
//   - on success, the copy is written to the cache first, then the version
//     is recorded: "unchanged" when it equals the stored one, "ok" with the
//     new version otherwise.
func (s *Scheduler) refreshContent(prior config.Blocklist) error {
	defer s.lockID(prior.ID)()
	// The list may have changed while waiting for the lock.
	if fresh, ok := s.storedList(prior.ID); ok {
		prior = fresh
	}
	domains, err := s.download(prior.Source.URL, prior.Source.Format, s.httpTimeout)
	now := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		msg := trimError(err)
		s.recordAttempt(prior.ID, true)
		s.bumpFailure(prior.ID)
		if s.OnDownloadFailed != nil {
			s.OnDownloadFailed(prior.ID, prior.Name, msg)
		}
		if prior.LastRefreshStatus != "error" || prior.LastRefreshError != msg {
			_ = s.apply(cluster.BlocklistContentSetPayload{
				ID: prior.ID, Status: "error", Error: msg, RefreshAt: now,
				URL: prior.Source.URL, Format: prior.Source.Format,
			})
		}
		return &DownloadError{Err: err}
	}
	e, err := s.cache.Write(prior.ID, domains)
	if err != nil {
		// Disk full or similar: no entry, the list is retried later.
		s.recordAttempt(prior.ID, true)
		log.Printf("refresh: write copy of %s: %v", prior.ID, err)
		return err
	}
	s.recordAttempt(prior.ID, false)
	p := cluster.BlocklistContentSetPayload{
		ID: prior.ID, Status: "unchanged", RefreshAt: now,
		URL: prior.Source.URL, Format: prior.Source.Format,
	}
	if prior.Content == nil || prior.Content.SHA256 != e.SHA256 {
		p.Status = "ok"
		p.Content = &config.BlocklistContent{SHA256: e.SHA256, DomainCount: e.Count, FetchedAt: now}
	}
	return s.apply(p)
}

// seedLegacy records the version of a legacy URL list (domains replicated
// by a previous version) from the leader's own copy of those domains,
// without downloading (§6 step 3). This clears the domains from the
// replicated state. The list is now handled by the content path, so the
// legacy flag of its attempt record is cleared (clearLegacyAttempt).
func (s *Scheduler) seedLegacy(bl config.Blocklist) {
	defer s.lockID(bl.ID)()
	e, err := s.cache.WriteLegacy(bl)
	if err != nil {
		log.Printf("refresh: seed copy of %s: %v", bl.ID, err)
		return
	}
	fetched := bl.LastUpdated
	if fetched == "" {
		fetched = time.Now().UTC().Format(time.RFC3339)
	}
	refreshAt := bl.LastRefreshAt
	if refreshAt == "" {
		refreshAt = fetched
	}
	err = s.apply(cluster.BlocklistContentSetPayload{
		ID:        bl.ID,
		Status:    "ok",
		RefreshAt: refreshAt,
		Content:   &config.BlocklistContent{SHA256: e.SHA256, DomainCount: e.Count, FetchedAt: fetched},
		URL:       bl.Source.URL,
		Format:    bl.Source.Format,
	})
	if err == nil {
		s.clearLegacyAttempt(bl.ID)
	}
}

// apply writes p through Raft; a failure is logged and returned.
func (s *Scheduler) apply(p cluster.BlocklistContentSetPayload) error {
	err := s.setBlocklistContent(p)
	if err != nil {
		log.Printf("refresh: record %s result of %s: %v", p.Status, p.ID, err)
	}
	return err
}

// dueLegacy returns the URL-source blocklists whose last_refresh_at +
// interval has passed, as before M50. A list holding a content version but
// no domains (written while the gate was met, before a rollback or the
// join of a node running a previous version) is due whatever its
// interval, so nodes running a previous version get domains again. Such a
// list is downloaded at most once per retryAfter, and only until a legacy
// refresh on this node succeeds: a download with no domains leaves the
// content version in place (keepStoredContent), so the list is then due on
// its interval instead of on every tick. Attempts made while the gate was
// met do not count (seedLegacy and the content path clear the legacy flag),
// so a gate loss is handled on the next tick. Sorted
// by oldest-due-first so stale lists get attention before the
// freshly-added ones.
func (s *Scheduler) dueLegacy(snap *config.Config, now time.Time) []config.Blocklist {
	type item struct {
		bl  config.Blocklist
		age time.Duration
	}
	var items []item
	for _, bl := range snap.Filtering.Blocklists {
		if bl.Source.Type != "url" || bl.Source.URL == "" {
			continue
		}
		if bl.Content != nil && len(bl.Domains) == 0 {
			_, a := s.lastRun(bl)
			if a.legacy && now.Sub(a.at) < s.retryAfter {
				continue
			}
			if !a.legacy || a.failed {
				items = append(items, item{bl: bl, age: mostOverdue})
				continue
			}
		}
		if bl.RefreshIntervalSeconds == 0 {
			// Explicit zero = "don't auto-refresh". Inherit-from-default is
			// signalled by the field being unset; YAML zero-value is the
			// same as explicit-zero, so we treat 0 as "disabled" for v1.
			// Operators who want the cluster default set the interval
			// explicitly.
			continue
		}
		interval := time.Duration(bl.RefreshIntervalSeconds) * time.Second
		last, _ := s.lastRun(bl)
		next := last.Add(interval)
		if now.Before(next) {
			continue
		}
		items = append(items, item{bl: bl, age: now.Sub(next)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].age > items[j].age })
	out := make([]config.Blocklist, len(items))
	for i, it := range items {
		out[i] = it.bl
	}
	return out
}

// refreshLegacy fetches a single blocklist and writes the full list,
// domains included, through Raft (behavior before M50, kept while the
// capability gate is not met). On HTTP/parse error, only the LastRefresh*
// fields are updated; the prior Domains slice is preserved.
func (s *Scheduler) refreshLegacy(prior config.Blocklist) error {
	defer s.lockID(prior.ID)()
	domains, err := s.download(prior.Source.URL, prior.Source.Format, s.httpTimeout)
	now := time.Now().UTC().Format(time.RFC3339)

	updated := prior
	updated.LastRefreshAt = now

	if err != nil {
		updated.LastRefreshStatus = "error"
		updated.LastRefreshError = trimError(err)
		s.bumpFailure(prior.ID)
		if s.OnDownloadFailed != nil {
			s.OnDownloadFailed(prior.ID, prior.Name, trimError(err))
		}
	} else if domainsEqual(prior.Domains, domains) {
		updated.LastRefreshStatus = "unchanged"
		updated.LastRefreshError = ""
	} else {
		updated.LastRefreshStatus = "ok"
		updated.LastRefreshError = ""
		updated.LastUpdated = now
		updated.Domains = domains
		updated.Content = nil
	}

	uerr := s.upsertBlocklist(updated)
	s.setAttempt(prior.ID, attempt{at: time.Now(), failed: err != nil || uerr != nil, legacy: true})
	if uerr != nil {
		log.Printf("refresh: upsert %s: %v", prior.ID, uerr)
		if err == nil {
			return uerr
		}
	}
	if err != nil {
		return &DownloadError{Err: err}
	}
	return nil
}

func (s *Scheduler) bumpFailure(id string) {
	s.failuresMu.Lock()
	s.failures[id]++
	s.failuresMu.Unlock()
}

// trimError returns a short single-line representation suitable for the
// LastRefreshError field (operators see this in the UI).
func trimError(err error) string {
	s := err.Error()
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

// domainsEqual returns true when two domain slices contain the same
// set of names (order-insensitive, case-insensitive).
func domainsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, d := range a {
		seen[strings.ToLower(d)] = struct{}{}
	}
	for _, d := range b {
		if _, ok := seen[strings.ToLower(d)]; !ok {
			return false
		}
	}
	return true
}
