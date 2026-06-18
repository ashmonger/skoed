// Package sse implements a fan-out broadcaster for Server-Sent Events clients.
// Multiple goroutines may call Publish concurrently; slow clients that cannot
// keep up are dropped rather than blocking the publisher.
package sse

import "sync"

// Broadcaster fans event payloads out to all currently connected SSE clients.
type Broadcaster struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

// NewBroadcaster returns an empty, ready-to-use Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{clients: make(map[chan []byte]struct{})}
}

// Subscribe registers a new SSE client and returns a channel on which event
// payloads are delivered and a cancel function the caller must invoke when the
// client disconnects. Each client channel has a buffer of 32 events.
func (b *Broadcaster) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

// Publish sends payload to every subscribed client. Clients whose buffer is
// full are skipped — their stale connection will be detected by the HTTP
// layer and cleaned up via the cancel function.
func (b *Broadcaster) Publish(payload []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- payload:
		default:
		}
	}
}
