package dns

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// fakeClock is a manually advanced clock for the device-new queue tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// queued drains the items queued so far without running a worker.
func queued(q *DeviceNewQueue) []string {
	var out []string
	for {
		select {
		case ip := <-q.queue:
			out = append(out, ip)
		default:
			return out
		}
	}
}

func newTestQueue(size, maxChecked int) (*DeviceNewQueue, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	q := newDeviceNewQueue(func(string) {}, size, maxChecked)
	q.now = clk.now
	return q, clk
}

// TestDeviceNewQueueQueuesFirstQueryOnly checks that only the first query of
// an IP within the check interval is queued, so the webhook and the
// new-dynamic-client list see the IP (FS-NewDynamicClientAlertEndpoint)
// without one write per query.
func TestDeviceNewQueueQueuesFirstQueryOnly(t *testing.T) {
	q, _ := newTestQueue(16, 100)
	for range 5 {
		q.Notify("192.0.2.1")
	}
	q.Notify("192.0.2.2")
	got := queued(q)
	if fmt.Sprint(got) != "[192.0.2.1 192.0.2.2]" {
		t.Fatalf("queued %v, want [192.0.2.1 192.0.2.2]", got)
	}
}

// TestDeviceNewQueueRequeuesAfterInterval checks that an IP is queued again
// once the check interval has passed, so an entry removed from the store
// (e.g. by a snapshot restore) is recorded again.
func TestDeviceNewQueueRequeuesAfterInterval(t *testing.T) {
	q, clk := newTestQueue(16, 100)
	q.Notify("192.0.2.1")
	clk.advance(deviceNewCheckInterval - time.Second)
	q.Notify("192.0.2.1")
	if got := queued(q); len(got) != 1 {
		t.Fatalf("queued %v within the interval, want one item", got)
	}
	clk.advance(time.Second)
	q.Notify("192.0.2.1")
	if got := queued(q); len(got) != 1 {
		t.Fatalf("queued %v after the interval, want one item", got)
	}
}

// TestDeviceNewQueueDropsWhenFull checks that Notify drops an IP instead of
// blocking when the queue is full, and does not mark it as checked so a
// later query queues it.
func TestDeviceNewQueueDropsWhenFull(t *testing.T) {
	q, _ := newTestQueue(1, 100)
	q.Notify("192.0.2.1")
	q.Notify("192.0.2.2") // queue full: dropped
	if got := queued(q); fmt.Sprint(got) != "[192.0.2.1]" {
		t.Fatalf("queued %v, want [192.0.2.1]", got)
	}
	q.Notify("192.0.2.2")
	if got := queued(q); fmt.Sprint(got) != "[192.0.2.2]" {
		t.Fatalf("queued %v after the queue drained, want [192.0.2.2]", got)
	}
}

// TestDeviceNewQueueBoundsCheckedIPs checks that the map of checked IPs never
// grows past its limit, and that expired entries are pruned first.
func TestDeviceNewQueueBoundsCheckedIPs(t *testing.T) {
	q, clk := newTestQueue(1000, 10)
	for i := range 8 {
		q.Notify(fmt.Sprintf("192.0.2.%d", i))
	}
	clk.advance(deviceNewCheckInterval)
	q.Notify("198.51.100.1")
	q.Notify("198.51.100.2")
	q.Notify("198.51.100.3") // limit reached: the 8 expired entries go
	if n := len(q.checked); n != 3 {
		t.Fatalf("%d checked IPs after pruning, want 3", n)
	}
	for i := range 100 {
		q.Notify(fmt.Sprintf("203.0.113.%d", i))
		if n := len(q.checked); n > 10 {
			t.Fatalf("%d checked IPs, want at most 10", n)
		}
	}
}

// TestDeviceNewQueueRunsWork checks that the worker started by
// NewDeviceNewQueue runs the callback for a queued IP, and that Stop waits
// for it to finish.
func TestDeviceNewQueueRunsWork(t *testing.T) {
	done := make(chan string, 1)
	q := NewDeviceNewQueue(func(ip string) { done <- ip })
	q.Notify("192.0.2.1")
	select {
	case ip := <-done:
		if ip != "192.0.2.1" {
			t.Fatalf("work ran for %q, want 192.0.2.1", ip)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("work did not run")
	}
	q.Stop()
	q.Stop()              // idempotent
	q.Notify("192.0.2.2") // after Stop: must not block or panic
}

// fakeWriter is a minimal dns.ResponseWriter for handler tests.
type fakeWriter struct {
	dns.ResponseWriter
	remote net.Addr
}

func (w *fakeWriter) RemoteAddr() net.Addr    { return w.remote }
func (w *fakeWriter) WriteMsg(*dns.Msg) error { return nil }

// TestServeDNSCallsOnDeviceNewSynchronously checks that the handler calls the
// device-new hook before ServeDNS returns, without starting a goroutine per
// query. The hook writes a plain variable: under -race, a call from another
// goroutine would be reported.
func TestServeDNSCallsOnDeviceNewSynchronously(t *testing.T) {
	var got string
	h := NewHandler(HandlerConfig{OnDeviceNew: func(ip string) { got = ip }})
	w := &fakeWriter{remote: &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 5353}}
	h.ServeDNS(w, new(dns.Msg)) // no question: FORMERR after the hook
	if got != "192.0.2.1" {
		t.Fatalf("OnDeviceNew got %q, want 192.0.2.1", got)
	}
}

// BenchmarkDeviceNewQueueNotifyKnown measures the per-query cost for an IP
// already checked within the interval.
func BenchmarkDeviceNewQueueNotifyKnown(b *testing.B) {
	q := NewDeviceNewQueue(func(string) {})
	defer q.Stop()
	q.Notify("192.0.2.1")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			q.Notify("192.0.2.1")
		}
	})
}
