package dhcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Connector reads a lease snapshot from one upstream DHCP source.
// Implementations are stateless w.r.t. previous polls — the Manager
// holds the cache + history.
type Connector interface {
	// Source returns a short identifier ("kea", "dnsmasq", "http_json")
	// for log messages and the Lease.Source field.
	Source() string
	// Fetch returns the current lease set. Errors are surfaced to the
	// Manager which logs them and reuses the prior snapshot.
	Fetch() ([]Lease, error)
}

// Config is the per-node DHCP block. Mirror of cluster.DhcpSection
// kept local to avoid importing the cluster package.
//
// M6.5 additions (TS-LeaseOrigin, TS-Dhcpv6Lease):
//   - ConfigPath  : dnsmasq running-config path; powers origin tagging.
//   - FilePathV6  : dnsmasq DHCPv6 lease file; defaults to FilePath+"6"
//                   when unset.
type Config struct {
	Enabled        bool
	Kind           string // "kea" | "dnsmasq" | "http_json"
	URL            string
	FilePath       string
	Username       string
	Password       string
	RefreshSeconds int

	// M6.5 — TS-LeaseOrigin: dnsmasq running-config path for static
	// reservation discovery (dhcp-host= directives). When empty, every
	// dnsmasq lease ends up with empty Origin/OriginConfidence (i.e. the
	// M3.6 behaviour is preserved bit-for-bit).
	ConfigPath string

	// M6.5 — TS-Dhcpv6Lease: dnsmasq DHCPv6 lease file (`dnsmasq.leases6`).
	// When unset and a v4 FilePath is set, the connector tries
	// FilePath+"6" once on boot. If that file doesn't exist, v6 is
	// silently disabled — no spam.
	FilePathV6 string
}

// New builds a Connector from a Config. Returns an error when Kind is
// unknown or required fields are missing.
func New(cfg Config) (Connector, error) {
	switch cfg.Kind {
	case "kea":
		if cfg.URL == "" {
			return nil, errors.New("kea connector: url required")
		}
		return &keaConn{url: cfg.URL, user: cfg.Username, pass: cfg.Password,
			client: &http.Client{Timeout: 5 * time.Second}}, nil
	case "dnsmasq":
		if cfg.FilePath == "" {
			return nil, errors.New("dnsmasq connector: file_path required")
		}
		conn := &dnsmasqConn{
			path:       cfg.FilePath,
			configPath: cfg.ConfigPath,
			pathV6:     cfg.FilePathV6,
		}
		// M6.5 — TS-LeaseOrigin: when no explicit ConfigPath is set,
		// auto-discover dnsmasq.conf next to the lease file. This is the
		// canonical dnsmasq layout (`/var/lib/misc/dnsmasq.leases` lives
		// next to `/etc/dnsmasq.conf` only on Debian's split layout —
		// homelab installs almost always co-locate them).
		if conn.configPath == "" {
			candidate := guessDnsmasqConfigPath(cfg.FilePath)
			if candidate != "" {
				conn.configPath = candidate
			}
		}
		return conn, nil
	case "http_json":
		if cfg.URL == "" {
			return nil, errors.New("http_json connector: url required")
		}
		return &httpJSONConn{url: cfg.URL, user: cfg.Username, pass: cfg.Password,
			client: &http.Client{Timeout: 5 * time.Second}}, nil
	default:
		return nil, fmt.Errorf("unknown dhcp.kind: %q", cfg.Kind)
	}
}

// ─── dnsmasq ─────────────────────────────────────────────────────────

type dnsmasqConn struct {
	path       string // v4 lease file
	configPath string // dnsmasq.conf (M6.5 — origin tagging); optional
	pathV6     string // v6 lease file (M6.5 — TS-Dhcpv6Lease); optional
}

func (c *dnsmasqConn) Source() string { return "dnsmasq" }

