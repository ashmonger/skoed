package dohresolvers

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ClusterAPI is the subset of *cluster.Cluster the scheduler needs.
// Defined as an interface to keep the dohresolvers package free of a
// cluster import (and easy to fake in tests).
type ClusterAPI interface {
	IsLeader() bool
	CurrentDohSnapshot() (*Snapshot, error)
	UpsertDohResolverSnapshot(snap Snapshot) error
	RecordDohResolverRefreshFailure(attemptedAt time.Time, reason string) error
}

// Options bundles the knobs a Scheduler needs at construction.
type Options struct {
	UpstreamURL     string
	RefreshInterval time.Duration // typically 24h; SKOED_TEST_DOH_RESOLVER_REFRESH_SECONDS overrides
	RequestTimeout  time.Duration // HTTP timeout per attempt
	StaleAfter      time.Duration // typically 7d; informational (handlers compute)
	Tick            time.Duration // poll cadence; default 10s (test mode 1s)
	BackoffSteps    []time.Duration
}

// Scheduler runs on every node. It only does work when it observes
// IsLeader()=true; otherwise each tick returns immediately.
type Scheduler struct {
	c    ClusterAPI
	opts Options

	cancel context.CancelFunc
	done   chan struct{}

	// runMu serialises refresh cycles so concurrent ticks (test-mode
	// 1s cadence + manual /refresh nudges) collapse to a single
	// in-flight fetch (FS-DohResolverDbLeaderOnlyScheduler).
	runMu sync.Mutex

	// refreshNow is closed-and-replaced to wake the loop from sleep
	// when an operator hits POST /api/v1/doh-resolvers/refresh.
	pingMu sync.Mutex
	ping   chan struct{}

	// counters; surfaced via Snapshot() to the metrics collector.
	successes atomic.Uint64
	failures  atomic.Uint64
}

// New wires a Scheduler. The caller starts and stops it.
func New(c ClusterAPI, opts Options) *Scheduler {
	if opts.Tick <= 0 {
		opts.Tick = 10 * time.Second
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 24 * time.Hour
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 20 * time.Second
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = 7 * 24 * time.Hour
	}
	if len(opts.BackoffSteps) == 0 {
		opts.BackoffSteps = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	}
	// Test overrides keep the acceptance suite under its 15s budget.
	if v := os.Getenv("SKOED_TEST_DOH_RESOLVER_REFRESH_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.RefreshInterval = time.Duration(n) * time.Second
			// Shorten the poll cadence too so the leader's first tick
			// lands quickly.
			if opts.Tick > opts.RefreshInterval {
				opts.Tick = opts.RefreshInterval
			}
		}
	}
	if v := os.Getenv("SKOED_TEST_DOH_RESOLVER_BACKOFF_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			d := time.Duration(n) * time.Millisecond
			opts.BackoffSteps = []time.Duration{d, d, d}
		}
	}
	if v := os.Getenv("SKOED_TEST_DOH_RESOLVER_UPSTREAM"); v != "" {
		opts.UpstreamURL = v
	}
	return &Scheduler{c: c, opts: opts, ping: make(chan struct{}, 1)}
}

// Counters returns the (success, failure) totals observed since boot.
// Used by the metrics collector to surface skoed_doh_resolver_refresh_total.
func (s *Scheduler) Counters() (success, failure uint64) {
	return s.successes.Load(), s.failures.Load()
}

// Start spawns the worker goroutine. Idempotent.
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

// Nudge requests an immediate refresh attempt on the next loop iteration.
// Non-blocking; duplicate nudges before the first one fires are coalesced.
func (s *Scheduler) Nudge() {
	s.pingMu.Lock()
	select {
	case s.ping <- struct{}{}:
	default:
	}
	s.pingMu.Unlock()
}

// CurrentSnapshotID is convenient for the POST /refresh response body.
func (s *Scheduler) CurrentSnapshotID() string {
	snap, err := s.c.CurrentDohSnapshot()
	if err != nil || snap == nil {
		return ""
	}
	return snap.SnapshotID
}

func (s *Scheduler) loop(ctx context.Context) {
	defer close(s.done)
	// First tick runs immediately so the bundled-seed path actually
	// reaches the upstream once the leader is elected — FS-DohResolverDbScheduledDailyRefresh.
	s.tickOnLeader(false)
	t := time.NewTicker(s.opts.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tickOnLeader(false)
		case <-s.ping:
			// Operator-triggered nudge: bypass the "is a refresh due?"
			// check so the next cycle starts immediately regardless of
			// when the last attempt landed.
			s.tickOnLeader(true)
		}
	}
}

