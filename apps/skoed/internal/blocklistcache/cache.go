// Package blocklistcache keeps this node's copy of the downloaded content of
// URL blocklists under <data_dir>/blocklists (M50,
// TS-BlocklistContentDistribution §2.2). The content is not replicated
// through Raft: the replicated state only carries its SHA-256, and each
// node holds the file with that hash.
//
// A file holds one normalized domain per line (lower-cased, deduplicated,
// sorted, LF), so two nodes that normalize the same input produce the same
// bytes and the same hash. Files are written to a temporary file, fsynced,
// then renamed over the target.
package blocklistcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter"
)

// maxFileBytes bounds a copy received from a peer. A normalized list is
// smaller than the downloaded one, which is bounded to 64 MiB.
const maxFileBytes = 64 << 20

const fileSuffix = ".list"

// Entry describes the copy of one list held by this node.
type Entry struct {
	SHA256 string // hex
	Count  int    // number of domains
}

// ErrHashMismatch is returned by Install when the received bytes do not
// have the expected SHA-256.
var ErrHashMismatch = errors.New("content hash mismatch")

// Cache is the set of list files of this node. The hash and count of each
// file are computed once, when it is written or loaded, and kept in memory.
type Cache struct {
	dir string

	mu        sync.RWMutex
	entries   map[string]Entry
	writtenAt map[string]time.Time // set by Write and Install

	listenersMu sync.Mutex
	listeners   []func()
}

// Open creates <dataDir>/blocklists (mode 0700) if needed and loads the
// hash of every file in it. A file that cannot be read is deleted, so its
// list is reported as missing. Leftover temporary files are deleted.
func Open(dataDir string) (*Cache, error) {
	dir := filepath.Join(dataDir, "blocklists")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod %s: %w", dir, err)
	}
	c := &Cache{dir: dir, entries: map[string]Entry{}, writtenAt: map[string]time.Time{}}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, de := range names {
		name := de.Name()
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(path)
			continue
		}
		if !strings.HasSuffix(name, fileSuffix) || !de.Type().IsRegular() {
			continue
		}
		id, err := url.PathUnescape(strings.TrimSuffix(name, fileSuffix))
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("blocklistcache: %s is unreadable, deleting it: %v", path, err)
			_ = os.Remove(path)
			continue
		}
		c.entries[id] = entryOf(data)
	}
	return c, nil
}

// Dir returns the cache directory.
func (c *Cache) Dir() string { return c.dir }

// OnChange registers fn, called after a file is written, installed or
// removed. Used to rebuild the filter engine.
func (c *Cache) OnChange(fn func()) {
	c.listenersMu.Lock()
	c.listeners = append(c.listeners, fn)
	c.listenersMu.Unlock()
}

func (c *Cache) changed() {
	c.listenersMu.Lock()
	fns := append([]func(){}, c.listeners...)
	c.listenersMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// Encode normalizes domains (lower-cased, trimmed, deduplicated, sorted)
// and returns the file content with its entry.
func Encode(domains []string) ([]byte, Entry) {
	set := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			set[d] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(set))
	for d := range set {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	var b bytes.Buffer
	for _, d := range sorted {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	data := b.Bytes()
	sum := sha256.Sum256(data)
	return data, Entry{SHA256: hex.EncodeToString(sum[:]), Count: len(sorted)}
}

func entryOf(data []byte) Entry {
	sum := sha256.Sum256(data)
	return Entry{SHA256: hex.EncodeToString(sum[:]), Count: bytes.Count(data, []byte{'\n'})}
}

// Get returns the entry of list id.
func (c *Cache) Get(id string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	return e, ok
}

// Has reports whether this node holds the version sha256 of list id.
func (c *Cache) Has(id, sha256 string) bool {
	e, ok := c.Get(id)
	return ok && e.SHA256 == sha256
}

// Entries returns a copy of every entry, keyed by list ID.
func (c *Cache) Entries() map[string]Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]Entry, len(c.entries))
	for k, v := range c.entries {
		out[k] = v
	}
	return out
}

func (c *Cache) path(id string) string {
	return filepath.Join(c.dir, url.PathEscape(id)+fileSuffix)
}

// validID refuses IDs that cannot name a file in the cache directory.
func validID(id string) error {
	if id == "" || id == "." || id == ".." {
		return fmt.Errorf("invalid blocklist id %q", id)
	}
	return nil
}

