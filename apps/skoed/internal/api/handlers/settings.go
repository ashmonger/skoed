package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/skoed/skoed/internal/config"
	dnsengine "github.com/skoed/skoed/internal/dns"
)

// settingsResponse is the JSON shape returned by GET /api/v1/settings.
type settingsResponse struct {
	DNS           config.DNSConfig      `json:"dns"`
	Filtering     filteringSettings     `json:"filtering"`
	QueryLog      config.QueryLogConfig `json:"query_log"`
	DNSCryptStamp string                `json:"dnscrypt_stamp,omitempty"` // M8: sdns:// URI
	// M34: TLS auto-renewal settings (omitted when cluster has no TLS renew config).
	TLS *tlsSettingsResp `json:"tls,omitempty"`
	// M34.5: session timeout settings.
	Auth authSettingsResp `json:"auth"`
}

// authSettingsResp is the auth sub-object in the settings response.
type authSettingsResp struct {
	SessionTimeoutSeconds int `json:"session_timeout_seconds"`
}

// tlsSettingsResp is the tls sub-object in the settings response.
type tlsSettingsResp struct {
	AutoRenew            bool            `json:"auto_renew"`
	RenewalThresholdDays int             `json:"renewal_threshold_days"`
	ACME                 acmeSettingsResp `json:"acme"`
}

type acmeSettingsResp struct {
	Domains []string `json:"domains"`
	Email   string   `json:"email"`
}

type filteringSettings struct {
	BlockPolicy string `json:"block_policy"`
}

