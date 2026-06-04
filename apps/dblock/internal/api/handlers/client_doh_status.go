package handlers

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dblock/dblock/internal/filter"
	"github.com/dblock/dblock/internal/log"
)

// clientDohStatusResponse is the JSON shape documented in
// specs/functional/per-client-doh-status.feature (FS-ClientDohStatusEndpointShape).
type clientDohStatusResponse struct {
	Client            string  `json:"client"`
	UsingDoH          bool    `json:"using_doh"`
	DohProbes1h       int     `json:"doh_probes_1h"`
	LastDoHQuery      *string `json:"last_doh_query"`
	SuspectedProvider *string `json:"suspected_provider"`
}

// dohProviderPatterns maps probe-domain substrings to a canonical provider
// name. Order matters when multiple substrings could match (cloudflare's
// canary "mozilla.cloudflare-dns.com" must resolve to cloudflare, not
// mozilla — so we match the longest-overlap first by ordering carefully).
//
// Adding a provider here is enough; the table is read by inferProvider only.
var dohProviderPatterns = []struct {
	pattern  string
	provider string
}{
	{"cloudflare-dns.com", "cloudflare"},
	{"dns.google", "google"},
	{"dns.quad9.net", "quad9"},
	{"dns.adguard.com", "adguard"},
	{"dns.adguard-dns.com", "adguard"},
	{"doh.opendns.com", "opendns"},
	{"dns.nextdns.io", "nextdns"},
	{"dns.controld.com", "controld"},
}

// inferProvider returns the canonical provider name for a DoH probe
// domain, or "" when nothing matches. Caller decides whether to translate
// empty to nil JSON.
func inferProvider(domain string) string {
	d := strings.ToLower(domain)
	for _, p := range dohProviderPatterns {
		if strings.Contains(d, p.pattern) {
			return p.provider
		}
	}
	return ""
}

// GetClientDohStatus handles GET /api/v1/clients/{ip}/doh-status.
//
// Reads from the local query log only — no Raft round-trip, no cluster
// fan-out. The 1-hour window is rolling against filter.Now(), so test mode
// can drive deterministic windowing via DBLOCK_TEST_NOW.
//
// FSIDs: FS-ClientDohStatusEndpointShape, FS-ClientDohStatusNoProbes,
// FS-ClientDohStatusInvalidIp, FS-ClientDohStatusRollingWindow,
// FS-ClientDohStatusSuspectedProvider.
func (h *Handler) GetClientDohStatus(w http.ResponseWriter, r *http.Request) {
	clientIP := chi.URLParam(r, "ip")
	if net.ParseIP(clientIP) == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid client IP",
		})
		return
	}

	ql := h.app.GetQueryLog()
	// Pull all of THIS client's entries — the ring buffer is bounded so
	// "all" is finite. Filter by outcome=blocked, then narrow to cat:doh
	// probes within the rolling 1h window in this handler.
	entries, _ := ql.Query(clientIP, string(log.OutcomeBlocked), 0, 0)

	cutoff := filter.Now().Add(-time.Hour)
	probes := 0
	var lastTS time.Time
	var lastDomain string
	for _, e := range entries {
		if e.BlocklistID != "cat:doh" {
			continue
		}
		if e.Timestamp.Before(cutoff) {
			continue
		}
		probes++
		if e.Timestamp.After(lastTS) {
			lastTS = e.Timestamp
			lastDomain = e.Domain
		}
	}

	resp := clientDohStatusResponse{
		Client:      clientIP,
		UsingDoH:    probes > 0,
		DohProbes1h: probes,
	}
	if !lastTS.IsZero() {
		s := lastTS.UTC().Format(time.RFC3339)
		resp.LastDoHQuery = &s
		if p := inferProvider(lastDomain); p != "" {
			resp.SuspectedProvider = &p
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
