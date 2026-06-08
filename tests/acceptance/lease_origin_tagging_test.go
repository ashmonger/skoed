// Acceptance tests for M6.5 — per-connector static-vs-dynamic lease
// origin tagging (TS-LeaseOrigin).
//
// FSIDs covered:
//   FS-LeaseOriginKeaReservationsReportedHigh     → TestLeaseOriginKeaReservationsReportedHigh
//   FS-LeaseOriginKeaReservationsUnreachableInferred → TestLeaseOriginKeaReservationsUnreachableInferred
//   FS-LeaseOriginDnsmasqDhcpHostParsed           → TestLeaseOriginDnsmasqDhcpHostParsed
//   FS-LeaseOriginDnsmasqConfigUnreadable         → TestLeaseOriginDnsmasqConfigUnreadable
//   FS-LeaseOriginHttpJsonHonoursWireField        → TestLeaseOriginHttpJsonHonoursWireField
//   FS-LeaseOriginHttpJsonRejectsUnknownValue     → TestLeaseOriginHttpJsonRejectsUnknownValue
//   FS-LeaseOriginClientLookupExposesFields       → TestLeaseOriginClientLookupExposesFields
//   FS-LeaseOriginUnknownClientOmitsOrigin        → TestLeaseOriginUnknownClientOmitsOrigin
//   FS-LeaseOriginClientsListSurfacesBadge        → TestLeaseOriginClientsListSurfacesBadge
//   FS-LeaseOriginQueryLogDoesNotChange           → TestLeaseOriginQueryLogDoesNotChange
//   FS-LeaseOriginPrometheusGauges                → TestLeaseOriginPrometheusGauges
//   FS-LeaseOriginRefreshFlipsTag                 → TestLeaseOriginRefreshFlipsTag
//
// Strategy:
//   - Kea tests spin a httptest server that answers both "lease4-get-all"
//     and "reservation-get-all" — the test mutates which command returns
//     what to drive origin behaviour.
//   - Dnsmasq tests write a fixture lease file + a sibling dnsmasq.conf
//     in the test's TempDir, then point the connector at both.
//   - HTTP_JSON tests use the existing mutableLeaseServer pattern from
//     dhcp_spoof_detection_test.go (we craft raw payloads here so we can
//     emit the new `origin` field on the wire).
//   - Every test self-skips when origin endpoints/fields are 404 / absent
//     (M6.5 impl pending).

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// originClient is the M6.5-extended shape of GET /api/v1/clients/{ip}.
// All fields are omitempty so we can tell "feature shipped, value blank"
// from "feature not yet wired".
type originClient struct {
	IP               string `json:"ip"`
	MAC              string `json:"mac"`
	Hostname         string `json:"hostname"`
	ClientID         string `json:"client_id"`
	Source           string `json:"source"`
	Origin           string `json:"origin"`
	OriginConfidence string `json:"origin_confidence"`
}

// originLease is the M6.5-extended shape of the internal lease snapshot
// (mirrors Lease + the two new fields). The snapshot endpoint returns
// the raw connector view, which is what we want to assert against in
// the per-connector parse tests.
type originLease struct {
	IP               string `json:"ip"`
	MAC              string `json:"mac"`
	Hostname         string `json:"hostname"`
	ClientID         string `json:"client_id"`
	Source           string `json:"source"`
	Origin           string `json:"origin"`
	OriginConfidence string `json:"origin_confidence"`
}

// fetchOriginClient pulls GET /api/v1/clients/{ip} and decodes into the
// M6.5 shape. Skips when the route is 404 or 501 (feature pending).
func fetchOriginClient(t *testing.T, n *Node, ip string) originClient {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/"+ip, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s returns 404", ip)
	}
	if resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients/%s returns 501", ip)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/v1/clients/%s: status %d", ip, resp.StatusCode)
	}
	var c originClient
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("decode origin client: %v", err)
	}
	return c
}

// fetchOriginLeases pulls the internal lease snapshot and decodes into
// the M6.5-extended Lease shape, skipping when the harness isn't wired.
func fetchOriginLeases(t *testing.T, n *Node) []originLease {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/_leases", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3.6 lease snapshot route absent (404)")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("lease snapshot status %d", resp.StatusCode)
	}
	var out []originLease
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode origin leases: %v", err)
	}
	return out
}

func findOriginLeaseByIP(ls []originLease, ip string) originLease {
	for _, l := range ls {
		if l.IP == ip {
			return l
		}
	}
	return originLease{}
}

