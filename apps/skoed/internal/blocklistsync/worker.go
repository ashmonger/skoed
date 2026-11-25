// Package blocklistsync copies the downloaded content of URL blocklists
// from the Raft leader to this node (M50, TS-BlocklistContentDistribution
// §5.2). The replicated state carries the SHA-256 of the version every
// node must hold; the worker fetches that version from the leader's
// internal content endpoint whenever this node's copy differs.
//
// The worker runs on every node. On the leader it does no copy (the
// refresh scheduler downloads there) but still prunes copies of deleted
// lists.
package blocklistsync

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/blocklistcache"
	"github.com/skoed/skoed/internal/cluster"
	"github.com/skoed/skoed/internal/config"
)

const (
	debounce      = 500 * time.Millisecond
	fetchTimeout  = 60 * time.Second
	maxConcurrent = 2
)

// ContentPath returns the path of the internal content endpoint for list
// id, relative to a node's API base URL.
func ContentPath(id, sha256 string) string {
	return "/api/v1/cluster/_internal/blocklists/" + url.PathEscape(id) + "/content?sha256=" + url.QueryEscape(sha256)
}

// Worker copies missing list versions from the leader.
type Worker struct {
	c     *cluster.Cluster
	cache *blocklistcache.Cache
	tick  time.Duration

	kick   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}

	failuresMu sync.Mutex
	failures   map[string]uint64
}

// New creates a worker. tick is the interval between passes (10 s in
// production, 1 s in test mode).
func New(c *cluster.Cluster, cache *blocklistcache.Cache, tick time.Duration) *Worker {
	if tick <= 0 {
		tick = 10 * time.Second
	}
	return &Worker{
		c:        c,
		cache:    cache,
		tick:     tick,
		kick:     make(chan struct{}, 1),
		failures: map[string]uint64{},
	}
}

// Notify asks for a pass soon. Registered as a cluster subscriber, so it
// runs after every apply and after a snapshot restore; it never blocks.
func (w *Worker) Notify() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Start spawns the worker goroutine and subscribes it to cluster
// notifications. A second Start is a no-op.
func (w *Worker) Start() {
	if w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan struct{})
	w.c.Subscribe(w.Notify)
	go w.loop(ctx)
}

// Stop signals the worker to exit and waits for it.
func (w *Worker) Stop() {
	if w.cancel == nil {
		return
	}
	w.cancel()
	<-w.done
	w.cancel = nil
}

// FetchFailures returns the number of failed copies per list since process
// start (skoed_blocklist_content_fetch_failures_total).
func (w *Worker) FetchFailures() map[string]uint64 {
	w.failuresMu.Lock()
	defer w.failuresMu.Unlock()
	out := make(map[string]uint64, len(w.failures))
	for k, v := range w.failures {
		out[k] = v
	}
	return out
}

func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	w.pass(ctx)
	t := time.NewTicker(w.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(debounce):
			}
		}
		w.pass(ctx)
	}
}

// pass prunes copies of deleted lists, seeds copies of legacy lists that
// have none, and, on a follower, copies every enabled URL list whose
// replicated version differs from this node's copy.
func (w *Worker) pass(ctx context.Context) {
	snap, err := w.c.Store().Snapshot()
	if err != nil {
		log.Printf("blocklistsync: snapshot: %v", err)
		return
	}
	known := make(map[string]bool, len(snap.Filtering.Blocklists))
	for _, bl := range snap.Filtering.Blocklists {
		known[bl.ID] = true
	}
	w.cache.Prune(func(id string) bool { return known[id] })
	w.cache.SeedLegacy(snap, true)

	if w.c.IsLeader() {
		return
	}
	var wanted []config.Blocklist
	for _, bl := range snap.Filtering.Blocklists {
		if bl.Enabled && bl.Source.Type == "url" && bl.Content != nil && !w.cache.Has(bl.ID, bl.Content.SHA256) {
			wanted = append(wanted, bl)
		}
	}
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for _, bl := range wanted {
		wg.Add(1)
		sem <- struct{}{}
		go func(id, sha string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := w.fetch(ctx, id, sha); err != nil {
				w.failuresMu.Lock()
				w.failures[id]++
				w.failuresMu.Unlock()
				log.Printf("blocklistsync: copy %q (%.12s) from the leader: %v", id, sha, err)
			}
		}(bl.ID, bl.Content.SHA256)
	}
	wg.Wait()
}

// fetch copies version sha of list id from the leader into the cache. On
// any failure the previous copy is kept and the next pass retries.
func (w *Worker) fetch(ctx context.Context, id, sha string) error {
	base := w.c.LeaderAPIAddress()
	if base == "" {
		return fmt.Errorf("no leader")
	}
	secret := w.c.ClusterSecret()
	if secret == "" {
		return fmt.Errorf("cluster secret not yet replicated")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+ContentPath(id, sha), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Cluster-Secret", secret)
	// Set explicitly, so the transport leaves the body compressed and we
	// decompress it here, whatever the client.
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := w.c.PeerHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("leader answered %s", resp.Status)
	}
	var body io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("gzip body: %w", err)
		}
		defer gz.Close()
		body = gz
	}
	_, err = w.cache.Install(id, body, sha)
	return err
}
