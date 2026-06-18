package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/api/sse"
)

// EventsHandler streams skoed events to SSE clients (M22.5 TS-BrowserExtension).
type EventsHandler struct {
	broadcaster *sse.Broadcaster
}

// NewEventsHandler creates an EventsHandler backed by the given broadcaster.
func NewEventsHandler(b *sse.Broadcaster) *EventsHandler {
	return &EventsHandler{broadcaster: b}
}

// StreamEvents handles GET /api/v1/events. It upgrades the connection to a
// persistent Server-Sent Events stream and delivers every event published to
// the broadcaster until the client disconnects.
func (h *EventsHandler) StreamEvents(w http.ResponseWriter, r *http.Request) {
	if h.broadcaster == nil {
		http.Error(w, "SSE broadcaster not initialised", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := h.broadcaster.Subscribe()
	defer cancel()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case payload, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "%s\n", payload)
			flusher.Flush()

		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