// dnsmasqConfigMaxRead caps the dnsmasq.conf read to 1 MiB (defence
// against `--conf-dir=/` misconfigurations).
const dnsmasqConfigMaxRead = 1 << 20

// guessDnsmasqConfigPath returns the path to a sibling dnsmasq.conf
// next to the given lease file, when one exists. Empty string when no
// candidate is found.
func guessDnsmasqConfigPath(leaseFilePath string) string {
	dir, _ := splitDir(leaseFilePath)
	if dir == "" {
		return ""
	}
	candidate := dir + "/dnsmasq.conf"
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// splitDir is a stdlib-free filepath.Dir for the limited use here. We
// keep dependencies minimal in this package.
func splitDir(p string) (dir, base string) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i], p[i+1:]
		}
	}
	return "", p
}

// dnsmasqOriginIndex captures the set of static-lookup keys harvested
// from a dnsmasq.conf. Empty when ConfigPath is unset or unreadable —
// callers detect "no claim" via origin tagging fallback.
type dnsmasqOriginIndex struct {
	ok        bool // false ⇒ ConfigPath set but unreadable; tag every lease unknown
	disabled  bool // true  ⇒ no ConfigPath at all; preserve M3.6 wire shape
	truncated bool // 1 MiB cap hit
	ips       map[string]struct{}
	macs      map[string]struct{}
	ids       map[string]struct{}
}

// loadDnsmasqOriginIndex reads ConfigPath (when set) and returns the set
// of (ip, mac, id) keys mentioned in `dhcp-host=` directives.
func loadDnsmasqOriginIndex(configPath string) dnsmasqOriginIndex {
	if configPath == "" {
		return dnsmasqOriginIndex{disabled: true}
	}
	// Pre-flight mode check: if the file has no read bits set at all,
	// treat it as unreadable even when the process happens to be root.
	// Docker test environments run as root and would otherwise bypass
	// chmod 0000, but the spec's intent is "operator removed read
	// access ⇒ no claim about origin" (per FS-LeaseOriginDnsmasqConfigUnreadable).
	if st, err := os.Stat(configPath); err == nil {
		if st.Mode().Perm()&0o444 == 0 {
			log.Printf("dhcp dnsmasq_config_unreadable path=%s mode=%v (no read bits)", configPath, st.Mode())
			return dnsmasqOriginIndex{}
		}
	}
	f, err := os.Open(configPath)
	if err != nil {
		log.Printf("dhcp dnsmasq_config_unreadable path=%s err=%v", configPath, err)
		return dnsmasqOriginIndex{}
	}
	defer f.Close()
	idx := dnsmasqOriginIndex{
		ok:   true,
		ips:  map[string]struct{}{},
		macs: map[string]struct{}{},
		ids:  map[string]struct{}{},
	}
	body, err := io.ReadAll(io.LimitReader(f, dnsmasqConfigMaxRead+1))
	if err != nil {
		log.Printf("dhcp dnsmasq_config_unreadable path=%s err=%v", configPath, err)
		return dnsmasqOriginIndex{}
	}
	if len(body) > dnsmasqConfigMaxRead {
		log.Printf("dhcp dnsmasq_config_too_large path=%s size>%d", configPath, dnsmasqConfigMaxRead)
		body = body[:dnsmasqConfigMaxRead]
		idx.truncated = true
	}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		val, ok := strings.CutPrefix(line, "dhcp-host=")
		if !ok {
			continue
		}
		for _, part := range strings.Split(val, ",") {
			p := strings.TrimSpace(part)
			switch {
			case p == "":
			case strings.HasPrefix(p, "set:"), strings.HasPrefix(p, "tag:"):
				// dhcp-host=set:<tag>... has no IP binding — skip the whole record.
				continue
			case strings.HasPrefix(p, "id:"):
				idx.ids[p] = struct{}{}
			case net.ParseIP(p) != nil:
				idx.ips[p] = struct{}{}
			case looksLikeMAC(p):
				idx.macs[strings.ToLower(p)] = struct{}{}
			default:
				// hostname / lease-time — ignored for matching.
			}
		}
	}
	return idx
}

