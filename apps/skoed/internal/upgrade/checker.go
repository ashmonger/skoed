// Package upgrade implements the in-place upgrade subsystem:
// - Checker polls the release feed on a background goroutine and caches
//   the latest snapshot in memory (no per-request fetch).
// - Swap (swapper.go) downloads the new tar.gz, extracts the binary, and
//   atomically replaces the running executable via os.Rename.
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Feed is the parsed release feed document. Matches the spec layout
// in specs/technical/in-place-upgrade.md.
type Feed struct {
	Version         string            `json:"version"`
	PublishedAt     string            `json:"published_at"`
	ReleaseNotesURL string            `json:"release_notes_url"`
	Assets          map[string]string `json:"assets"`
}

// CheckResult is the shape returned by /api/v1/upgrade/check.
type CheckResult struct {
	CurrentVersion   string            `json:"current_version"`
	AvailableVersion string            `json:"available_version"`
	UpgradeAvailable bool              `json:"upgrade_available"`
	ReleaseNotesURL  string            `json:"release_notes_url"`
	PublishedAt      string            `json:"published_at"`
	CheckedAt        string            `json:"checked_at"`
	Assets           map[string]string `json:"assets,omitempty"`
}

// Checker polls the release feed on a fixed interval and caches the
// latest result in memory.
type Checker struct {
	currentVersion string
	feedURL        string
	pollEvery      time.Duration
	httpTimeout    time.Duration

	mu        sync.RWMutex
	feed      *Feed
	checkedAt time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

// Options bundles the wire-up knobs.
type Options struct {
	CurrentVersion string        // injected from main.go
	FeedURL        string        // empty disables polling entirely
	PollInterval   time.Duration // default 6 h
	HTTPTimeout    time.Duration // default 10 s
}

// New builds a Checker. Start must be called separately.
func New(opts Options) *Checker {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 6 * time.Hour
	}
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = 10 * time.Second
	}
	return &Checker{
		currentVersion: opts.CurrentVersion,
		feedURL:        opts.FeedURL,
		pollEvery:      opts.PollInterval,
		httpTimeout:    opts.HTTPTimeout,
	}
}

// Start spawns the polling goroutine. No-op when FeedURL is empty.
func (c *Checker) Start() {
	if c.feedURL == "" {
		return
	}
	if c.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	go c.loop(ctx)
}

// Stop signals the poll loop to exit and waits for it.
func (c *Checker) Stop() {
	if c.cancel == nil {
		return
	}
	c.cancel()
	<-c.done
	c.cancel = nil
}

// Latest returns a CheckResult reflecting the cached feed snapshot.
// Safe to call before the first poll completes — both feed and time
// fields will be zero.
func (c *Checker) Latest() CheckResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r := CheckResult{
		CurrentVersion: c.currentVersion,
	}
	if !c.checkedAt.IsZero() {
		r.CheckedAt = c.checkedAt.UTC().Format(time.RFC3339)
	}
	if c.feed != nil {
		r.AvailableVersion = c.feed.Version
		r.ReleaseNotesURL = c.feed.ReleaseNotesURL
		r.PublishedAt = c.feed.PublishedAt
		r.UpgradeAvailable = isNewer(c.feed.Version, c.currentVersion)
		r.Assets = c.feed.Assets
	}
	return r
}

func (c *Checker) loop(ctx context.Context) {
	defer close(c.done)
	// First poll immediately so the API serves real data within seconds
	// of boot rather than waiting a full pollEvery interval.
	c.pollOnce(ctx)
	t := time.NewTicker(c.pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.pollOnce(ctx)
		}
	}
}

func (c *Checker) pollOnce(ctx context.Context) {
	client := &http.Client{Timeout: c.httpTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.feedURL, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return
	}
	var f Feed
	if err := json.Unmarshal(body, &f); err != nil {
		return
	}
	c.mu.Lock()
	c.feed = &f
	c.checkedAt = time.Now()
	c.mu.Unlock()
}

// isNewer reports whether candidate > current using a relaxed-semver
// comparison. Both inputs may carry an optional leading "v".
//
// We intentionally avoid pulling golang.org/x/mod/semver to keep the
// import surface small for M5.6 v1; the comparison is good enough for
// skoed's normal release cadence (X.Y.Z).
func isNewer(candidate, current string) bool {
	c := splitVersion(candidate)
	r := splitVersion(current)
	for i := 0; i < 3; i++ {
		if c[i] != r[i] {
			return c[i] > r[i]
		}
	}
	return false
}

// splitVersion turns "v1.2.3" / "1.2.3-rc1" into [1,2,3]. Pre-release
// suffixes are stripped; treating "1.2.3-rc1" == "1.2.3" is fine for
// M5.6 v1 — channel support comes later.
func splitVersion(s string) [3]int {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.SplitN(s, ".", 3)
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		fmt.Sscanf(parts[i], "%d", &out[i])
	}
	return out
}
