package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// wsUpgrader allows all origins; auth is enforced by the Auth middleware before
// the upgrade, so no additional origin check is needed here.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

// StreamQueryLogWS handles GET /api/v1/query-log/ws.
// It provides the same live DNS query stream as the SSE endpoint but over a
// WebSocket connection, making it accessible in environments where SSE is
// stripped by reverse proxies.
//
// Authentication is performed by the Auth middleware before the upgrade.
// Same filters as SSE: ?profile_id=, ?result=, ?domain=, ?dnssec_status=.
// Keep-alive: {"type":"keep-alive"} text frames every 15 s.
func (h *Handler) StreamQueryLogWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade failure writes an HTTP error response automatically.
		return
	}
	defer conn.Close()

	filters := parseStreamFilters(r)
	nodeID := h.ownNodeID()

	ql := h.app.GetQueryLog()
	subID, ch := ql.Subscribe()
	defer ql.Unsubscribe(subID)

	// Discard all incoming frames (read-only stream); close on any read error.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

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
			if !filters.matches(entry) {
				continue
			}
			data, err := json.Marshal(entryToEvent(entry, nodeID))
			if err != nil {
				continue
			}
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-heartbeat.C:
			ka, _ := json.Marshal(map[string]string{"type": "keep-alive"})
			if err := conn.WriteMessage(websocket.TextMessage, ka); err != nil {
				return
			}
		}
	}
}
