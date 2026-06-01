package log

import (
	stdlog "log"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/dblock/dblock/internal/cluster"
)

// Committer is the subset of *cluster.Cluster that the Aggregator needs.
// Declared as an interface here so tests can stub it without spinning up a
// Raft node, and so the import direction stays one-way (log -> cluster only
// for the shared HourAggregate type; the behavioural seam is the interface).
type Committer interface {
	CommitHourlyAggregate(cluster.HourAggregate) error
	IsLeader() bool
}

// AggregatorConfig controls flush cadence and node identity.
type AggregatorConfig struct {
	NodeID        string
	FlushInterval time.Duration // default 60s
	TopN          int           // default 20
}

// retryBackoff is how long the Aggregator waits before retrying a failed
// Raft commit (e.g. leadership lost mid-flush, Raft busy). The bucket is
// retained across the wait so no counters are lost.
const retryBackoff = 30 * time.Second

// tickInterval is how often the flusher goroutine wakes to check whether
// the current hour bucket should be flushed.
const tickInterval = 1 * time.Second

// Aggregator builds hourly cluster-wide query stats. Each node runs one.
// The current hour bucket lives in memory; on flush it is encoded as a
// cluster.HourAggregate and committed via Raft so every node ends up with
// a replicated stats/{node_id}/{hour_unix} entry.
type Aggregator struct {
	cfg       AggregatorConfig
	committer Committer

	mu             sync.Mutex
	bucket         *hourBucket
	nextRetryAfter time.Time // zero means no pending retry

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// hourBucket holds the in-memory counters for one hour. Domains/clients are
// kept in maps for O(1) increment; top-N is computed only at flush time.
type hourBucket struct {
	hourStart time.Time
	openedAt  time.Time
	total     int
	blocked   int
	forwarded int
	cached    int
	local     int
	domains   map[string]int
	clients   map[string]int
}

func newHourBucket(now time.Time) *hourBucket {
	return &hourBucket{
		hourStart: now.Truncate(time.Hour),
		openedAt:  now,
		domains:   make(map[string]int),
		clients:   make(map[string]int),
	}
}

// NewAggregator returns an aggregator with the given config. Defaults are
// applied for any zero-valued field. The flush interval may be overridden
// in tests via DBLOCK_TEST_AGGREGATE_FLUSH_SECONDS when DBLOCK_TEST_MODE=1.
func NewAggregator(cfg AggregatorConfig, committer Committer) *Aggregator {
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 60 * time.Second
	}
	if cfg.TopN <= 0 {
		cfg.TopN = 20
	}
	if os.Getenv("DBLOCK_TEST_MODE") == "1" {
		if v := os.Getenv("DBLOCK_TEST_AGGREGATE_FLUSH_SECONDS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.FlushInterval = time.Duration(n) * time.Second
			}
		}
	}
	return &Aggregator{
		cfg:       cfg,
		committer: committer,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Observe records one query event. Safe for concurrent calls. Outcomes
// outside the known set still increment the total count but are not bucketed
// per-outcome — this is intentional so unexpected values don't get silently
// dropped from the cluster-wide total.
func (a *Aggregator) Observe(client, domain string, outcome Outcome) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bucket == nil {
		a.bucket = newHourBucket(time.Now())
	}
	a.bucket.total++
	switch outcome {
	case OutcomeBlocked:
		a.bucket.blocked++
	case OutcomeForwarded:
		a.bucket.forwarded++
	case OutcomeCached:
		a.bucket.cached++
	case OutcomeLocal:
		a.bucket.local++
	}
	if domain != "" {
		a.bucket.domains[domain]++
	}
	if client != "" {
		a.bucket.clients[client]++
	}
}

// Start spawns the flusher goroutine. Safe to call once per Aggregator.
func (a *Aggregator) Start() {
	go a.run()
}

// Stop terminates cleanly; drains a final flush attempt if pending counters
// exist. Idempotent.
func (a *Aggregator) Stop() {
	a.stopOnce.Do(func() {
		close(a.stopCh)
	})
	<-a.doneCh
}

func (a *Aggregator) run() {
	defer close(a.doneCh)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			a.tryFlush(time.Now(), true)
			return
		case now := <-ticker.C:
			a.tryFlush(now, false)
		}
	}
}

// tryFlush evaluates whether the current bucket is due to be flushed and,
// if so, commits it via Raft. The decision is:
//   - hour boundary crossed since the bucket opened, OR
//   - flushInterval elapsed since the bucket opened.
// On commit failure, the bucket is retained and retried after retryBackoff.
// On stop=true, we attempt one final flush regardless of cadence.
func (a *Aggregator) tryFlush(now time.Time, stop bool) {
	a.mu.Lock()
	if a.bucket == nil || a.bucket.total == 0 {
		a.mu.Unlock()
		return
	}
	if !stop {
		if !a.nextRetryAfter.IsZero() && now.Before(a.nextRetryAfter) {
			a.mu.Unlock()
			return
		}
		hourBoundary := now.Truncate(time.Hour).After(a.bucket.hourStart)
		intervalElapsed := now.Sub(a.bucket.openedAt) >= a.cfg.FlushInterval
		if !hourBoundary && !intervalElapsed {
			a.mu.Unlock()
			return
		}
	}
	bucket := a.bucket
	a.mu.Unlock()

	// CommitHourlyAggregate handles leader vs follower internally: on the
	// leader it Raft-applies; on a follower it HTTPs the aggregate to the
	// leader's internal endpoint, authenticated with the replicated cluster
	// secret. So this code path is uniform regardless of role.
	agg := a.buildAggregate(bucket)
	if err := a.committer.CommitHourlyAggregate(agg); err != nil {
		// Retain the bucket and back off. Common case: lost leadership
		// between the IsLeader check and the apply, or Raft is busy.
		a.mu.Lock()
		a.nextRetryAfter = now.Add(retryBackoff)
		a.mu.Unlock()
		stdlog.Printf("aggregator: commit hourly aggregate failed (will retry in %s): %v", retryBackoff, err)
		return
	}

	// Successful commit: reset to a fresh bucket aligned on the current
	// hour-floor so the next Observe starts a new bucket immediately.
	a.mu.Lock()
	a.bucket = nil
	a.nextRetryAfter = time.Time{}
	a.mu.Unlock()
}

// buildAggregate converts the in-memory bucket into the wire-format
// HourAggregate, applying top-N truncation to the domain and client maps.
func (a *Aggregator) buildAggregate(b *hourBucket) cluster.HourAggregate {
	return cluster.HourAggregate{
		NodeID:     a.cfg.NodeID,
		HourStart:  b.hourStart.Unix(),
		Total:      b.total,
		Blocked:    b.blocked,
		Forwarded:  b.forwarded,
		Cached:     b.cached,
		Local:      b.local,
		TopDomains: topN(b.domains, a.cfg.TopN),
		TopClients: topN(b.clients, a.cfg.TopN),
	}
}

// topN returns the n highest-count entries of m, sorted by count descending
// then by name ascending for deterministic ordering across nodes.
func topN(m map[string]int, n int) []cluster.NameCount {
	if len(m) == 0 || n <= 0 {
		return []cluster.NameCount{}
	}
	out := make([]cluster.NameCount, 0, len(m))
	for name, count := range m {
		out = append(out, cluster.NameCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