// GetSettings handles GET /api/v1/settings.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	sessionTimeout := cfg.Auth.SessionTimeoutSeconds
	if sessionTimeout == 0 {
		sessionTimeout = 28800 // 8-hour default
	}
	resp := settingsResponse{
		DNS: cfg.DNS,
		Filtering: filteringSettings{
			BlockPolicy: cfg.Filtering.BlockPolicy,
		},
		QueryLog:      cfg.QueryLog,
		DNSCryptStamp: dnscryptStamp(h),
		Auth:          authSettingsResp{SessionTimeoutSeconds: sessionTimeout},
	}
	// M34: include TLS renew settings when a cluster is available.
	if tlsCfg, err := h.app.GetTLSRenewConfig(); err == nil {
		domains := tlsCfg.ACME.Domains
		if domains == nil {
			domains = []string{}
		}
		threshold := tlsCfg.RenewalThresholdDays
		if threshold == 0 {
			threshold = 30
		}
		resp.TLS = &tlsSettingsResp{
			AutoRenew:            tlsCfg.AutoRenew,
			RenewalThresholdDays: threshold,
			ACME: acmeSettingsResp{
				Domains: domains,
				Email:   tlsCfg.ACME.Email,
			},
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// dnscryptStamp returns the sdns:// URI for this node, or "" when DNSCrypt
// is not configured or no keypair has been generated yet.
func dnscryptStamp(h *Handler) string {
	c := h.app.GetCluster()
	if c == nil {
		return ""
	}
	node := c.Node()
	if node == nil || node.Node.DNS.Listen.DNSCryptPort == 0 {
		return ""
	}
	keys, err := c.GetDNSCryptKeys()
	if err != nil || keys == nil {
		return ""
	}
	// Derive the public host from the API address (strip port, keep IP).
	host, _, splitErr := net.SplitHostPort(node.Node.APIAddress)
	if splitErr != nil {
		host = node.Node.APIAddress
	}
	addr := fmt.Sprintf("%s:%d", host, node.Node.DNS.Listen.DNSCryptPort)
	return dnsengine.DNSCryptStamp(keys.Config, addr)
}

// settingsPatch is the body accepted by PATCH /api/v1/settings.
// All fields are optional; only present fields are applied.
type settingsPatch struct {
	DNS       *dnsPatch       `json:"dns"`
	Filtering *filteringPatch `json:"filtering"`
	QueryLog  *queryLogPatch  `json:"query_log"`
	Auth      *authPatch      `json:"auth"`
}

type authPatch struct {
	SessionTimeoutSeconds *int `json:"session_timeout_seconds"`
}

type dnsPatch struct {
	Mode              *string               `json:"mode"`
	DNSSECMode        *string               `json:"dnssec_mode"`
	UpstreamResolvers []string              `json:"upstream_resolvers"`
	UpstreamRoutes    []config.UpstreamRoute `json:"upstream_routes"`
	UpstreamTimeout   *int                  `json:"upstream_timeout_seconds"`
	TrustedSubnets    []string              `json:"trusted_subnets"`
	Cache             *config.CacheConfig   `json:"cache"`
}

type filteringPatch struct {
	BlockPolicy     *string `json:"block_policy"`
	PauseMaxSeconds *int    `json:"pause_max_seconds"`
}

type queryLogPatch struct {
	MaxEntries *int `json:"max_entries"`
}

// UpdateSettings handles PATCH /api/v1/settings.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	// Use a raw map decode first so we can detect which fields are explicitly present.
	// Then decode into the typed struct for validation.
	var patch settingsPatch
	// We decode from a json.RawMessage approach to handle partial updates properly.
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	rebuildDNS := false
	rebuildFilter := false
	updateQueryLog := false
	var newMaxEntries int

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		if patch.DNS != nil {
			d := patch.DNS
			if d.Mode != nil {
				if *d.Mode != "forwarding" && *d.Mode != "recursive" {
					return &validationError{"dns.mode must be 'forwarding' or 'recursive'"}
				}
				cfg.DNS.Mode = *d.Mode
				rebuildDNS = true
			}
			if d.DNSSECMode != nil {
				if *d.DNSSECMode != "transparent" && *d.DNSSECMode != "validate" {
					return &validationError{"dns.dnssec_mode must be 'transparent' or 'validate'"}
				}
				cfg.DNS.DNSSECMode = *d.DNSSECMode
				rebuildDNS = true
			}
			if d.UpstreamResolvers != nil {
				normalised := make([]string, len(d.UpstreamResolvers))
				for i, u := range d.UpstreamResolvers {
					n, err := config.NormaliseUpstream(u)
					if err != nil {
						return &validationError{fmt.Sprintf("dns.upstream_resolvers[%d]: %s", i, err)}
					}
					normalised[i] = n
				}
				cfg.DNS.UpstreamResolvers = normalised
				rebuildDNS = true
			}
			if d.UpstreamRoutes != nil {
				for i, route := range d.UpstreamRoutes {
					if err := config.ValidateUpstreamRoute(route); err != nil {
						return &validationError{fmt.Sprintf("dns.upstream_routes[%d]: %s", i, err)}
					}
				}
				cfg.DNS.UpstreamRoutes = d.UpstreamRoutes
				rebuildDNS = true
			}
			if d.UpstreamTimeout != nil {
				if *d.UpstreamTimeout < 0 {
					return &validationError{"dns.upstream_timeout_seconds must be >= 0"}
				}
				cfg.DNS.UpstreamTimeout = *d.UpstreamTimeout
				rebuildDNS = true
			}
			if d.TrustedSubnets != nil {
				cfg.DNS.TrustedSubnets = d.TrustedSubnets
				rebuildDNS = true
			}
			if d.Cache != nil {
				cfg.DNS.Cache = *d.Cache
				rebuildDNS = true
			}
		}
		if patch.Filtering != nil {
			f := patch.Filtering
			if f.BlockPolicy != nil {
				switch *f.BlockPolicy {
				case "nxdomain", "null", "nodata", "redirect":
				default:
					return &validationError{"filtering.block_policy must be nxdomain, null, nodata, or redirect"}
				}
				cfg.Filtering.BlockPolicy = *f.BlockPolicy
				rebuildFilter = true
			}
			if f.PauseMaxSeconds != nil {
				if *f.PauseMaxSeconds < 0 {
					return &validationError{"filtering.pause_max_seconds must be >= 0"}
				}
				cfg.Filtering.PauseMaxSeconds = *f.PauseMaxSeconds
			}
		}
		if patch.QueryLog != nil {
			q := patch.QueryLog
			if q.MaxEntries != nil {
				if *q.MaxEntries < 0 {
					return &validationError{"query_log.max_entries must be >= 0"}
				}
				cfg.QueryLog.MaxEntries = *q.MaxEntries
				newMaxEntries = *q.MaxEntries
				updateQueryLog = true
			}
		}
		if patch.Auth != nil {
			a := patch.Auth
			if a.SessionTimeoutSeconds != nil {
				v := *a.SessionTimeoutSeconds
				if v <= 0 || v > 604800 {
					return &validationError{"auth.session_timeout_seconds must be between 1 and 604800"}
				}
				cfg.Auth.SessionTimeoutSeconds = v
			}
		}
		return nil
	}); err != nil {
		if ve, ok := err.(*validationError); ok {
			writeError(w, http.StatusBadRequest, ve.msg)
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	if rebuildFilter {
		if err := h.app.RebuildFilter(); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild filter: "+err.Error())
			return
		}
	}
	if rebuildDNS {
		if err := h.app.RebuildDNSFromCfg(); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild dns: "+err.Error())
			return
		}
	}
	if updateQueryLog {
		h.app.GetQueryLog().SetMaxEntries(newMaxEntries)
	}

	cfg := h.app.GetCfg()
	resp := settingsResponse{
		DNS: cfg.DNS,
		Filtering: filteringSettings{
			BlockPolicy: cfg.Filtering.BlockPolicy,
		},
		QueryLog:      cfg.QueryLog,
		DNSCryptStamp: dnscryptStamp(h),
	}
	writeJSON(w, http.StatusOK, resp)
}

