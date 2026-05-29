package dns

import (
	"container/list"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const defaultCacheTTL = 60 * time.Second

type cacheKey struct {
	Name  string
	Qtype uint16
}

type cacheEntry struct {
	msg       *dns.Msg
	expiresAt time.Time
}

// Cache is a thread-safe, LRU-evicting DNS response cache.
type Cache struct {
	mu         sync.Mutex
	maxEntries int
	items      map[cacheKey]*list.Element
	order      *list.List // front = most recently used
}

type cacheListItem struct {
	key   cacheKey
	entry cacheEntry
}

// NewCache creates a Cache with the given maximum number of entries.
func NewCache(maxEntries int) *Cache {
	return newCache(maxEntries)
}

// newCache creates a Cache with the given maximum number of entries.
func newCache(maxEntries int) *Cache {
	return &Cache{
		maxEntries: maxEntries,
		items:      make(map[cacheKey]*list.Element, maxEntries),
		order:      list.New(),
	}
}

// get returns a copy of the cached response for key if it exists and has not
// expired. The copy has TTLs decremented to reflect remaining lifetime.
func (c *Cache) get(key cacheKey) (*dns.Msg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	item := elem.Value.(*cacheListItem)
	remaining := time.Until(item.entry.expiresAt)
	if remaining <= 0 {
		// Expired: evict.
		c.order.Remove(elem)
		delete(c.items, key)
		return nil, false
	}

	// Move to front (most recently used).
	c.order.MoveToFront(elem)

	// Return a copy with decremented TTLs.
	out := item.entry.msg.Copy()
	remainSecs := uint32(remaining.Seconds())
	for _, rr := range out.Answer {
		hdr := rr.Header()
		if hdr.Ttl > remainSecs {
			hdr.Ttl = remainSecs
		}
	}
	for _, rr := range out.Ns {
		hdr := rr.Header()
		if hdr.Ttl > remainSecs {
			hdr.Ttl = remainSecs
		}
	}
	for _, rr := range out.Extra {
		hdr := rr.Header()
		if hdr.Ttl > remainSecs {
			hdr.Ttl = remainSecs
		}
	}
	return out, true
}

// set stores a DNS response in the cache, keyed by key. Only successful
// responses with a non-empty answer section are cached. TTL is derived from
// the first answer RR; if none is present the default of 60 s is used.
func (c *Cache) set(key cacheKey, msg *dns.Msg) {
	if msg == nil {
		return
	}
	if msg.Rcode != dns.RcodeSuccess {
		return
	}
	if len(msg.Answer) == 0 {
		return
	}

	ttl := defaultCacheTTL
	if firstTTL := msg.Answer[0].Header().Ttl; firstTTL > 0 {
		ttl = time.Duration(firstTTL) * time.Second
	}

	entry := cacheEntry{
		msg:       msg.Copy(),
		expiresAt: time.Now().Add(ttl),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		// Update existing entry.
		elem.Value.(*cacheListItem).entry = entry
		c.order.MoveToFront(elem)
		return
	}

	// Evict LRU entry if at capacity.
	if c.maxEntries > 0 && c.order.Len() >= c.maxEntries {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheListItem).key)
		}
	}

	elem := c.order.PushFront(&cacheListItem{key: key, entry: entry})
	c.items[key] = elem
}

// size returns the number of entries currently in the cache.
func (c *Cache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
