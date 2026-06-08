package dohresolvers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// fetchPayload is the shape we expect from the curated upstream feed:
// either a top-level array of resolver entries, or an object with a
// "resolvers" key containing the array. Both forms are accepted.
type fetchPayload struct {
	Resolvers []ResolverEntry `json:"resolvers"`
}

// maxFetchBody is the upper bound on accepted upstream response size.
// 1 MiB is generous; the curated catalog is on the order of a few KiB.
const maxFetchBody = 1 << 20

var idRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// fetchAndValidate issues a single HTTP GET against url with the given
// timeout, parses the body, and validates every entry. Returns the
// parsed entries plus the body bytes (so callers can fingerprint the
// snapshot deterministically) or an error suitable for surfacing in
// last_refresh_error.
func fetchAndValidate(url string, timeout time.Duration) ([]ResolverEntry, []byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, nil, fmt.Errorf("upstream: %v", trimErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read: %v", trimErr(err))
	}
	if len(body) > maxFetchBody {
		return nil, nil, fmt.Errorf("upstream body too large (>%d bytes)", maxFetchBody)
	}

	entries, err := parsePayload(body)
	if err != nil {
		return nil, body, err
	}
	if err := validateEntries(entries); err != nil {
		return nil, body, err
	}
	return entries, body, nil
}

// parsePayload accepts either the wrapped or the array form. Strips
// any extraneous keys silently.
func parsePayload(body []byte) ([]ResolverEntry, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var arr []ResolverEntry
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, fmt.Errorf("parse: %v", err)
		}
		return arr, nil
	}
	var wrapped fetchPayload
	if err := json.Unmarshal([]byte(trimmed), &wrapped); err != nil {
		return nil, fmt.Errorf("parse: %v", err)
	}
	if wrapped.Resolvers == nil {
		return nil, fmt.Errorf("parse: missing 'resolvers' key")
	}
	return wrapped.Resolvers, nil
}

// validateEntries enforces the schema described in TS-DohResolverDb
// §"Refresh cycle". Returns the first violation.
func validateEntries(entries []ResolverEntry) error {
	if len(entries) == 0 {
		return fmt.Errorf("validate: empty resolvers list")
	}
	seen := map[string]struct{}{}
	for i, e := range entries {
		if !idRegex.MatchString(e.ID) {
			return fmt.Errorf("validate: entry %d: invalid id %q", i, e.ID)
		}
		if _, dup := seen[e.ID]; dup {
			return fmt.Errorf("validate: duplicate id %q", e.ID)
		}
		seen[e.ID] = struct{}{}
		if n := len(strings.TrimSpace(e.Name)); n == 0 || n > 64 {
			return fmt.Errorf("validate: entry %s: name length %d out of range", e.ID, n)
		}
		for _, v4 := range e.IPv4 {
			addr, err := netip.ParseAddr(v4)
			if err != nil || !addr.Is4() {
				return fmt.Errorf("validate: entry %s: ipv4 %q invalid", e.ID, v4)
			}
		}
		for _, v6 := range e.IPv6 {
			addr, err := netip.ParseAddr(v6)
			if err != nil || addr.Is4() {
				return fmt.Errorf("validate: entry %s: ipv6 %q invalid", e.ID, v6)
			}
		}
		if len(e.IPv4) == 0 && len(e.IPv6) == 0 {
			return fmt.Errorf("validate: entry %s: ipv4 and ipv6 both empty", e.ID)
		}
	}
	return nil
}

// snapshotIDFor builds the canonical `<fetched_at>-<sha256(body)[:8]>`
// snapshot id described in TS-DohResolverDb §"Refresh cycle".
func snapshotIDFor(fetchedAt string, body []byte) string {
	sum := sha256.Sum256(body)
	return fetchedAt + "-" + hex.EncodeToString(sum[:])[:8]
}

// trimErr keeps an upstream error short enough for the
// last_refresh_error field.
func trimErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