// tlsSettingsPut is the body accepted by PUT /api/v1/settings/tls.
type tlsSettingsPut struct {
	AutoRenew            bool     `json:"auto_renew"`
	RenewalThresholdDays int      `json:"renewal_threshold_days"`
	ACME                 struct {
		Domains []string `json:"domains"`
		Email   string   `json:"email"`
	} `json:"acme"`
}

// PutTLSSettings handles PUT /api/v1/settings/tls.
// Persists TLS auto-renewal settings cluster-wide via Raft.
func (h *Handler) PutTLSSettings(w http.ResponseWriter, r *http.Request) {
	var body tlsSettingsPut
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	threshold := body.RenewalThresholdDays
	if threshold == 0 {
		threshold = 30
	}
	if threshold < 1 {
		writeError(w, http.StatusBadRequest, "renewal_threshold_days must be >= 1")
		return
	}
	domains := body.ACME.Domains
	if domains == nil {
		domains = []string{}
	}
	cfg := config.TLSRenewConfig{
		AutoRenew:            body.AutoRenew,
		RenewalThresholdDays: threshold,
		ACME: config.ACMERenewConfig{
			Domains: domains,
			Email:   body.ACME.Email,
		},
	}
	if err := h.app.SetTLSRenewConfig(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "set tls renew config: "+err.Error())
		return
	}
	saved, _ := h.app.GetTLSRenewConfig()
	savedDomains := saved.ACME.Domains
	if savedDomains == nil {
		savedDomains = []string{}
	}
	writeJSON(w, http.StatusOK, tlsSettingsResp{
		AutoRenew:            saved.AutoRenew,
		RenewalThresholdDays: saved.RenewalThresholdDays,
		ACME: acmeSettingsResp{
			Domains: savedDomains,
			Email:   saved.ACME.Email,
		},
	})
}

// Health handles GET /api/v1/health. No auth required.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DiscoverUpstreams handles POST /api/v1/settings/discover-upstreams.
// It reads /etc/resolv.conf and returns nameserver entries as suggestions.
// Nothing is applied automatically — the operator must PATCH /api/v1/settings
// to use the suggestions.
func (h *Handler) DiscoverUpstreams(w http.ResponseWriter, r *http.Request) {
	resolvers := readResolvConf("/etc/resolv.conf")
	writeJSON(w, http.StatusOK, map[string][]string{
		"suggested_resolvers": resolvers,
	})
}

// readResolvConf parses nameserver lines from a resolv.conf-format file.
func readResolvConf(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		addr := net.JoinHostPort(parts[1], "53")
		out = append(out, addr)
	}
	return out
}

// validationError is an internal sentinel for input validation failures.
type validationError struct {
	msg string
}

func (e *validationError) Error() string { return e.msg }