// Write normalizes domains and stores them as the copy of list id, unless
// the copy already has the same hash.
func (c *Cache) Write(id string, domains []string) (Entry, error) {
	if err := validID(id); err != nil {
		return Entry{}, err
	}
	data, e := Encode(domains)
	if c.Has(id, e.SHA256) {
		return e, nil
	}
	tmp, err := os.CreateTemp(c.dir, url.PathEscape(id)+".*.tmp")
	if err != nil {
		return Entry{}, fmt.Errorf("create tmp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return Entry{}, fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := c.commit(id, tmp, e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Install stores the content read from r as the copy of list id. The
// content is written to a temporary file while it is hashed; when its
// SHA-256 is not wantSHA256, the temporary file is deleted, the previous
// copy is kept and ErrHashMismatch is returned.
func (c *Cache) Install(id string, r io.Reader, wantSHA256 string) (Entry, error) {
	if err := validID(id); err != nil {
		return Entry{}, err
	}
	tmp, err := os.CreateTemp(c.dir, url.PathEscape(id)+".*.tmp")
	if err != nil {
		return Entry{}, fmt.Errorf("create tmp: %w", err)
	}
	h := sha256.New()
	lines := &lineCounter{}
	n, err := io.Copy(io.MultiWriter(tmp, h, lines), io.LimitReader(r, maxFileBytes+1))
	if err == nil && n > maxFileBytes {
		err = fmt.Errorf("content larger than %d bytes", maxFileBytes)
	}
	if err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return Entry{}, err
	}
	e := Entry{SHA256: hex.EncodeToString(h.Sum(nil)), Count: lines.n}
	if e.SHA256 != wantSHA256 {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return Entry{}, fmt.Errorf("%w: got %s, want %s", ErrHashMismatch, e.SHA256, wantSHA256)
	}
	if err := c.commit(id, tmp, e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// commit fsyncs and closes tmp, sets its mode to 0600, and renames it over
// the file of list id.
func (c *Cache) commit(id string, tmp *os.File, e Entry) error {
	name := tmp.Name()
	fail := func(format string, err error) error {
		_ = os.Remove(name)
		return fmt.Errorf(format, name, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fail("chmod %s: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fail("fsync %s: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fail("close %s: %w", err)
	}
	c.mu.Lock()
	err := os.Rename(name, c.path(id))
	if err == nil {
		c.entries[id] = e
		c.writtenAt[id] = time.Now()
	}
	c.mu.Unlock()
	if err != nil {
		return fail("rename %s: %w", err)
	}
	c.changed()
	return nil
}

// Domains returns the domains of this node's copy of list id, whatever its
// version (filter.ContentSource). ok is false when there is no copy.
func (c *Cache) Domains(id, _ string) ([]string, bool) {
	c.mu.RLock()
	_, ok := c.entries[id]
	var data []byte
	var err error
	if ok {
		data, err = os.ReadFile(c.path(id))
	}
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if err != nil {
		log.Printf("blocklistcache: read copy of %q: %v", id, err)
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), true
}

// OpenFile opens this node's copy of list id for reading, with its entry.
// The file and the entry are read under the same lock, so they match. The
// caller closes the file.
func (c *Cache) OpenFile(id string) (*os.File, Entry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	if !ok {
		return nil, Entry{}, os.ErrNotExist
	}
	f, err := os.Open(c.path(id))
	if err != nil {
		return nil, Entry{}, err
	}
	return f, e, nil
}

// pruneGrace protects a copy written just before its list is committed:
// the create handler writes the file of a new list, then commits it
// through Raft.
const pruneGrace = time.Minute

// Prune deletes the copy of every list for which keep returns false,
// except copies written in the last minute.
func (c *Cache) Prune(keep func(id string) bool) {
	c.mu.Lock()
	removed := false
	for id := range c.entries {
		if keep(id) || time.Since(c.writtenAt[id]) < pruneGrace {
			continue
		}
		if err := os.Remove(c.path(id)); err != nil && !os.IsNotExist(err) {
			log.Printf("blocklistcache: remove copy of %q: %v", id, err)
			continue
		}
		delete(c.entries, id)
		delete(c.writtenAt, id)
		removed = true
	}
	c.mu.Unlock()
	if removed {
		c.changed()
	}
}

// WriteLegacy stores the replicated domains of a URL list written by a
// previous version (config.Blocklist.IsLegacyURLContent) as the copy of
// that list (TS-BlocklistContentDistribution §6). Every node normalizes
// the same domains, so they all get the same hash.
func (c *Cache) WriteLegacy(bl config.Blocklist) (Entry, error) {
	return c.Write(bl.ID, filter.ParseStoredDomains(bl.Domains, bl.Source.Format))
}

// SeedLegacy writes a copy for every legacy URL list of cfg. With
// onlyMissing, lists that already have a copy, of any version, are
// skipped, which keeps the check cheap enough to run on every sync pass.
func (c *Cache) SeedLegacy(cfg *config.Config, onlyMissing bool) {
	for _, bl := range cfg.Filtering.Blocklists {
		if !bl.IsLegacyURLContent() {
			continue
		}
		if onlyMissing {
			if _, ok := c.Get(bl.ID); ok {
				continue
			}
		}
		if _, err := c.WriteLegacy(bl); err != nil {
			log.Printf("blocklistcache: seed copy of %q from the replicated domains: %v", bl.ID, err)
		}
	}
}

// lineCounter counts the LF bytes written to it.
type lineCounter struct{ n int }

func (l *lineCounter) Write(p []byte) (int, error) {
	l.n += bytes.Count(p, []byte{'\n'})
	return len(p), nil
}