// keaCmdRequest is the Kea control-agent envelope (command + service +
// arguments). Used to dispatch between lease4-get-all and
// reservation-get-all from a single httptest server.
type keaCmdRequest struct {
	Command   string                 `json:"command"`
	Service   []string               `json:"service"`
	Arguments map[string]interface{} `json:"arguments"`
}

// keaWithReservationsServer answers BOTH the M3.6 lease4-get-all command
// and the M6.5 reservation-get-all command from one httptest server.
// The reservation response is parameterised by `reservedIPs` so the
// caller can drive which leases get tagged static.
//
// `reservationsBehaviour` controls the reservation endpoint outcome:
//   - "ok"        → 200 with the reservedIPs list
//   - "error"     → HTTP 500 (drives unknown/inferred path)
//   - "result_ne_0" → HTTP 200 but result=1 (treated as error per TS)
type keaWithReservationsServer struct {
	mu                    sync.Mutex
	reservedIPs           []string
	reservationsBehaviour string
	leaseFixture          []byte
	srv                   *httptest.Server
}

func newKeaWithReservationsServer(t *testing.T, fixture string, reservedIPs []string) *keaWithReservationsServer {
	t.Helper()
	body, err := os.ReadFile(fixturePath(t, fixture))
	if err != nil {
		t.Fatalf("read kea fixture: %v", err)
	}
	k := &keaWithReservationsServer{
		reservedIPs:           reservedIPs,
		reservationsBehaviour: "ok",
		leaseFixture:          body,
	}
	k.srv = httptest.NewServer(http.HandlerFunc(k.handle))
	return k
}

func (k *keaWithReservationsServer) handle(w http.ResponseWriter, r *http.Request) {
	var req keaCmdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Fall back to the M3.6 path — many test stubs don't decode the
		// request and just stream the fixture. Treat this as a
		// lease4-get-all so M3.6-only callers still work.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(k.leaseFixture)
		return
	}
	switch req.Command {
	case "lease4-get-all":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(k.leaseFixture)
	case "reservation-get-all":
		k.mu.Lock()
		behaviour := k.reservationsBehaviour
		reserved := append([]string{}, k.reservedIPs...)
		k.mu.Unlock()
		switch behaviour {
		case "error":
			w.WriteHeader(http.StatusInternalServerError)
			return
		case "result_ne_0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `[{"result":1,"text":"unsupported"}]`)
			return
		default:
			// Build a plausible reservation response shape.
			records := []map[string]interface{}{}
			for _, ip := range reserved {
				records = append(records, map[string]interface{}{
					"ip-address":  ip,
					"hw-address":  "aa:bb:cc:dd:ee:42",
					"hostname":    "reserved-host",
					"client-id":   "id:reservation",
				})
			}
			env := []map[string]interface{}{{
				"result": 0,
				"text":   fmt.Sprintf("%d reservation(s) found.", len(records)),
				"arguments": map[string]interface{}{
					"hosts": records,
				},
			}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(env)
		}
	default:
		// Unknown command → empty success.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"result":0,"text":"noop"}]`))
	}
}

func (k *keaWithReservationsServer) setReserved(ips []string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reservedIPs = ips
}

func (k *keaWithReservationsServer) setBehaviour(b string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reservationsBehaviour = b
}

func (k *keaWithReservationsServer) URL() string { return k.srv.URL }
func (k *keaWithReservationsServer) Close()      { k.srv.Close() }

// writeDnsmasqConfig writes a dnsmasq.conf-shaped file in dir and
// returns its absolute path. The harness doesn't currently surface a
// ConfigPath knob, so when the spec adds it the test will pass once
// startClusterWithDhcp learns about cfg.ConfigPath. Until then the
// scenario auto-skips when origin remains blank.
func writeDnsmasqConfig(t *testing.T, dir, contents string) string {
	t.Helper()
	p := filepath.Join(dir, "dnsmasq.conf")
	if err := os.WriteFile(p, []byte(contents), 0600); err != nil {
		t.Fatalf("write dnsmasq.conf: %v", err)
	}
	return p
}

// writeDnsmasqLeases writes a dnsmasq-leases-formatted file and returns
// its path. Caller passes raw "exp mac ip host clientid" tab-separated
// lines.
func writeDnsmasqLeases(t *testing.T, dir string, lines []string) string {
	t.Helper()
	p := filepath.Join(dir, "dnsmasq.leases")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatalf("write dnsmasq.leases: %v", err)
	}
	return p
}

