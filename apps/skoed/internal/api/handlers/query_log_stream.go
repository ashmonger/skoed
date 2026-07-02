package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	dlog "github.com/skoed/skoed/internal/log"
)

// queryStreamEvent is the JSON payload for every SSE "event: query" frame.
// M43 adds DnssecStatus/DnssecError; M41 adds NodeID.
type queryStreamEvent struct {
	Domain       string `json:"domain"`
	Type         string `json:"type"`
	ClientIP     string `json:"client_ip"`
	ProfileID    string `json:"profile_id"`
	Result       string `json:"result"`
	DurationMs   int    `json:"duration_ms"`
	Timestamp    string `json:"timestamp"`
	NodeID       string `json:"node_id,omitempty"`
	DnssecStatus string `json:"dnssec_status,omitempty"`
	DnssecError  string `json:"dnssec_error,omitempty"`
}

// streamFilters holds the parsed query-string filter values.
type streamFilters struct {
	profileID    string
	result       string
	domain       string
	dnssecStatus string
}

func parseStreamFilters(r *http.Request) streamFilters {
	return streamFilters{
		profileID:    r.URL.Query().Get("profile_id"),
		result:       r.URL.Query().Get("result"),
		domain:       r.URL.Query().Get("domain"),
		dnssecStatus: r.URL.Query().Get("dnssec_status"),
	}
}

func (f streamFilters) matches(e dlog.Entry) bool {
	if f.profileID != "" && e.ProfileID != f.profileID {
		return false
	}
	if f.result != "" && string(e.Outcome) != f.result {
		return false
	}
	if f.domain != "" && !strings.Contains(e.Domain, f.domain) {
		return false
	}
	if f.dnssecStatus != "" && e.DnssecStatus != f.dnssecStatus {
		return false
	}
	return true
}

func entryToEvent(e dlog.Entry, nodeID string) queryStreamEvent {
	return queryStreamEvent{
		Domain:       e.Domain,
		Type:         e.QueryType,
		ClientIP:     e.Client,
		ProfileID:    e.ProfileID,
		Result:       string(e.Outcome),
		Timestamp:    e.Timestamp.UTC().Format(time.RFC3339),
		NodeID:       nodeID,
		DnssecStatus: e.DnssecStatus,
		DnssecError:  e.DnssecError,
	}
}

func writeSSEQuery(w http.ResponseWriter, flusher http.Flusher, data []byte) {
	fmt.Fprintf(w, "event: query\ndata: %s\n\n", data)
	flusher.Flush()
}

