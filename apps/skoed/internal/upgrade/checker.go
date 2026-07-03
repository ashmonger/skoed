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
	// Checksums maps each asset key (e.g. "linux_amd64") to the hex SHA-256 of
	// its tar.gz. Required for the upgrade to proceed — Swap refuses an asset
	// without a checksum. Sourced from the goreleaser checksums.txt on the
	// GitHub release (ChecksumsURL) or inline in a custom feed. Authenticity
	// rests on the GitHub-hosted build + HTTPS transport; the checksum protects
	// integrity of the downloaded artifact.
	Checksums map[string]string `json:"checksums,omitempty"`
	// ChecksumsURL points at a goreleaser checksums.txt. When set, it overrides
	// any inline Checksums.
	ChecksumsURL string `json:"checksums_url,omitempty"`
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
	Checksums        map[string]string `json:"checksums,omitempty"`
}

// Checker polls the release feed on a fixed interval and caches the
// latest result in memory.
type Checker struct {
	currentVersion string
	feedURL        string
	githubRepo     string // non-empty → use GitHub releases API instead of feedURL
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
	FeedURL        string        // explicit custom feed; takes priority over GithubRepo
	GithubRepo     string        // "owner/repo" — auto-check GitHub releases when FeedURL is empty
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
		githubRepo:     opts.GithubRepo,
		pollEvery:      opts.PollInterval,
		httpTimeout:    opts.HTTPTimeout,
	}
}

// Start spawns the polling goroutine. No-op when neither FeedURL nor GithubRepo is set.
func (c *Checker) Start() {
	if c.feedURL == "" && c.githubRepo == "" {
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

// Refresh triggers an immediate poll, blocking until it completes, then
// returns the updated result. Used by the manual "Check for updates" button.
func (c *Checker) Refresh() CheckResult {
	c.pollOnce(context.Background())
	return c.Latest()
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
		r.Checksums = c.feed.Checksums
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
	var f *Feed
	var err error
	if c.githubRepo != "" && c.feedURL == "" {
		f, err = fetchGitHubRelease(ctx, c.githubRepo, c.httpTimeout)
	} else {
		f, err = fetchFeed(ctx, c.feedURL, c.httpTimeout)
	}
	if err != nil || f == nil {
		return
	}
	c.mu.Lock()
	c.feed = f
	c.checkedAt = time.Now()
	c.mu.Unlock()
}

func fetchFeed(ctx context.Context, feedURL string, timeout time.Duration) (*Feed, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	var f Feed
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, err
	}
	// A checksums.txt URL (goreleaser) overrides any inline checksums.
	if f.ChecksumsURL != "" {
		f.Checksums = loadChecksums(ctx, f.ChecksumsURL, timeout)
	}
	return &f, nil
}

// githubRelease is the subset of the GitHub releases API response we care about.
type githubRelease struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchGitHubRelease queries the GitHub releases API and converts the response
// to a Feed. Asset keys follow the goreleaser convention: "linux_amd64", etc.
func fetchGitHubRelease(ctx context.Context, repo string, timeout time.Duration) (*Feed, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	f := &Feed{
		Version:         strings.TrimPrefix(rel.TagName, "v"),
		PublishedAt:     rel.PublishedAt,
		ReleaseNotesURL: rel.HTMLURL,
		Assets:          make(map[string]string),
		Checksums:       make(map[string]string),
	}
	// Map goreleaser asset names to AssetKey() keys ("linux_amd64", etc.).
	// goreleaser names: "skoed_0.2.6_linux_amd64.tar.gz" → "linux_amd64"
	// Strip the extension, then drop leading segments that aren't os_arch pairs.
	var checksumsURL string
	for _, a := range rel.Assets {
		if a.Name == "checksums.txt" || strings.HasSuffix(a.Name, "_checksums.txt") {
			checksumsURL = a.BrowserDownloadURL
			continue
		}
		key := assetKeyFromName(a.Name)
		if key != "" {
			f.Assets[key] = a.BrowserDownloadURL
		}
	}
	f.ChecksumsURL = checksumsURL
	if checksumsURL != "" {
		f.Checksums = loadChecksums(ctx, checksumsURL, timeout)
	}
	return f, nil
}

// loadChecksums downloads a goreleaser checksums.txt and returns a map of asset
// key ("linux_amd64") → hex SHA-256. Returns nil on any error, in which case
// the caller has no checksum and Swap refuses the upgrade.
func loadChecksums(ctx context.Context, checksumsURL string, timeout time.Duration) map[string]string {
	checksums := fetchBytes(ctx, checksumsURL, timeout, 256*1024)
	if checksums == nil {
		return nil
	}
	out := make(map[string]string)
	for _, line := range strings.Split(string(checksums), "\n") {
		// Format: "<hex sha256>  <filename>"
		fields := strings.Fields(line)
		if len(fields) == 2 {
			if key := assetKeyFromName(fields[1]); key != "" {
				out[key] = fields[0]
			}
		}
	}
	return out
}

// fetchBytes GETs url and returns up to limit bytes, or nil on any error.
func fetchBytes(ctx context.Context, url string, timeout time.Duration, limit int64) []byte {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil
	}
	return body
}

// assetKeyFromName maps a goreleaser asset filename to the AssetKey() key
// used by the upgrade handler (e.g. "linux_amd64"). It handles both:
//   - "skoed_0.2.6_linux_amd64.tar.gz"  →  "linux_amd64"
//   - "skoed_linux_amd64.tar.gz"         →  "linux_amd64"
//
// Only tar.gz archives are considered; other files (checksums, zip) are
// skipped (empty string returned).
func assetKeyFromName(filename string) string {
	if !strings.HasSuffix(filename, ".tar.gz") {
		return ""
	}
	name := strings.TrimSuffix(filename, ".tar.gz")
	parts := strings.Split(name, "_")
	// Walk from the end looking for a two-segment "os_arch" tail.
	knownOS := map[string]bool{"linux": true, "darwin": true, "windows": true}
	for i := len(parts) - 2; i >= 0; i-- {
		if knownOS[parts[i]] {
			return strings.Join(parts[i:], "_")
		}
	}
	return ""
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