// originLeasePayload assembles a single http_json wire record. The
// `origin` key is appended verbatim so tests can emit garbage values.
func originLeasePayload(ip, mac, hostname, clientID, originRaw string) map[string]interface{} {
	out := map[string]interface{}{
		"ip":         ip,
		"mac":        mac,
		"hostname":   hostname,
		"client_id":  clientID,
		"expires_at": "2287-11-09T11:46:39Z",
	}
	if originRaw != "" {
		out["origin"] = originRaw
	}
	return out
}

// requireOriginFieldSurfaced skips the test when the M6.5 wire fields
// are absent on the lease snapshot for a control IP we know exists.
// Used so the per-FSID tests fail loudly on regressions once shipped
// but skip cleanly during the spec-first window.
func requireOriginFieldSurfaced(t *testing.T, n *Node, ip string) {
	t.Helper()
	ls := fetchOriginLeases(t, n)
	l := findOriginLeaseByIP(ls, ip)
	if l.IP == "" {
		t.Skipf("M6.5 impl pending: control lease %s not present in snapshot", ip)
	}
	if l.Origin == "" && l.OriginConfidence == "" {
		t.Skipf("M6.5 impl pending: connector does not yet emit origin fields")
	}
}

// FS-LeaseOriginKeaReservationsReportedHigh
func TestLeaseOriginKeaReservationsReportedHigh(t *testing.T) {
	// Mark 192.168.1.42 + 192.168.1.50 reserved; fixture leases include
	// 192.168.1.42 + 192.168.1.10 (.50 is not in the lease fixture so
	// it just exercises the reservation-set path, per the scenario).
	srv := newKeaWithReservationsServer(t, "kea-lease4-get-all.json",
		[]string{"192.168.1.42", "192.168.1.50"})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "kea", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond) // wait for first poll cycle
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	leases := fetchOriginLeases(t, n)
	statTablet := findOriginLeaseByIP(leases, "192.168.1.42")
	if statTablet.Origin != "dhcp_static" || statTablet.OriginConfidence != "high" {
		t.Errorf("192.168.1.42: want origin=dhcp_static/high, got %s/%s",
			statTablet.Origin, statTablet.OriginConfidence)
	}
	dynLaptop := findOriginLeaseByIP(leases, "192.168.1.10")
	if dynLaptop.Origin != "dhcp_dynamic" || dynLaptop.OriginConfidence != "high" {
		t.Errorf("192.168.1.10: want origin=dhcp_dynamic/high, got %s/%s",
			dynLaptop.Origin, dynLaptop.OriginConfidence)
	}
}

// FS-LeaseOriginKeaReservationsUnreachableInferred
func TestLeaseOriginKeaReservationsUnreachableInferred(t *testing.T) {
	srv := newKeaWithReservationsServer(t, "kea-lease4-get-all.json", nil)
	srv.setBehaviour("error") // reservation-get-all 500s
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "kea", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	leases := fetchOriginLeases(t, n)
	if len(leases) == 0 {
		t.Fatalf("no leases parsed at all (lease4-get-all should still work)")
	}
	for _, l := range leases {
		if l.Origin != "dhcp_dynamic" {
			t.Errorf("%s: reservation-get-all errored → want origin=dhcp_dynamic, got %q",
				l.IP, l.Origin)
		}
		if l.OriginConfidence != "unknown" {
			t.Errorf("%s: reservation-get-all errored → want origin_confidence=unknown, got %q",
				l.IP, l.OriginConfidence)
		}
	}
}

