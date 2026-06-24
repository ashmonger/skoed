package log

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Outcome describes the result of a DNS query.
type Outcome string

const (
	OutcomeForwarded Outcome = "forwarded"
	OutcomeBlocked   Outcome = "blocked"
	OutcomeLocal     Outcome = "local"
	OutcomeCached    Outcome = "cached"
)

// Entry is a single DNS query log record.
type Entry struct {
	ID          string // hex-encoded 16 random bytes
	Timestamp   time.Time
	Client      string // IP address string
	Domain      string
	QueryType   string // "A", "AAAA", "MX", etc.
	Outcome     Outcome
	BlocklistID string // set when Outcome == OutcomeBlocked
	Category    string // M3: "", "doh-probe", "doh-canary", "ddr-probe"
	ProfileID   string // M3: which profile decided the outcome (best-effort)
	PauseActive bool   // M13: true when filtering was paused for this query
	// M3.6 — DHCP enrichment. All optional; absent when no DHCP lease
	// matched the client IP.
	ClientHostname string `json:",omitempty"`
	ClientMAC      string `json:",omitempty"`
	ClientID       string `json:",omitempty"`
	// M21 — DNSSEC validation status. "ok", "bogus", "insecure", "indeterminate", or "" (transparent mode).
	DnssecStatus string `json:"dnssec_status,omitempty"`
}

// newEntryID generates a random 16-byte hex string for use as an entry ID.
func newEntryID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck — crypto/rand.Read never returns an error
	return hex.EncodeToString(b)
}

// QueryLog is a bounded in-memory ring buffer of DNS query entries.
// It is safe for concurrent use.
type QueryLog struct {
	mu          sync.Mutex
	entries     []Entry
	maxEntries  int
	observer    func(Entry)        // optional fan-out for cluster aggregator
	subscribers map[uint64]chan Entry
	nextSubID   uint64
}

// New creates a QueryLog with the given maximum number of entries.
func New(maxEntries int) *QueryLog {
	if maxEntries < 0 {
		maxEntries = 0
	}
	return &QueryLog{
		entries:     make([]Entry, 0, maxEntries),
		maxEntries:  maxEntries,
		subscribers: make(map[uint64]chan Entry),
	}
}

// SetObserver registers fn to be called after every Append. Used by the
// cluster aggregator to count events as they happen without polling the log.
// fn must be fast (it runs under Append's lock); offload heavy work.
func (l *QueryLog) SetObserver(fn func(Entry)) {
	l.mu.Lock()
	l.observer = fn
	l.mu.Unlock()
}

// Append adds an entry to the log. If the log is at capacity, the oldest
// entry is dropped to make room.
func (l *QueryLog) Append(e Entry) {
	if e.ID == "" {
		e.ID = newEntryID()
	}
	l.mu.Lock()
	if l.maxEntries > 0 {
		if len(l.entries) >= l.maxEntries {
			copy(l.entries, l.entries[1:])
			l.entries = l.entries[:len(l.entries)-1]
		}
		l.entries = append(l.entries, e)
	}
	obs := l.observer
	// Snapshot subscriber channels while holding the lock, then send without it.
	subs := make([]chan Entry, 0, len(l.subscribers))
	for _, ch := range l.subscribers {
		subs = append(subs, ch)
	}
	l.mu.Unlock()
	if obs != nil {
		obs(e)
	}
	for _, ch := range subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop rather than block Append
		}
	}
}

// Subscribe registers a new SSE stream subscriber. Returns an id used to
// Unsubscribe and a channel that receives each appended entry. The channel is
// buffered; slow consumers may miss entries if they fall behind.
func (l *QueryLog) Subscribe() (uint64, <-chan Entry) {
	ch := make(chan Entry, 64)
	l.mu.Lock()
	id := l.nextSubID
	l.nextSubID++
	l.subscribers[id] = ch
	l.mu.Unlock()
	return id, ch
}

// Unsubscribe removes and closes the subscriber channel registered under id.
func (l *QueryLog) Unsubscribe(id uint64) {
	l.mu.Lock()
	if ch, ok := l.subscribers[id]; ok {
		delete(l.subscribers, id)
		close(ch)
	}
	l.mu.Unlock()
}

// Query returns entries matching the optional filters in reverse chronological
// order (newest first). An empty client or outcome string means "no filter".
// limit=0 means no cap on the number of results returned.
// total is the count of matching entries before the limit/offset are applied.
func (l *QueryLog) Query(client, outcome string, limit, offset int) (entries []Entry, total int) {
	l.mu.Lock()
	snapshot := make([]Entry, len(l.entries))
	copy(snapshot, l.entries)
	l.mu.Unlock()

	// Collect matching entries in reverse chronological order (newest first).
	matched := make([]Entry, 0, len(snapshot))
	for i := len(snapshot) - 1; i >= 0; i-- {
		e := snapshot[i]
		if client != "" && e.Client != client {
			continue
		}
		if outcome != "" && string(e.Outcome) != outcome {
			continue
		}
		matched = append(matched, e)
	}

	total = len(matched)

	// Apply offset.
	if offset > 0 {
		if offset >= len(matched) {
			return []Entry{}, total
		}
		matched = matched[offset:]
	}

	// Apply limit.
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}

	return matched, total
}

// SetMaxEntries updates the retention limit. If the log currently holds more
// entries than the new limit, the oldest are discarded.
func (l *QueryLog) SetMaxEntries(n int) {
	if n < 0 {
		n = 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxEntries = n
	if len(l.entries) > n {
		excess := len(l.entries) - n
		l.entries = l.entries[excess:]
	}
}

// MaxEntries returns the current retention limit.
func (l *QueryLog) MaxEntries() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxEntries
}
