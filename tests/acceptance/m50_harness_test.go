// M50 harness: shared helpers for the blocklist content distribution
// acceptance tests (specs/functional/blocklist-content-distribution.feature,
// specs/technical/blocklist-content-distribution.md §5.3, §8, §13).
//
// The skoed features behind these endpoints and fields come after the tests,
// so every decoder treats a missing JSON field as its zero value.
package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// cutUID is the uid a follower runs under when a test cuts its access to the
// leader's API with iptables (nobody on Debian and Alpine).
const cutUID = 65534

// ---------------------------------------------------------------------------
// List server
// ---------------------------------------------------------------------------

// skoedUserAgent matches "skoed/<version> (node <node_id>)", the User-Agent
// that `filter.Download` sends (technical spec §13).
var skoedUserAgent = regexp.MustCompile(`^skoed/\S+ \(node ([^)]+)\)`)

// ListServer serves a newline-separated domain list (format "domainlist") and
// counts requests per skoed node, parsed from the User-Agent header.
type ListServer struct {
	URL string // full URL of the list

	mu       sync.Mutex
	domains  []string
	status   int
	requests map[string]int
}

// startListServer starts an httptest server that serves domains at URL. The
// server is closed in t.Cleanup.
func startListServer(t *testing.T, domains []string) *ListServer {
	t.Helper()
	s := &ListServer{requests: map[string]int{}}
	s.SetDomains(domains)
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL + "/list.txt"
	return s
}

func (s *ListServer) serve(w http.ResponseWriter, r *http.Request) {
	nodeID := ""
	if m := skoedUserAgent.FindStringSubmatch(r.UserAgent()); m != nil {
		nodeID = m[1]
	}
	s.mu.Lock()
	s.requests[nodeID]++
	status := s.status
	var body []byte
	if len(s.domains) > 0 {
		body = []byte(strings.Join(s.domains, "\n") + "\n")
	}
	s.mu.Unlock()

	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Write(body) //nolint:errcheck
}

// SetDomains replaces the list served from the next request on.
func (s *ListServer) SetDomains(domains []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.domains = append([]string(nil), domains...)
}

// SetStatus makes the server reply with this HTTP status instead of the list.
// 0 serves the list normally.
func (s *ListServer) SetStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

// RequestsByNode returns a copy of the request counters, keyed by node ID.
// Requests with an unparseable User-Agent are counted under "".
func (s *ListServer) RequestsByNode() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.requests))
	for k, v := range s.requests {
		out[k] = v
	}
	return out
}

// Reset sets every request counter back to zero.
func (s *ListServer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = map[string]int{}
}

// genDomains returns n unique names "<prefix>-<6 digits>.example.test".
func genDomains(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%06d.example.test", prefix, i)
	}
	return out
}

// ---------------------------------------------------------------------------
// Blocklist API
// ---------------------------------------------------------------------------

// BlocklistView is GET /api/v1/blocklists/{id} as seen by one node.
type BlocklistView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Source  struct {
		Type   string `json:"type"`
		URL    string `json:"url"`
		Format string `json:"format"`
	} `json:"source"`
	DomainCount            int    `json:"domain_count"`
	LastUpdated            string `json:"last_updated"`
	RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
	LastRefreshAt          string `json:"last_refresh_at"`
	LastRefreshStatus      string `json:"last_refresh_status"`
	LastRefreshError       string `json:"last_refresh_error"`
	ContentSHA256          string `json:"content_sha256"`
	LocalContentSHA256     string `json:"local_content_sha256"`
	ContentStatus          string `json:"content_status"`
}

// getBlocklist reads one blocklist from n. It returns false on 404, on any
// other non-200 status, on a transport error or on an undecodable body. It
// never fails the test, so it can be polled while a node is unreachable.
func getBlocklist(t *testing.T, n *ClusterNode, id string) (BlocklistView, bool) {
	t.Helper()
	resp, err := n.apiDoNonFatal(http.MethodGet, "/api/v1/blocklists/"+id, "")
	if err != nil {
		return BlocklistView{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return BlocklistView{}, false
	}
	var v BlocklistView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return BlocklistView{}, false
	}
	return v, true
}

// createURLBlocklist posts an enabled URL blocklist in domainlist format. The
// caller checks the status and closes the body.
func createURLBlocklist(t *testing.T, via *ClusterNode, id, url string, intervalSeconds int) *http.Response {
	t.Helper()
	type source struct {
		Type   string `json:"type"`
		URL    string `json:"url"`
		Format string `json:"format"`
	}
	body := struct {
		ID                     string `json:"id"`
		Name                   string `json:"name"`
		Enabled                bool   `json:"enabled"`
		Source                 source `json:"source"`
		RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
	}{
		ID:                     id,
		Name:                   "Test " + id,
		Enabled:                true,
		Source:                 source{Type: "url", URL: url, Format: "domainlist"},
		RefreshIntervalSeconds: intervalSeconds,
	}
	return via.apiDo(t, http.MethodPost, "/api/v1/blocklists", mustJSON(t, body))
}

// refreshBlocklist triggers a manual refresh. The caller checks the status
// and closes the body.
func refreshBlocklist(t *testing.T, via *ClusterNode, id string) *http.Response {
	t.Helper()
	return via.apiDo(t, http.MethodPost, "/api/v1/blocklists/"+id+"/refresh", "")
}