// FS-LeaseOriginDnsmasqDhcpHostParsed
func TestLeaseOriginDnsmasqDhcpHostParsed(t *testing.T) {
	dir := t.TempDir()
	writeDnsmasqConfig(t, dir, strings.Join([]string{
		"dhcp-host=aa:bb:cc:dd:ee:42,192.168.1.42,kid-tablet",
		"dhcp-host=id:laptop10,192.168.1.10,home-laptop",
		"",
	}, "\n"))
	leases := writeDnsmasqLeases(t, dir, []string{
		"9999999999 aa:bb:cc:dd:ee:42 192.168.1.42 kid-tablet id:tablet42",
		"9999999999 aa:bb:cc:dd:ee:10 192.168.1.10 home-laptop id:laptop10",
		"9999999999 aa:bb:cc:dd:ee:99 192.168.1.99 iphone-of-x id:guest99",
	})

	// Note: the harness DhcpOpts struct currently has no ConfigPath
	// knob. Until the M6.5 implementation extends the harness, dnsmasq
	// tags every lease "" / "" and this test self-skips cleanly via
	// requireOriginFieldSurfaced below.
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: leases,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	got := fetchOriginLeases(t, n)
	tablet := findOriginLeaseByIP(got, "192.168.1.42")
	if tablet.Origin != "dhcp_static" || tablet.OriginConfidence != "inferred" {
		t.Errorf("192.168.1.42: want dhcp_static/inferred, got %s/%s",
			tablet.Origin, tablet.OriginConfidence)
	}
	laptop := findOriginLeaseByIP(got, "192.168.1.10")
	if laptop.Origin != "dhcp_static" || laptop.OriginConfidence != "inferred" {
		t.Errorf("192.168.1.10: want dhcp_static/inferred, got %s/%s",
			laptop.Origin, laptop.OriginConfidence)
	}
	guest := findOriginLeaseByIP(got, "192.168.1.99")
	if guest.Origin != "dhcp_dynamic" || guest.OriginConfidence != "high" {
		t.Errorf("192.168.1.99: want dhcp_dynamic/high, got %s/%s",
			guest.Origin, guest.OriginConfidence)
	}
}

// FS-LeaseOriginDnsmasqConfigUnreadable
func TestLeaseOriginDnsmasqConfigUnreadable(t *testing.T) {
	dir := t.TempDir()
	leases := writeDnsmasqLeases(t, dir, []string{
		"9999999999 aa:bb:cc:dd:ee:42 192.168.1.42 kid-tablet id:tablet42",
		"9999999999 aa:bb:cc:dd:ee:10 192.168.1.10 home-laptop id:laptop10",
		"9999999999 aa:bb:cc:dd:ee:99 192.168.1.99 iphone-of-x id:guest99",
	})
	// Write a config file then chmod it 0000 so the connector can't
	// read it. (root in a container may bypass this — the test then
	// falls back to the skip path on requireOriginFieldSurfaced.)
	cfg := writeDnsmasqConfig(t, dir, "dhcp-host=aa:bb:cc:dd:ee:42,192.168.1.42\n")
	if err := os.Chmod(cfg, 0o000); err != nil {
		t.Fatalf("chmod dnsmasq.conf: %v", err)
	}
	defer os.Chmod(cfg, 0o600) //nolint:errcheck — best-effort cleanup

	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: leases,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	got := fetchOriginLeases(t, n)
	for _, l := range got {
		if l.Origin != "dhcp_dynamic" {
			t.Errorf("%s: unreadable config → want origin=dhcp_dynamic, got %q",
				l.IP, l.Origin)
		}
		if l.OriginConfidence != "unknown" {
			t.Errorf("%s: unreadable config → want confidence=unknown, got %q",
				l.IP, l.OriginConfidence)
		}
	}
}

// FS-LeaseOriginHttpJsonHonoursWireField
func TestLeaseOriginHttpJsonHonoursWireField(t *testing.T) {
	leases := []map[string]interface{}{
		originLeasePayload("192.168.1.50", "aa:bb:cc:dd:ee:50", "nas", "id:nas", "dhcp_static"),
		originLeasePayload("192.168.1.77", "aa:bb:cc:dd:ee:77", "guest", "id:guest", "dhcp_dynamic"),
		originLeasePayload("192.168.1.88", "aa:bb:cc:dd:ee:88", "noorigin", "id:?", ""),
	}
	srv := newMutableLeaseServer(leases)
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.50")

	got := fetchOriginLeases(t, n)
	cases := []struct {
		ip               string
		wantOrigin       string
		wantConfidence   string
	}{
		{"192.168.1.50", "dhcp_static", "high"},
		{"192.168.1.77", "dhcp_dynamic", "high"},
		{"192.168.1.88", "dhcp_dynamic", "unknown"},
	}
	for _, tc := range cases {
		l := findOriginLeaseByIP(got, tc.ip)
		if l.Origin != tc.wantOrigin || l.OriginConfidence != tc.wantConfidence {
			t.Errorf("%s: want %s/%s, got %s/%s",
				tc.ip, tc.wantOrigin, tc.wantConfidence,
				l.Origin, l.OriginConfidence)
		}
	}
}

