package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type queryStreamEvent struct {
	Domain     string `json:"domain"`
	Type       string `json:"type"`
	ClientIP   string `json:"client_ip"`
	ProfileID  string `json:"profile_id"`
	Result     string `json:"result"`
	DurationMs int    `json:"duration_ms"`
	Timestamp  string `json:"timestamp"`
}

// StreamQueryLog handles GET /api/v1/query-log/stream.
// It opens a Server-Sent Events connection and pushes each DNS query as it arrives.
// Optional query-string filters: profile_id, result, domain (substring match).
func (h *Handler) StreamQueryLog(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	profileFilter := r.URL.Query().Get("profile_id")
	resultFilter := r.URL.Query().Get("result")
	domainFilter := r.URL.Query().Get("domain")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ql := h.app.GetQueryLog()
	subID, ch := ql.Subscribe()
	defer ql.Unsubscribe(subID)

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case entry, open := <-ch:
			if !open {
				return
			}
			if profileFilter != "" && entry.ProfileID != profileFilter {
				continue
			}
			if resultFilter != "" && string(entry.Outcome) != resultFilter {
				continue
			}
			if domainFilter != "" && !strings.Contains(entry.Domain, domainFilter) {
				continue
			}

			ev := queryStreamEvent{
				Domain:    entry.Domain,
				Type:      entry.QueryType,
				ClientIP:  entry.Client,
				ProfileID: entry.ProfileID,
				Result:    string(entry.Outcome),
				Timestamp: entry.Timestamp.UTC().Format(time.RFC3339),
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: query\ndata: %s\n\n", data)
			flusher.Flush()

		case <-heartbeat.C:
			fmt.Fprintf(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}
