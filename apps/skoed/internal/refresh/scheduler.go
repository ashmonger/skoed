// Package refresh implements the M5.4 leader-only blocklist refresh
// scheduler. Started on every node; only the current Raft leader
// actually fetches. Results flow through the existing
// CmdBlocklistUpsert FSM command so every node converges.
package refresh

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/cluster"
	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter"
)

// Scheduler periodically refreshes URL-source blocklists on the leader.
type Scheduler struct {
	c             *cluster.Cluster
	tick          time.Duration
	defaultEvery  time.Duration
	httpTimeout   time.Duration
	maxConcurrent int

	cancel context.CancelFunc
	done   chan struct{}

	// failuresMu guards the per-blocklist failure counter map exposed via
	// PerBlocklistFailures(). Bumped each time the scheduler observes a
	// non-OK refresh; never reset.
	failuresMu sync.RWMutex
	failures   map[string]uint64
}

// Options bundles configuration knobs.
type Options struct {
	Tick             time.Duration // default 10s
	DefaultInterval  time.Duration // default 24h
	HTTPTimeout      time.Duration // default 30s
	MaxConcurrent    int           // default 4
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
		tick:          opts.Tick,
		defaultEvery:  opts.DefaultInterval,
		httpTimeout:   opts.HTTPTimeout,
		maxConcurrent: opts.MaxConcurrent,
		failures:      map[string]uint64{},
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
// fetches them in parallel.
func (s *Scheduler) refreshDueOnLeader() {
	if !s.c.IsLeader() {
		return
	}
	snap, err := s.c.Store().Snapshot()
	if err != nil {
		log.Printf("refresh: snapshot: %v", err)
		return
	}
	now := time.Now()
	due := s.dueBlocklists(snap, now)
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
			s.refreshOne(bl)
		}(bl)
	}
	wg.Wait()
}

// dueBlocklists returns the URL-source blocklists whose
// last_refresh_at + interval has passed. Sorted by oldest-due-first so
// stale lists get attention before the freshly-added ones.
func (s *Scheduler) dueBlocklists(snap *config.Config, now time.Time) []config.Blocklist {
	type item struct {
		bl  config.Blocklist
		age time.Duration
	}
	var items []item
	for _, bl := range snap.Filtering.Blocklists {
		if bl.Source.Type != "url" || bl.Source.URL == "" {
			continue
		}
		interval := time.Duration(bl.RefreshIntervalSeconds) * time.Second
		if bl.RefreshIntervalSeconds == 0 {
			// Explicit zero = "don't auto-refresh". Inherit-from-default is
			// signalled by the field being unset; YAML zero-value is the
			// same as explicit-zero, so we treat 0 as "disabled" for v1.
			// Operators who want the cluster default set the interval
			// explicitly.
			continue
		}
		var last time.Time
		if bl.LastRefreshAt != "" {
			if t, err := time.Parse(time.RFC3339, bl.LastRefreshAt); err == nil {
				last = t
			}
		}
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

// refreshOne fetches a single blocklist and writes the result through
// Raft. On HTTP/parse error, only the LastRefresh* fields are updated;
// the prior Domains slice is preserved.
func (s *Scheduler) refreshOne(prior config.Blocklist) {
	domains, err := filter.Download(prior.Source.URL, prior.Source.Format, s.httpTimeout)
	now := time.Now().UTC().Format(time.RFC3339)

	updated := prior
	updated.LastRefreshAt = now

	if err != nil {
		updated.LastRefreshStatus = "error"
		updated.LastRefreshError = trimError(err)
		s.bumpFailure(prior.ID)
	} else if domainsEqual(prior.Domains, domains) {
		updated.LastRefreshStatus = "unchanged"
		updated.LastRefreshError = ""
	} else {
		updated.LastRefreshStatus = "ok"
		updated.LastRefreshError = ""
		updated.LastUpdated = now
		updated.Domains = domains
	}

	if err := s.c.UpsertBlocklist(updated); err != nil {
		log.Printf("refresh: upsert %s: %v", prior.ID, err)
	}
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

// filter.Download builds its own HTTP client; ensure we don't leave a
// global http.Client around shadowing it.
var _ = http.DefaultClient