// exportConfigText returns the text of every file in the archive that
// GET /api/v1/config/export returns (a tar.gz), concatenated in archive
// order, so a test can check what the export contains with strings.Contains.
//
// The M50 contract named this helper `exportConfig`, but config_test.go
// already defines `exportConfig(t, *Node) []byte` (the raw archive), so the
// name is taken.
func exportConfigText(t *testing.T, n *ClusterNode) string {
	t.Helper()
	archive := exportConfig(t, n.Node)
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("export from %s: not gzip: %v", n.NodeID, err)
	}
	defer gz.Close()
	var out strings.Builder
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("export from %s: read tar: %v", n.NodeID, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		fmt.Fprintf(&out, "# %s\n", hdr.Name)
		if _, err := io.Copy(&out, tr); err != nil {
			t.Fatalf("export from %s: read %s: %v", n.NodeID, hdr.Name, err)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// ---------------------------------------------------------------------------
// DNS and polling
// ---------------------------------------------------------------------------

// isBlocked sends an A query for domain to n and reports whether the answer
// is a block answer: NXDOMAIN, an A record 0.0.0.0, or NOERROR without any
// answer (NODATA). A timeout or transport error counts as not blocked.
func isBlocked(t *testing.T, n *ClusterNode, domain string) bool {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: dnsQueryTimeout}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	r, _, err := c.Exchange(m, n.DNSAddr)
	if err != nil || r == nil {
		return false
	}
	switch r.Rcode {
	case dns.RcodeNameError:
		return true
	case dns.RcodeSuccess:
		if len(r.Answer) == 0 {
			return true
		}
		for _, rr := range r.Answer {
			if a, ok := rr.(*dns.A); ok && a.A.IsUnspecified() {
				return true
			}
		}
	}
	return false
}

// waitFor calls cond every 200ms until it returns true, and fails the test
// with the given message if that does not happen within the given duration.
func waitFor(t *testing.T, within time.Duration, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s: %s", within, fmt.Sprintf(format, args...))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Raft log indexes
// ---------------------------------------------------------------------------

// RaftIndexes is the "raft" object of GET /api/v1/cluster/status on the
// answering node (technical spec §8).
type RaftIndexes struct {
	First    int `json:"first_log_index"`
	Last     int `json:"last_log_index"`
	Snapshot int `json:"last_snapshot_index"`
}

// raftIndexes returns the raft log indexes n reports for itself. It returns
// false when the node does not answer, answers with a non-200 status, or the
// status has no "raft" object.
func raftIndexes(t *testing.T, n *ClusterNode) (RaftIndexes, bool) {
	t.Helper()
	resp, err := n.apiDoNonFatal(http.MethodGet, "/api/v1/cluster/status", "")
	if err != nil {
		return RaftIndexes{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RaftIndexes{}, false
	}
	var s struct {
		Raft *RaftIndexes `json:"raft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil || s.Raft == nil {
		return RaftIndexes{}, false
	}
	return *s.Raft, true
}

// ---------------------------------------------------------------------------
// Network cut with iptables
// ---------------------------------------------------------------------------

// requireIptables skips the test unless it runs as root and iptables works.
// run-in-docker.sh provides both (--cap-add NET_ADMIN, apk add iptables).
func requireIptables(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skipf("network cut needs root to run iptables (euid %d); run the suite with tests/acceptance/run-in-docker.sh", os.Geteuid())
	}
	// Check a rule of the same shape as blockUIDToPort's: `iptables -C` loads
	// the owner match and the REJECT target, so a kernel or iptables build
	// without them is caught here. Exit status 1 means the rule is absent and
	// both modules work; any other failure means iptables cannot cut traffic.
	probe := []string{
		"-C", "OUTPUT", "-m", "owner", "--uid-owner", "65534",
		"-p", "tcp", "--dport", "1", "-j", "REJECT", "--reject-with", "tcp-reset",
	}
	out, err := exec.Command("iptables", probe...).CombinedOutput()
	var exitErr *exec.ExitError
	if err == nil || (errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
		return
	}
	t.Skipf("network cut needs a working iptables with the owner match and the REJECT target (NET_ADMIN capability and the iptables package): `iptables %s` failed: %v: %s",
		strings.Join(probe, " "), err, strings.TrimSpace(string(out)))
}

// blockUIDToPort rejects TCP connections from processes running as uid to
// 127.0.0.1:port. The returned restore function deletes the rule; it is also
// registered in t.Cleanup and only deletes the rule once.
func blockUIDToPort(t *testing.T, uid, port int) (restore func()) {
	t.Helper()
	rule := []string{
		"OUTPUT", "-m", "owner", "--uid-owner", strconv.Itoa(uid),
		"-p", "tcp", "-d", "127.0.0.1", "--dport", strconv.Itoa(port),
		"-j", "REJECT", "--reject-with", "tcp-reset",
	}
	if out, err := exec.Command("iptables", append([]string{"-A"}, rule...)...).CombinedOutput(); err != nil {
		t.Fatalf("iptables -A %s: %v: %s", strings.Join(rule, " "), err, strings.TrimSpace(string(out)))
	}
	var once sync.Once
	restore = func() {
		once.Do(func() {
			if out, err := exec.Command("iptables", append([]string{"-D"}, rule...)...).CombinedOutput(); err != nil {
				t.Errorf("iptables -D %s: %v: %s", strings.Join(rule, " "), err, strings.TrimSpace(string(out)))
			}
		})
	}
	t.Cleanup(restore)
	return restore
}