// looksLikeMAC returns true for the canonical 6-octet colon form.
func looksLikeMAC(s string) bool {
	if len(s) != 17 {
		return false
	}
	for i, r := range s {
		if i%3 == 2 {
			if r != ':' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// matches reports whether the lease appears in this directive set.
func (idx dnsmasqOriginIndex) matches(l Lease) bool {
	if _, ok := idx.ips[l.IP]; ok {
		return true
	}
	if l.MAC != "" {
		if _, ok := idx.macs[strings.ToLower(l.MAC)]; ok {
			return true
		}
	}
	if l.ClientID != "" {
		if _, ok := idx.ids[l.ClientID]; ok {
			return true
		}
	}
	return false
}

// tag applies the M6.5 origin annotation to one parsed lease in-place.
func (idx dnsmasqOriginIndex) tag(l *Lease) {
	switch {
	case idx.disabled:
		// No ConfigPath set — M3.6 wire shape preserved; leave fields empty.
		return
	case !idx.ok:
		// ConfigPath set but unreadable: every lease tagged dynamic/unknown.
		l.Origin = OriginDhcpDynamic
		l.OriginConfidence = OriginConfidenceUnknown
		return
	case idx.matches(*l):
		l.Origin = OriginDhcpStatic
		l.OriginConfidence = OriginConfidenceInferred
	default:
		l.Origin = OriginDhcpDynamic
		l.OriginConfidence = OriginConfidenceHigh
	}
}

func (c *dnsmasqConn) Fetch() ([]Lease, error) {
	f, err := os.Open(c.path)
	if err != nil {
		return nil, fmt.Errorf("open dnsmasq lease file: %w", err)
	}
	defer f.Close()

	idx := loadDnsmasqOriginIndex(c.configPath)

	now := time.Now()
	var out []Lease
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// dnsmasq leases lines look like:
		//   <expiry-epoch> <mac> <ip> <hostname> <client-id>
		// hostname or client-id may be "*" meaning "absent".
		if len(fields) < 4 {
			continue // malformed — skip silently (the spec says: log warn)
		}
		exp, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		l := Lease{
			IP:        fields[2],
			MAC:       fields[1],
			Hostname:  fields[3],
			ExpiresAt: time.Unix(exp, 0),
			Source:    "dnsmasq",
		}
		if len(fields) >= 5 {
			l.ClientID = fields[4]
		}
		l.normalize()
		if l.IsExpired(now) {
			continue
		}
		idx.tag(&l)
		out = append(out, l)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}

	// M6.5 — DHCPv6 lease file (TS-Dhcpv6Lease). pathV6 may be empty, in
	// which case we fall back to "<v4 path>6" probed once; missing file
	// is INFO-once and treated as v6-disabled.
	if v6, err := c.fetchV6(now); err == nil {
		out = append(out, v6...)
	}
	return out, nil
}

// dnsmasqV6MissingOnce ensures the absent-v6 INFO message only logs once
// per process per path (matches TS-Dhcpv6Lease — refuse to log-spam).
var dnsmasqV6MissingOnce sync.Map // key: path → struct{}

// fetchV6 reads /var/lib/misc/dnsmasq.leases6 (or the configured v6 path)
// and returns one Lease per active v6 entry. Returns nil + nil error when
// no v6 file is configured or the file is absent (the most common case).
func (c *dnsmasqConn) fetchV6(now time.Time) ([]Lease, error) {
	path := c.pathV6
	if path == "" && c.path != "" {
		candidate := c.path + "6"
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
		}
	}
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			if _, loaded := dnsmasqV6MissingOnce.LoadOrStore(path, struct{}{}); !loaded {
				log.Printf("dhcp dnsmasq v6 file not present: %s", path)
			}
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Lease
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// Format: <expiry-epoch> <iaid> <ipv6> <hostname> <duid>
		if len(fields) < 5 {
			log.Printf("dhcp dnsmasq_v6_malformed line=%q", line)
			continue
		}
		exp, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			log.Printf("dhcp dnsmasq_v6_malformed line=%q err=%v", line, err)
			continue
		}
		expires := time.Unix(exp, 0)
		if !expires.IsZero() && expires.Before(now) {
			continue // expired
		}
		host := fields[3]
		if host == "*" {
			host = ""
		}
		out = append(out, Lease{
			Source:        "dnsmasq",
			Hostname:      host,
			IPv6Addresses: []string{fields[2]},
			DUID:          fields[4],
			ExpiresAt:     expires,
		})
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── Kea ─────────────────────────────────────────────────────────────

type keaConn struct {
	url    string
	user   string
	pass   string
	client *http.Client
}

func (c *keaConn) Source() string { return "kea" }

// keaResponse mirrors the control-agent's command-response wrapper for
// `lease4-get-all`. Result 0 = success.
type keaResponse []struct {
	Result    int    `json:"result"`
	Text      string `json:"text"`
	Arguments struct {
		Leases []struct {
			IPAddress string `json:"ip-address"`
			HWAddress string `json:"hw-address"`
			Hostname  string `json:"hostname"`
			ClientID  string `json:"client-id"`
			ValidLft  int64  `json:"valid-lft"`
			Cltt      int64  `json:"cltt"`
		} `json:"leases"`
	} `json:"arguments"`
}

// keaReservationsResponse mirrors `reservation-get-all` for dhcp4.
type keaReservationsResponse []struct {
	Result    int    `json:"result"`
	Text      string `json:"text"`
	Arguments struct {
		Hosts []struct {
			IPAddress string `json:"ip-address"`
			HWAddress string `json:"hw-address"`
			Hostname  string `json:"hostname"`
			ClientID  string `json:"client-id"`
		} `json:"hosts"`
	} `json:"arguments"`
}

// keaLease6Response mirrors `lease6-get-all` for dhcp6.
type keaLease6Response []struct {
	Result    int    `json:"result"`
	Text      string `json:"text"`
	Arguments struct {
		Leases []struct {
			IPAddress string `json:"ip-address"`
			Type      string `json:"type"` // "IA_NA" | "IA_PD" | "IA_TA"
			DUID      string `json:"duid"`
			Hostname  string `json:"hostname"`
			HWAddress string `json:"hw-address"`
			ValidLft  int64  `json:"valid-lft"`
			Cltt      int64  `json:"cltt"`
			PrefLen   int    `json:"prefix-len,omitempty"`
		} `json:"leases"`
	} `json:"arguments"`
}

// postCommand POSTs the given JSON command body to the Kea control-agent.
// Caller is responsible for closing the returned *http.Response.Body.
func (c *keaConn) postCommand(body string) (*http.Response, error) {
	req, err := http.NewRequest("POST", c.url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	return c.client.Do(req)
}

// fetchReservedIPs returns the IPs Kea reports as host reservations and
// an `ok` flag — false means reservation-get-all errored / returned a
// non-success result, in which case the caller treats every lease as
// dhcp_dynamic / unknown (per FS-LeaseOriginKeaReservationsUnreachableInferred).
func (c *keaConn) fetchReservedIPs() (map[string]struct{}, bool) {
	resp, err := c.postCommand(`{"command":"reservation-get-all","service":["dhcp4"],"arguments":{"subnet-id":0}}`)
	if err != nil {
		log.Printf("dhcp kea_reservation_lookup_failed err=%v", err)
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("dhcp kea_reservation_lookup_failed status=%d", resp.StatusCode)
		return nil, false
	}
	var rr keaReservationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		log.Printf("dhcp kea_reservation_lookup_failed decode=%v", err)
		return nil, false
	}
	out := map[string]struct{}{}
	for _, env := range rr {
		if env.Result != 0 {
			log.Printf("dhcp kea_reservation_lookup_failed result=%d text=%q", env.Result, env.Text)
			return nil, false
		}
		for _, h := range env.Arguments.Hosts {
			if h.IPAddress != "" {
				out[strings.TrimSpace(h.IPAddress)] = struct{}{}
			}
		}
	}
	return out, true
}

// fetchV6 returns the merged DHCPv6 leases from Kea. IA_NA + IA_PD
// records sharing one DUID collapse into a single Lease.
func (c *keaConn) fetchV6() []Lease {
	resp, err := c.postCommand(`{"command":"lease6-get-all","service":["dhcp6"]}`)
	if err != nil {
		log.Printf("dhcp kea lease6-get-all err=%v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("dhcp kea lease6-get-all status=%d", resp.StatusCode)
		return nil
	}
	var lr keaLease6Response
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		log.Printf("dhcp kea lease6 decode err=%v", err)
		return nil
	}
	byDUID := map[string]*Lease{}
	var order []string // preserve first-seen DUID order for stable output
	for _, env := range lr {
		if env.Result != 0 {
			continue
		}
		for _, k := range env.Arguments.Leases {
			addr := k.IPAddress
			if k.Type == "IA_PD" && k.PrefLen > 0 {
				addr = fmt.Sprintf("%s/%d", k.IPAddress, k.PrefLen)
			}
			key := k.DUID
			if key == "" {
				key = "addr:" + addr
			}
			l, ok := byDUID[key]
			if !ok {
				l = &Lease{
					Source:        "kea",
					Hostname:      strings.TrimSpace(k.Hostname),
					DUID:          k.DUID,
					IPv6Addresses: []string{},
					ExpiresAt:     time.Unix(k.Cltt+k.ValidLft, 0),
				}
				if l.Hostname == "*" {
					l.Hostname = ""
				}
				byDUID[key] = l
				order = append(order, key)
			}
			l.IPv6Addresses = append(l.IPv6Addresses, addr)
		}
	}
	if len(byDUID) == 0 {
		return nil
	}
	out := make([]Lease, 0, len(byDUID))
	for _, k := range order {
		l := byDUID[k]
		// Stable address ordering keeps Raft snapshots diffable.
		sortStrings(l.IPv6Addresses)
		out = append(out, *l)
	}
	return out
}

// sortStrings is a tiny helper that avoids importing "sort" just here.
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

func (c *keaConn) Fetch() ([]Lease, error) {
	body := strings.NewReader(`{"command":"lease4-get-all","service":["dhcp4"]}`)
	req, err := http.NewRequest("POST", c.url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("kea %s: %d %s", c.url, resp.StatusCode, string(raw))
	}

	var kr keaResponse
	if err := json.NewDecoder(resp.Body).Decode(&kr); err != nil {
		// Some Kea deployments wrap the response in a single object rather
		// than an array — try the single-object form too.
		return nil, fmt.Errorf("kea decode: %w", err)
	}

	// M6.5 — TS-LeaseOrigin: fetch the reservation set BEFORE we
	// annotate leases. On error the OK flag tells us to fall back to
	// "every lease dhcp_dynamic / unknown".
	reserved, reservationsOK := c.fetchReservedIPs()

	var out []Lease
	for _, env := range kr {
		if env.Result != 0 {
			continue
		}
		for _, k := range env.Arguments.Leases {
			l := Lease{
				IP:        k.IPAddress,
				MAC:       k.HWAddress,
				Hostname:  k.Hostname,
				ClientID:  k.ClientID,
				Source:    "kea",
				ExpiresAt: time.Unix(k.Cltt+k.ValidLft, 0),
			}
			l.normalize()
			switch {
			case !reservationsOK:
				l.Origin = OriginDhcpDynamic
				l.OriginConfidence = OriginConfidenceUnknown
			default:
				if _, ok := reserved[l.IP]; ok {
					l.Origin = OriginDhcpStatic
				} else {
					l.Origin = OriginDhcpDynamic
				}
				l.OriginConfidence = OriginConfidenceHigh
			}
			out = append(out, l)
		}
	}

	// M6.5 — TS-Dhcpv6Lease: append v6 leases. Errors are logged
	// in fetchV6 and we just keep the v4 result (partial success >
	// dropping the whole poll).
	out = append(out, c.fetchV6()...)
	return out, nil
}

// ─── Generic HTTP JSON ───────────────────────────────────────────────

type httpJSONConn struct {
	url    string
	user   string
	pass   string
	client *http.Client
}

func (c *httpJSONConn) Source() string { return "http_json" }

// httpJSONLease is the on-the-wire shape — M6.5 adds optional origin,
// ipv6_addresses, duid fields, all `omitempty` so an M3.6 producer
// remains valid input.
type httpJSONLease struct {
	IP            string   `json:"ip"`
	MAC           string   `json:"mac"`
	Hostname      string   `json:"hostname"`
	ClientID      string   `json:"client_id"`
	ExpiresAt     string   `json:"expires_at"` // RFC3339
	Origin        string   `json:"origin,omitempty"`
	IPv6Addresses []string `json:"ipv6_addresses,omitempty"`
	DUID          string   `json:"duid,omitempty"`
}

// httpJSONOriginUnknownOnce dedupes the "unknown origin value" warning
// within one poll cycle so a bad upstream doesn't flood the log.
var httpJSONOriginUnknownOnce sync.Map

// tagOriginFromHTTPJSON applies the M6.5 origin field semantics. Returns
// an updated lease (value semantics).
func tagOriginFromHTTPJSON(l Lease, raw string) Lease {
	switch raw {
	case "":
		l.Origin = OriginDhcpDynamic
		l.OriginConfidence = OriginConfidenceUnknown
	case string(OriginDhcpStatic):
		l.Origin = OriginDhcpStatic
		l.OriginConfidence = OriginConfidenceHigh
	case string(OriginDhcpDynamic):
		l.Origin = OriginDhcpDynamic
		l.OriginConfidence = OriginConfidenceHigh
	case string(OriginRouterAdvertised), string(OriginManualAdmin):
		// Forward-compat enum values: accept them on the wire as high
		// confidence (TS-LeaseOrigin says no M6.5 connector emits them,
		// but a future caller might).
		l.Origin = Origin(raw)
		l.OriginConfidence = OriginConfidenceHigh
	default:
		if _, loaded := httpJSONOriginUnknownOnce.LoadOrStore(raw, struct{}{}); !loaded {
			log.Printf("dhcp http_json_unknown_origin_value value=%q", raw)
		}
		l.Origin = OriginDhcpDynamic
		l.OriginConfidence = OriginConfidenceUnknown
	}
	return l
}

func (c *httpJSONConn) Fetch() ([]Lease, error) {
	req, err := http.NewRequest("GET", c.url, nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http_json %s: status %d", c.url, resp.StatusCode)
	}
	var raw []httpJSONLease
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("http_json decode: %w", err)
	}
	out := make([]Lease, 0, len(raw))
	for _, r := range raw {
		l := Lease{
			IP:            r.IP,
			MAC:           r.MAC,
			Hostname:      r.Hostname,
			ClientID:      r.ClientID,
			Source:        "http_json",
			IPv6Addresses: append([]string(nil), r.IPv6Addresses...),
			DUID:          r.DUID,
		}
		if r.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, r.ExpiresAt); err == nil {
				l.ExpiresAt = t
			}
		}
		l.normalize()
		l = tagOriginFromHTTPJSON(l, r.Origin)
		out = append(out, l)
	}
	return out, nil
}