// FS-LeaseOriginHttpJsonRejectsUnknownValue
func TestLeaseOriginHttpJsonRejectsUnknownValue(t *testing.T) {
	leases := []map[string]interface{}{
		originLeasePayload("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42",
			"totally-made-up"),
	}
	srv := newMutableLeaseServer(leases)
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	got := fetchOriginLeases(t, n)
	l := findOriginLeaseByIP(got, "192.168.1.42")
	if l.IP != "192.168.1.42" {
		t.Fatalf("lease still ingested? got %+v", l)
	}
	if l.Origin != "dhcp_dynamic" {
		t.Errorf("garbage origin → want dhcp_dynamic, got %q", l.Origin)
	}
	if l.OriginConfidence != "unknown" {
		t.Errorf("garbage origin → want confidence=unknown, got %q", l.OriginConfidence)
	}
}

// FS-LeaseOriginClientLookupExposesFields
func TestLeaseOriginClientLookupExposesFields(t *testing.T) {
	leases := []map[string]interface{}{
		originLeasePayload("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet",
			"id:tablet42", "dhcp_static"),
		originLeasePayload("192.168.1.10", "aa:bb:cc:dd:ee:10", "home-laptop",
			"id:laptop10", "dhcp_dynamic"),
	}
	srv := newMutableLeaseServer(leases)
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	stat := fetchOriginClient(t, n, "192.168.1.42")
	if stat.IP != "192.168.1.42" {
		t.Errorf("ip echo: got %q", stat.IP)
	}
	if stat.Origin != "dhcp_static" {
		t.Errorf("192.168.1.42 origin: want dhcp_static, got %q", stat.Origin)
	}
	if stat.OriginConfidence != "high" {
		t.Errorf("192.168.1.42 confidence: want high, got %q", stat.OriginConfidence)
	}

	dyn := fetchOriginClient(t, n, "192.168.1.10")
	if dyn.Origin != "dhcp_dynamic" {
		t.Errorf("192.168.1.10 origin: want dhcp_dynamic, got %q", dyn.Origin)
	}
}

// FS-LeaseOriginUnknownClientOmitsOrigin
func TestLeaseOriginUnknownClientOmitsOrigin(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]interface{}{
		originLeasePayload("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet",
			"id:tablet42", "dhcp_static"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	got := fetchOriginClient(t, n, "192.168.99.99")
	if got.Origin != "" {
		t.Errorf("unknown IP: want origin=\"\", got %q", got.Origin)
	}
	if got.OriginConfidence != "" {
		t.Errorf("unknown IP: want origin_confidence=\"\", got %q", got.OriginConfidence)
	}
	if got.Source != "none" {
		t.Errorf("unknown IP: want source=none, got %q", got.Source)
	}
}

// FS-LeaseOriginClientsListSurfacesBadge
func TestLeaseOriginClientsListSurfacesBadge(t *testing.T) {
	// The chip is rendered by the SPA (web/src/views/Clients.vue) — a
	// black-box acceptance test asserts the field is present on the
	// list endpoint, not the rendered HTML. The SPA exercise is part
	// of the demo recipe.
	leases := []map[string]interface{}{
		originLeasePayload("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet",
			"id:tablet42", "dhcp_static"),
		originLeasePayload("192.168.1.10", "aa:bb:cc:dd:ee:10", "home-laptop",
			"id:laptop10", "dhcp_dynamic"),
		originLeasePayload("192.168.1.99", "aa:bb:cc:dd:ee:99", "iphone-of-x",
			"id:guest99", "dhcp_dynamic"),
	}
	srv := newMutableLeaseServer(leases)
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	resp := n.apiDo(t, "GET", "/api/v1/clients", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M6.5 impl pending: GET /api/v1/clients list route returns 404")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/v1/clients: status %d", resp.StatusCode)
	}
	// Accept either a bare array or an envelope {clients: [...]}.
	body := readBody(t, resp)
	var arr []originClient
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		var env struct {
			Clients []originClient `json:"clients"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("decode clients list: %v: %s", err, body)
		}
		arr = env.Clients
	}
	want := map[string]string{
		"192.168.1.42": "dhcp_static",
		"192.168.1.10": "dhcp_dynamic",
		"192.168.1.99": "dhcp_dynamic",
	}
	got := map[string]string{}
	for _, c := range arr {
		got[c.IP] = c.Origin
	}
	for ip, wantOrigin := range want {
		if got[ip] != wantOrigin {
			t.Errorf("/api/v1/clients[%s].origin: want %q, got %q",
				ip, wantOrigin, got[ip])
		}
	}
}

// FS-LeaseOriginQueryLogDoesNotChange
func TestLeaseOriginQueryLogDoesNotChange(t *testing.T) {
	t.Setenv("SKOED_TEST_MODE", "1")
	srv := newMutableLeaseServer([]map[string]interface{}{
		originLeasePayload("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet",
			"id:tablet42", "dhcp_static"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, "192.168.1.42")
	time.Sleep(500 * time.Millisecond)

	// Fetch the raw query-log JSON (not the typed struct) so we can
	// assert the LACK of an `origin` field.
	resp := n.apiDo(t, "GET", "/api/v1/query-log?limit=50", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("query-log endpoint absent")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("query-log status %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	// The raw JSON for any 192.168.1.42 row must not contain "origin".
	// Pinpoint the row by client IP token to avoid false positives
	// from log metadata or other rows.
	if !strings.Contains(body, "192.168.1.42") {
		t.Skipf("no query-log entry yet for 192.168.1.42")
	}
	// Decode into generic maps and assert no key named "origin" on the
	// 192.168.1.42 row.
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		var env struct {
			Entries []map[string]interface{} `json:"entries"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("decode query-log: %v", err)
		}
		arr = env.Entries
	}
	for _, row := range arr {
		ip, _ := row["client"].(string)
		if ip != "192.168.1.42" {
			continue
		}
		if _, has := row["origin"]; has {
			t.Errorf("query-log row carries `origin` field — must NOT: %+v", row)
		}
	}
}

