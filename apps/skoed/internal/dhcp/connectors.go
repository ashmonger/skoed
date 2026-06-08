package dhcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
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
type Config struct {
	Enabled        bool
	Kind           string // "kea" | "dnsmasq" | "http_json"
	URL            string
	FilePath       string
	Username       string
	Password       string
	RefreshSeconds int
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
		return &dnsmasqConn{path: cfg.FilePath}, nil
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

type dnsmasqConn struct{ path string }

func (c *dnsmasqConn) Source() string { return "dnsmasq" }

func (c *dnsmasqConn) Fetch() ([]Lease, error) {
	f, err := os.Open(c.path)
	if err != nil {
		return nil, fmt.Errorf("open dnsmasq lease file: %w", err)
	}
	defer f.Close()

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
		out = append(out, l)
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
			out = append(out, l)
		}
	}
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

type httpJSONLease struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Hostname  string `json:"hostname"`
	ClientID  string `json:"client_id"`
	ExpiresAt string `json:"expires_at"` // RFC3339
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
			IP:       r.IP,
			MAC:      r.MAC,
			Hostname: r.Hostname,
			ClientID: r.ClientID,
			Source:   "http_json",
		}
		if r.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, r.ExpiresAt); err == nil {
				l.ExpiresAt = t
			}
		}
		l.normalize()
		out = append(out, l)
	}
	return out, nil
}