// StreamQueryLog handles GET /api/v1/query-log/stream.
// M29: single-node SSE.  M41: ?cluster=true aggregates all peers.
// M42: ?backfill=N replays N recent entries before going live.
// M43: dnssec_status/dnssec_error on events; ?dnssec_status= filter.
func (h *Handler) StreamQueryLog(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	filters := parseStreamFilters(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if n := parseBackfillParam(r.URL.Query().Get("backfill")); n > 0 {
		h.sendBackfill(w, flusher, filters, n)
	}

	if r.URL.Query().Get("cluster") == "true" {
		h.streamCluster(w, flusher, r.Context(), filters)
		return
	}

	h.streamSingleNode(w, flusher, r.Context(), filters, h.ownNodeID())
}

func (h *Handler) streamSingleNode(w http.ResponseWriter, flusher http.Flusher, ctx context.Context, filters streamFilters, nodeID string) {
	ql := h.app.GetQueryLog()
	subID, ch := ql.Subscribe()
	defer ql.Unsubscribe(subID)

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case entry, open := <-ch:
			if !open {
				return
			}
			if !filters.matches(entry) {
				continue
			}
			data, err := json.Marshal(entryToEvent(entry, nodeID))
			if err != nil {
				continue
			}
			writeSSEQuery(w, flusher, data)
		case <-heartbeat.C:
			fmt.Fprintf(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// sendBackfill replays the last n entries from the ring buffer, applying
// filters, then sends "event: backfill_end".
func (h *Handler) sendBackfill(w http.ResponseWriter, flusher http.Flusher, filters streamFilters, n int) {
	nodeID := h.ownNodeID()
	for _, e := range h.app.GetQueryLog().Snapshot(n) {
		if !filters.matches(e) {
			continue
		}
		data, err := json.Marshal(entryToEvent(e, nodeID))
		if err != nil {
			continue
		}
		writeSSEQuery(w, flusher, data)
	}
	fmt.Fprintf(w, "event: backfill_end\ndata: {}\n\n")
	flusher.Flush()
}

func parseBackfillParam(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if n > 500 {
		return 500
	}
	return n
}

func (h *Handler) ownNodeID() string {
	c := h.app.GetCluster()
	if c == nil {
		return ""
	}
	return c.NodeID()
}

// ── M41: cluster-wide fan-in ──────────────────────────────────────────────────

type fanEvent struct {
	nodeID string
	data   []byte // marshalled queryStreamEvent JSON
}

func (h *Handler) streamCluster(w http.ResponseWriter, flusher http.Flusher, ctx context.Context, filters streamFilters) {
	c := h.app.GetCluster()
	if c == nil {
		h.streamSingleNode(w, flusher, ctx, filters, "")
		return
	}

	merged := make(chan fanEvent, 256)
	ownID := c.NodeID()
	secret, _ := c.Store().ClusterSecret()
	members, _ := c.Store().Members()

	var wg sync.WaitGroup

	// Local node.
	wg.Add(1)
	go func() {
		defer wg.Done()
		localFan(ctx, h.app.GetQueryLog(), ownID, filters, merged)
	}()

	// Peers.
	for _, m := range members {
		if m.NodeID == ownID {
			continue
		}
		apiURL := c.MemberAPIURL(m)
		if apiURL == "" {
			continue
		}
		wg.Add(1)
		go func(nodeID, baseURL string) {
			defer wg.Done()
			peerFan(ctx, nodeID, baseURL, secret, filters, merged, w, flusher)
		}(m.NodeID, apiURL)
	}

	go func() { wg.Wait(); close(merged) }()

	dedup := newDedupWindow(100 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-merged:
			if !open {
				return
			}
			if dedup.seen(ev.nodeID, ev.data) {
				continue
			}
			writeSSEQuery(w, flusher, ev.data)
		case <-heartbeat.C:
			fmt.Fprintf(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func localFan(ctx context.Context, ql interface {
	Subscribe() (uint64, <-chan dlog.Entry)
	Unsubscribe(uint64)
}, nodeID string, filters streamFilters, out chan<- fanEvent) {
	id, ch := ql.Subscribe()
	defer ql.Unsubscribe(id)
	for {
		select {
		case <-ctx.Done():
			return
		case e, open := <-ch:
			if !open {
				return
			}
			if !filters.matches(e) {
				continue
			}
			data, err := json.Marshal(entryToEvent(e, nodeID))
			if err != nil {
				continue
			}
			select {
			case out <- fanEvent{nodeID: nodeID, data: data}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// peerFan connects to a peer's SSE stream and forwards events to out.
// baseURL is the peer's routable HTTP base URL (e.g. "http://10.0.0.102:8080").
func peerFan(ctx context.Context, nodeID, baseURL, secret string,
	filters streamFilters, out chan<- fanEvent,
	w io.Writer, flusher http.Flusher) {

	url := baseURL + "/api/v1/query-log/stream"
	if params := buildFilterParams(filters); params != "" {
		url += "?" + params
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		sendNodeUnavailable(w, flusher, nodeID)
		return
	}
	req.Header.Set("X-Cluster-Secret", secret)

	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		sendNodeUnavailable(w, flusher, nodeID)
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := ensureNodeID(strings.TrimPrefix(line, "data: "), nodeID)
		select {
		case out <- fanEvent{nodeID: nodeID, data: []byte(payload)}:
		case <-ctx.Done():
			return
		}
	}
}

func buildFilterParams(f streamFilters) string {
	var parts []string
	if f.profileID != "" {
		parts = append(parts, "profile_id="+f.profileID)
	}
	if f.result != "" {
		parts = append(parts, "result="+f.result)
	}
	if f.domain != "" {
		parts = append(parts, "domain="+f.domain)
	}
	if f.dnssecStatus != "" {
		parts = append(parts, "dnssec_status="+f.dnssecStatus)
	}
	return strings.Join(parts, "&")
}

func sendNodeUnavailable(w io.Writer, flusher http.Flusher, nodeID string) {
	data, _ := json.Marshal(map[string]string{"node_id": nodeID})
	fmt.Fprintf(w, "event: node_unavailable\ndata: %s\n\n", data)
	flusher.Flush()
}

func ensureNodeID(payload, nodeID string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return payload
	}
	if v, ok := m["node_id"]; !ok || v == "" {
		m["node_id"] = nodeID
		b, err := json.Marshal(m)
		if err != nil {
			return payload
		}
		return string(b)
	}
	return payload
}

// ── Dedup window ──────────────────────────────────────────────────────────────

type dedupWindow struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]time.Time
}

func newDedupWindow(ttl time.Duration) *dedupWindow {
	return &dedupWindow{ttl: ttl, entries: make(map[string]time.Time)}
}

func (d *dedupWindow) seen(nodeID string, data []byte) bool {
	key := nodeID + "|" + string(data)
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, exp := range d.entries {
		if now.After(exp) {
			delete(d.entries, k)
		}
	}
	if _, ok := d.entries[key]; ok {
		return true
	}
	d.entries[key] = now.Add(d.ttl)
	return false
}