// tickOnLeader is the per-tick body. Returns immediately on followers.
// When force is true, the "is a refresh due?" check is skipped — used
// by Nudge() to honour a manual POST /refresh without waiting for the
// next interval boundary.
func (s *Scheduler) tickOnLeader(force bool) {
	if !s.c.IsLeader() {
		return
	}
	if !s.runMu.TryLock() {
		return // a cycle is already running; collapse this tick
	}
	defer s.runMu.Unlock()

	// Decide if a refresh is due. We use last_refresh_attempt_at —
	// failed attempts also reset the clock so we don't hammer a
	// broken upstream every tick.
	now := time.Now().UTC()
	if !force {
		snap, err := s.c.CurrentDohSnapshot()
		if err != nil {
			log.Printf("dohresolvers: read snapshot: %v", err)
			return
		}
		if snap != nil && snap.LastRefreshAttemptAt != "" {
			if last, perr := time.Parse(time.RFC3339, snap.LastRefreshAttemptAt); perr == nil {
				if now.Sub(last) < s.opts.RefreshInterval {
					return
				}
			}
		}
	}
	s.runCycle(now)
}

// runCycle attempts the configured backoff sequence. On success, the
// new snapshot is replicated through Raft. On total failure, only the
// failure metadata is replicated; the prior snapshot is preserved
// (FS-DohResolverDbUpstreamFailureKeepsLastGoodSnapshot).
func (s *Scheduler) runCycle(startedAt time.Time) {
	if s.opts.UpstreamURL == "" {
		// No upstream feed configured — promote the bundled seed to a
		// fresh, Raft-replicated snapshot so every node converges to
		// the same SeedSnapshot bytes and stale checks work off a
		// recent fetched_at. Treated as a successful refresh because
		// the seed IS the authoritative snapshot in this deployment
		// posture (air-gapped, opinionated bundled list).
		entries := BundledSeed()
		body, _ := json.Marshal(entries) // deterministic for a stable seed
		// Use RFC3339Nano so back-to-back refreshes (e.g. the acceptance
		// test's force-refresh issued < 1s after the boot fetch) get
		// distinct snapshot ids. RFC3339Nano is a strict superset of
		// RFC3339 and parses correctly via time.Parse(time.RFC3339, …).
		fetched := time.Now().UTC().Format(time.RFC3339Nano)
		newSnap := Snapshot{
			SnapshotID:           snapshotIDFor(fetched, body),
			SourceURL:            "bundled-seed",
			FetchedAt:            fetched,
			LastRefreshAttemptAt: fetched,
			LastRefreshSuccessAt: fetched,
			LastRefreshError:     "",
			Resolvers:            entries,
		}
		if err := s.c.UpsertDohResolverSnapshot(newSnap); err != nil {
			log.Printf("dohresolvers: replicate seed snapshot: %v", err)
			s.failures.Add(1)
			return
		}
		s.successes.Add(1)
		return
	}

	// Backoff layout: first attempt immediate; later attempts each sleep
	// for one BackoffSteps entry before re-trying. With the default
	// [30s, 2m, 10m] we therefore do 3 attempts total. Test mode
	// (SKOED_TEST_DOH_RESOLVER_BACKOFF_MS) compresses every step to the
	// same short duration so the suite stays under its 15s budget.
	attempts := len(s.opts.BackoffSteps)
	if attempts < 1 {
		attempts = 1
	}
	var lastErr string
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(s.opts.BackoffSteps[attempt-1])
		}
		entries, body, err := fetchAndValidate(s.opts.UpstreamURL, s.opts.RequestTimeout)
		if err != nil {
			lastErr = err.Error()
			log.Printf("dohresolvers: attempt %d failed: %v", attempt+1, err)
			continue
		}
		// Use RFC3339Nano so back-to-back refreshes (e.g. the acceptance
		// test's force-refresh issued < 1s after the boot fetch) get
		// distinct snapshot ids. RFC3339Nano is a strict superset of
		// RFC3339 and parses correctly via time.Parse(time.RFC3339, …).
		fetched := time.Now().UTC().Format(time.RFC3339Nano)
		newSnap := Snapshot{
			SnapshotID:           snapshotIDFor(fetched, body),
			SourceURL:            s.opts.UpstreamURL,
			FetchedAt:            fetched,
			LastRefreshAttemptAt: fetched,
			LastRefreshSuccessAt: fetched,
			LastRefreshError:     "",
			Resolvers:            entries,
		}
		if err := s.c.UpsertDohResolverSnapshot(newSnap); err != nil {
			log.Printf("dohresolvers: replicate snapshot: %v", err)
			lastErr = "replicate: " + err.Error()
			continue
		}
		s.successes.Add(1)
		return
	}

	if lastErr == "" {
		lastErr = "unknown upstream failure"
	}
	if err := s.c.RecordDohResolverRefreshFailure(time.Now().UTC(), lastErr); err != nil {
		log.Printf("dohresolvers: record failure: %v", err)
	}
	s.failures.Add(1)
}