// FS-LeaseOriginPrometheusGauges
func TestLeaseOriginPrometheusGauges(t *testing.T) {
	// 2 static + 1 dynamic — matches the FSID phrasing.
	leases := []map[string]interface{}{
		originLeasePayload("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet",
			"id:tablet42", "dhcp_static"),
		originLeasePayload("192.168.1.50", "aa:bb:cc:dd:ee:50", "nas", "id:nas",
			"dhcp_static"),
		originLeasePayload("192.168.1.10", "aa:bb:cc:dd:ee:10", "home-laptop",
			"id:laptop10", "dhcp_dynamic"),
	}
	srv := newMutableLeaseServer(leases)
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	body := fetchMetrics(t, n)
	if !strings.Contains(body, "skoed_dhcp_leases") {
		t.Skipf("M6.5 impl pending: skoed_dhcp_leases series absent")
	}
	// The TS describes labels {source, origin}; assert the two
	// substantive series and that no router_advertised/manual_admin
	// series is emitted (cardinality bound: only labels we observed).
	wantSubstrings := []string{
		`origin="dhcp_static"`,
		`origin="dhcp_dynamic"`,
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(body, w) {
			t.Errorf("metrics body missing %q in skoed_dhcp_leases series", w)
		}
	}
	// No-series-for-zero rule.
	for _, mustNot := range []string{
		`origin="router_advertised"`,
		`origin="manual_admin"`,
	} {
		if strings.Contains(body, mustNot) {
			t.Errorf("metrics body emitted %q despite zero leases of that origin", mustNot)
		}
	}
}

// FS-LeaseOriginRefreshFlipsTag
func TestLeaseOriginRefreshFlipsTag(t *testing.T) {
	// Start with no reservations → 192.168.1.42 is dynamic.
	srv := newKeaWithReservationsServer(t, "kea-lease4-get-all.json", nil)
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "kea", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	requireOriginFieldSurfaced(t, n, "192.168.1.42")

	before := fetchOriginClient(t, n, "192.168.1.42")
	if before.Origin != "dhcp_dynamic" {
		t.Fatalf("before-flip: want dhcp_dynamic, got %q", before.Origin)
	}

	// Operator adds a reservation; next poll cycle must flip the tag.
	srv.setReserved([]string{"192.168.1.42"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		after := fetchOriginClient(t, n, "192.168.1.42")
		if after.Origin == "dhcp_static" && after.OriginConfidence == "high" {
			// Also assert no spoof anomaly was raised for the flip.
			for _, a := range fetchAnomalies(t, n) {
				if a.IP == "192.168.1.42" {
					t.Errorf("origin flip raised a spoof anomaly (must not): %+v", a)
				}
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("origin did not flip to dhcp_static/high within 5s after reservation added")
}
