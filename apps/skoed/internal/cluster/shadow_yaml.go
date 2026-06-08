package cluster

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/config"
	"gopkg.in/yaml.v3"
)

// defaultShadowDebounce is the default coalescing window between an FSM apply
// signal and the resulting on-disk write.
const defaultShadowDebounce = 1 * time.Second

// ShadowWriter mirrors bbolt state to <data_dir>/config.yaml after every FSM
// apply so filesystem-level backup tools (PBS, restic) capture a coherent
// human-readable M1-format snapshot of the replicated configuration.
//
// Writes are debounced: a burst of applies within the debounce window collapses
// into a single write reflecting the latest committed state. Failed writes are
// logged and never block the FSM.
type ShadowWriter struct {
	cluster  *Cluster
	debounce time.Duration
	path     string

	signal chan struct{}
	done   chan struct{}
	wg     sync.WaitGroup

	startOnce sync.Once
	stopOnce  sync.Once
}

// NewShadowWriter creates a writer bound to the cluster. The caller must
// invoke Start to spawn the writer goroutine. A zero debounce defaults to
// one second.
func NewShadowWriter(c *Cluster, debounce time.Duration) *ShadowWriter {
	if debounce <= 0 {
		debounce = defaultShadowDebounce
	}
	return &ShadowWriter{
		cluster:  c,
		debounce: debounce,
		path:     c.Node().DataPath("config.yaml"),
		signal:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

// Start spawns the writer goroutine and subscribes to FSM applies. It is safe
// to call Start more than once; only the first call has effect.
func (w *ShadowWriter) Start() {
	w.startOnce.Do(func() {
		// Subscribe before spawning so we never miss an apply that lands
		// between subscription and goroutine launch.
		w.cluster.Subscribe(w.notify)

		w.wg.Add(1)
		go w.run()

		// Initial write on startup to overwrite any stale YAML left by a
		// previous crash.
		w.notify()
	})
}

// Stop terminates the writer cleanly, draining any pending write first.
func (w *ShadowWriter) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		w.wg.Wait()
	})
}

// notify is invoked by the FSM apply callback. It is non-blocking and
// coalescing: if a signal is already pending, the new one is dropped because
// the writer will pick up the latest snapshot on the next debounce.
func (w *ShadowWriter) notify() {
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

// run is the writer loop. It waits for a signal, then waits up to debounce
// for further signals before snapshotting and writing.
func (w *ShadowWriter) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.done:
			// Drain a pending signal so a Stop() right after a mutation
			// still flushes the latest state to disk.
			select {
			case <-w.signal:
				w.writeOnce()
			default:
			}
			return
		case <-w.signal:
			w.debounceAndWrite()
		}
	}
}

// debounceAndWrite waits up to the debounce window for additional signals,
// dropping them so the eventual write reflects the latest committed state.
func (w *ShadowWriter) debounceAndWrite() {
	timer := time.NewTimer(w.debounce)
	defer timer.Stop()
	for {
		select {
		case <-w.done:
			w.writeOnce()
			return
		case <-w.signal:
			// Coalesce: a further signal arrived inside the window; keep
			// waiting until the timer fires with no new activity.
		case <-timer.C:
			w.writeOnce()
			return
		}
	}
}

// writeOnce snapshots the store and atomically replaces config.yaml. Failures
// are logged but never propagated — the shadow file is best-effort and must
// not interfere with cluster operation.
func (w *ShadowWriter) writeOnce() {
	if err := w.snapshotAndWrite(); err != nil {
		log.Printf("shadow_yaml: write failed: %v", err)
	}
}

// clusterSections mirrors the cluster-replicated fields of *config.Config.
// We deliberately omit `version` (the doc carries `schema_version` at root)
// AND `api` (the API port moved to `node.api_address` in M2 and is no
// longer cluster-replicated — it's node-local).
type clusterSections struct {
	DNS       config.DNSConfig       `yaml:"dns"`
	Filtering config.FilteringConfig `yaml:"filtering"`
	LocalDNS  config.LocalDNSConfig  `yaml:"local_dns"`
	QueryLog  config.QueryLogConfig  `yaml:"query_log"`
	Auth      config.AuthConfig      `yaml:"auth"`
}

// shadowDoc is the on-disk merged YAML shape: a node-local section, an
// optional bootstrap section, and the cluster-replicated sections inlined at
// the top level.
type shadowDoc struct {
	SchemaVersion   int               `yaml:"schema_version"`
	Node            NodeSection       `yaml:"node"`
	Bootstrap       *BootstrapSection `yaml:"bootstrap,omitempty"`
	clusterSections `yaml:",inline"`
}

func (w *ShadowWriter) snapshotAndWrite() error {
	snap, err := w.cluster.Store().Snapshot()
	if err != nil {
		return fmt.Errorf("snapshot store: %w", err)
	}
	// Node-local DNS listen is never replicated; defensively zero it on the
	// snapshot copy in case a future store change ever leaks it through.
	snap.DNS.Listen = config.ListenConfig{}

	node := w.cluster.Node()
	doc := shadowDoc{
		SchemaVersion: 1,
		Node:          node.Node,
		clusterSections: clusterSections{
			DNS:       snap.DNS,
			Filtering: snap.Filtering,
			LocalDNS:  snap.LocalDNS,
			QueryLog:  snap.QueryLog,
			Auth:      snap.Auth,
		},
	}
	if node.Bootstrap.LeaderAddress != "" || node.Bootstrap.Token != "" {
		bs := node.Bootstrap
		doc.Bootstrap = &bs
	}

	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	return atomicWriteFile(w.path, data)
}

// atomicWriteFile writes data to a sibling tmp file, fsyncs it, then renames
// over the target so readers never observe a partial file.
func atomicWriteFile(target string, data []byte) error {
	tmp := target + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
