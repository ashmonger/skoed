package dns

import (
	"sync"
	"time"
)

const (
	// deviceNewCheckInterval is how long a client IP is skipped after it
	// was queued. It matches the webhook dispatcher's 10-minute dedupe, and
	// it bounds the delay before an IP whose new-dynamic-client entry was
	// removed (e.g. by a Raft snapshot restore) is recorded again.
	deviceNewCheckInterval = 10 * time.Minute
	// deviceNewQueueSize is the number of IPs waiting for the worker.
	// When the queue is full, new IPs are dropped and queued by a later
	// query, so a flood of spoofed source addresses cannot create
	// unbounded goroutines or store writes.
	deviceNewQueueSize = 1024
	// deviceNewMaxChecked caps the checked-IP map at 65,536 entries
	// (a few MB at most).
	deviceNewMaxChecked = 65536
)

// DeviceNewQueue runs a device-new callback off the DNS path. Notify queues
// a client IP at most once per deviceNewCheckInterval, and a single worker
// goroutine runs the callback for each queued IP, one at a time.
type DeviceNewQueue struct {
	work       func(clientIP string)
	queue      chan string
	maxChecked int
	now        func() time.Time

	mu      sync.Mutex
	checked map[string]time.Time // IP → when it was queued

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewDeviceNewQueue returns a DeviceNewQueue and starts its worker, which
// runs work for each queued IP until Stop is called.
func NewDeviceNewQueue(work func(clientIP string)) *DeviceNewQueue {
	q := newDeviceNewQueue(work, deviceNewQueueSize, deviceNewMaxChecked)
	go q.run()
	return q
}

func newDeviceNewQueue(work func(string), size, maxChecked int) *DeviceNewQueue {
	return &DeviceNewQueue{
		work:       work,
		queue:      make(chan string, size),
		maxChecked: maxChecked,
		now:        time.Now,
		checked:    make(map[string]time.Time),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Notify queues clientIP for the worker unless it was queued within the
// last deviceNewCheckInterval. It never blocks: when the queue is full, the
// IP is dropped and left unmarked, so a later query queues it.
func (q *DeviceNewQueue) Notify(clientIP string) {
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	if at, ok := q.checked[clientIP]; ok && now.Sub(at) < deviceNewCheckInterval {
		return
	}
	select {
	case q.queue <- clientIP:
	default:
		return
	}
	if len(q.checked) >= q.maxChecked {
		q.prune(now)
	}
	q.checked[clientIP] = now
}

// prune removes the expired entries of the checked map. When more than half
// of the entries are still recent, it clears the map instead, so pruning
// does not run on every insert: those IPs are then queued once more.
func (q *DeviceNewQueue) prune(now time.Time) {
	for ip, at := range q.checked {
		if now.Sub(at) >= deviceNewCheckInterval {
			delete(q.checked, ip)
		}
	}
	if len(q.checked) > q.maxChecked/2 {
		clear(q.checked)
	}
}

// Stop stops the worker and waits for the current callback to return.
// IPs still queued are not processed. Notify stays safe to call after Stop.
func (q *DeviceNewQueue) Stop() {
	q.stopOnce.Do(func() { close(q.stop) })
	<-q.done
}

func (q *DeviceNewQueue) run() {
	defer close(q.done)
	for {
		select {
		case <-q.stop:
			return
		case ip := <-q.queue:
			q.work(ip)
		}
	}
}
