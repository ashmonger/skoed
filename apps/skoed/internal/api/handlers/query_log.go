package handlers

import (
	"net/http"
	"strconv"
	"time"
)

type queryLogEntry struct {
	ID             string    `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	Client         string    `json:"client"`
	Domain         string    `json:"domain"`
	QueryType      string    `json:"query_type"`
	Outcome        string    `json:"outcome"`
	BlocklistID    string    `json:"blocklist_id"`
	Category       string    `json:"category"`
	ProfileID      string    `json:"profile_id,omitempty"`
	ClientHostname string    `json:"client_hostname,omitempty"`
	ClientMAC      string    `json:"client_mac,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	PauseActive    bool      `json:"pause_active,omitempty"`
	DnssecStatus   string    `json:"dnssec_status,omitempty"` // M21: "ok", "bogus", "insecure", "indeterminate"
	// M35.5 (TS-DeviceRegistry): name of the registered device that matched
	// the client, if any. Omitted for unregistered clients.
	DeviceName string `json:"device_name,omitempty"`
}

type queryLogResponse struct {
	Entries []queryLogEntry `json:"entries"`
	Total   int             `json:"total"`
}

// GetQueryLog handles GET /api/v1/query-log.
// Supports optional query params: client=, outcome=, limit=, offset=
func (h *Handler) GetQueryLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client := q.Get("client")
	outcome := q.Get("outcome")

	limit := 100
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	offset := 0
	if o := q.Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	ql := h.app.GetQueryLog()
	entries, total := ql.Query(client, outcome, limit, offset)

	// M35.5 (TS-DeviceRegistry): build IP→device name index for enrichment.
	deviceByIP := map[string]string{}
	if cfg := h.app.GetCfg(); cfg != nil {
		for _, d := range cfg.Devices {
			for _, ip := range d.IPs {
				deviceByIP[ip] = d.Name
			}
		}
	}

	result := make([]queryLogEntry, len(entries))
	for i, e := range entries {
		result[i] = queryLogEntry{
			ID:             e.ID,
			Timestamp:      e.Timestamp,
			Client:         e.Client,
			Domain:         e.Domain,
			QueryType:      e.QueryType,
			Outcome:        string(e.Outcome),
			BlocklistID:    e.BlocklistID,
			Category:       e.Category,
			ProfileID:      e.ProfileID,
			ClientHostname: e.ClientHostname,
			ClientMAC:      e.ClientMAC,
			ClientID:       e.ClientID,
			PauseActive:    e.PauseActive,
			DnssecStatus:   e.DnssecStatus,
			DeviceName:     deviceByIP[e.Client],
		}
	}

	writeJSON(w, http.StatusOK, queryLogResponse{
		Entries: result,
		Total:   total,
	})
}
