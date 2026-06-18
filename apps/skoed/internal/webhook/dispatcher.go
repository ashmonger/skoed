// Package webhook implements the M22 push-alert dispatcher. Events fired
// anywhere in the skoed process are fanned out to all matching, enabled
// webhook endpoints. Delivery is asynchronous with a buffered queue; each
// endpoint retries up to four times with exponential back-off.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/config"
)


// EventType is the string label that identifies what happened.
type EventType string

const (
	EventDeviceNew           EventType = "device.new"
	EventBlocklistDownFailed EventType = "blocklist.download_failed"
	EventClusterNodeDown     EventType = "cluster.node_down"
	EventClusterNodeRejoined EventType = "cluster.node_rejoined"
	EventFilterPauseStarted  EventType = "filter.pause_started"
	EventFilterPauseExpired  EventType = "filter.pause_expired"
	EventTest                EventType = "webhook.test"
)

// Event is the JSON body posted to each webhook endpoint.
type Event struct {
	ID        string    `json:"id"`
	Event     EventType `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	NodeID    string    `json:"node_id,omitempty"`
	Data      any       `json:"data"`
}

type delivery struct {
	endpoint config.WebhookEndpoint
	event    Event
}

// Dispatcher fans out events to registered webhook endpoints. It is safe for
// concurrent use from multiple goroutines.
type Dispatcher struct {
	// endpoints is called each time an event is fired to get the current list.
	// Using a function lets the caller wire it to the live app config so
	// endpoint additions/removals take effect without restarting the dispatcher.
	endpoints func() []config.WebhookEndpoint

	httpClient *http.Client
	queue      chan delivery

	// auditSink receives the final delivery failure after all retries are
	// exhausted. Optional; nil disables the audit callback.
	auditSink func(endpointID string, eventType EventType, err error)

	// device.new deduplication: suppress duplicate fires within 10 minutes
	// for the same client IP to avoid flooding endpoints on noisy networks.
	seenMu  sync.Mutex
	seenIPs map[string]time.Time

	// sseSink is an optional callback invoked for every fired event so that
	// SSE clients receive events in real time alongside webhook delivery.
	// Set via SetSSESink; nil disables the SSE fan-out.
	sseSink func(payload []byte)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a Dispatcher. endpoints must be non-nil. Call Start to begin
// processing deliveries.
func New(endpoints func() []config.WebhookEndpoint) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		endpoints:  endpoints,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		queue:      make(chan delivery, 256),
		seenIPs:    make(map[string]time.Time),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// SetSSESink wires a callback invoked for every event so SSE clients receive
// events in real time. Call before Start.
func (d *Dispatcher) SetSSESink(fn func(payload []byte)) { d.sseSink = fn }

// SetAuditSink wires a callback invoked when all delivery retries are
// exhausted. The callback receives the endpoint ID, the event type, and the
// final error. Call before Start.
func (d *Dispatcher) SetAuditSink(fn func(endpointID string, eventType EventType, err error)) {
	d.auditSink = fn
}

// Start launches 8 delivery worker goroutines. Each worker drains the queue
// in order; the combined pool provides concurrency without in-order
// guarantees across endpoints.
func (d *Dispatcher) Start() {
	for i := 0; i < 8; i++ {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			for dl := range d.queue {
				d.deliver(dl)
			}
		}()
	}
}

// Stop signals the workers and waits for in-flight deliveries to finish.
func (d *Dispatcher) Stop() {
	d.cancel()
	close(d.queue)
	d.wg.Wait()
}

// Fire dispatches eventType to every enabled endpoint whose event filter
// matches. Fast, non-blocking: events that do not fit in the buffer are
// silently dropped rather than blocking the caller.
func (d *Dispatcher) Fire(eventType EventType, payload any) {
	eps := d.endpoints()
	ev := Event{
		ID:        newEventID(),
		Event:     eventType,
		Timestamp: time.Now(),
		Data:      payload,
	}

	// M22.5: publish to SSE clients before queuing webhook deliveries.
	if d.sseSink != nil {
		if b, err := json.Marshal(ev); err == nil {
			frame := formatSSEFrame(string(eventType), b)
			d.sseSink(frame)
		}
	}

	for _, ep := range eps {
		if !ep.Enabled {
			continue
		}
		if !matchesFilter(ep.Events, eventType) {
			continue
		}
		select {
		case d.queue <- delivery{endpoint: ep, event: ev}:
		default:
			// queue full — drop to avoid blocking callers
		}
	}
}

// FireTo sends a test event directly to a specific endpoint by ID, bypassing
// the endpoint's event filter. Returns an error if the endpoint is not found
// or the HTTP call fails (no retries for test deliveries).
func (d *Dispatcher) FireTo(endpointID string, eventType EventType, payload any) error {
	eps := d.endpoints()
	ev := Event{
		ID:        newEventID(),
		Event:     eventType,
		Timestamp: time.Now(),
		Data:      payload,
	}
	for _, ep := range eps {
		if ep.ID == endpointID {
			return d.post(ep, ev)
		}
	}
	return fmt.Errorf("webhook endpoint %q not found", endpointID)
}

// NotifyDeviceNew fires a device.new event for clientIP if the same IP has
// not been seen within the last 10 minutes. This deduplication prevents
// flooding endpoints when a single device issues many DNS queries.
func (d *Dispatcher) NotifyDeviceNew(clientIP string) {
	d.seenMu.Lock()
	last, ok := d.seenIPs[clientIP]
	if ok && time.Since(last) < 10*time.Minute {
		d.seenMu.Unlock()
		return
	}
	d.seenIPs[clientIP] = time.Now()
	d.seenMu.Unlock()
	d.Fire(EventDeviceNew, map[string]string{"client_ip": clientIP})
}

// deliver posts the event to the endpoint with exponential back-off (0 s,
// 1 s, 4 s, 16 s). The auditSink is called only after the final attempt fails.
func (d *Dispatcher) deliver(dl delivery) {
	delays := []time.Duration{0, 1 * time.Second, 4 * time.Second, 16 * time.Second}
	var lastErr error
	for _, delay := range delays {
		if delay > 0 {
			select {
			case <-d.ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		if err := d.post(dl.endpoint, dl.event); err == nil {
			return
		} else {
			lastErr = err
		}
	}
	if d.auditSink != nil {
		d.auditSink(dl.endpoint.ID, dl.event.Event, lastErr)
	}
}

// post serialises the event, signs it, and sends a single HTTP POST.
func (d *Dispatcher) post(ep config.WebhookEndpoint, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	mac := hmac.New(sha256.New, []byte(ep.Secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Skoed-Signature", sig)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// matchesFilter returns true when the endpoint's event list contains the
// event type or the wildcard "*".
func matchesFilter(events []string, eventType EventType) bool {
	for _, e := range events {
		if e == string(eventType) || e == "*" {
			return true
		}
	}
	return false
}

// newEventID returns a cryptographically random 8-byte hex string.
func newEventID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck — crypto/rand.Read never returns an error
	return hex.EncodeToString(b)
}

// formatSSEFrame returns a complete SSE frame for the given event type and
// JSON data payload, ready to write to an http.ResponseWriter.
func formatSSEFrame(eventType string, data []byte) []byte {
	return []byte(fmt.Sprintf("event: %s\ndata: %s\nid: %s\n\n",
		eventType, data, newEventID()))
}
