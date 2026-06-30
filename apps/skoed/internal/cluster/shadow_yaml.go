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
// human-readable snapshot of the replicated cluster configuration.
//
// # Two config files, two purposes
//
// A running skoed node owns two distinct config.yaml files:
//
//	/etc/skoed/config.yaml       — startup input, read once at boot, never
//	                               written by skoed at runtime. Contains
//	                               node-local settings (data_dir, api_address,
//	                               peer_address, listen port, TLS/ACME, bootstrap
//	                               token). Must be hand-edited by the operator.
//	                               The cluster sections it carries (dns, filtering,
//	                               auth…) are only used on first boot to seed
//	                               bbolt; after that they become stale.
//
//	<data_dir>/config.yaml       — shadow output, rewritten after every Raft
//	  (typically                   apply. Contains the live cluster-replicated
//	   /var/lib/skoed/            settings (auth, dns, filtering, schedules,
//	   config.yaml)               dhcp…) as a human-readable backup. Node-local
//	                               settings (listen addresses, api/peer ports)
//	                               are NOT written here; they remain in
//	                               /etc/skoed/config.yaml on each node.
//
// Changing a setting via the UI/API updates bbolt via Raft, which triggers
// the shadow writer to update /var/lib/skoed/config.yaml. The /etc/ file is
// never touched. To inspect or back up the live cluster configuration, read
// /var/lib/skoed/config.yaml on any in-sync node.
//
// Future: dns.listen already lives in config.DNSConfig (Raft-replicated).
// Making it the authoritative source for all nodes — configured once via the
// UI and applied cluster-wide — is the right long-term direction. It requires
// an API endpoint, a UI section, and per-node DNS listener restart on Raft
// apply. Until then each node reads its listen config from node.dns.listen in
// /etc/skoed/config.yaml.
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

// shadowDNSConfig is the DNS shape written to config.yaml by the shadow writer.
// It deliberately omits Listen: listen addresses are node-local (they live in
// node.dns.listen) and are never replicated via Raft. Writing a zeroed listen
// block (port:0, ipv4:false, ipv6:false) to the file confuses human readers
// into thinking DNS is disabled. upstream_timeout_seconds is omitempty so it
// is absent when the default (3 s) is in effect.
type shadowDNSConfig struct {
	Mode              string                `yaml:"mode"`
	DNSSECMode        string                `yaml:"dnssec_mode,omitempty"`
	UpstreamResolvers []string              `yaml:"upstream_resolvers,omitempty"`
	UpstreamRoutes    []config.UpstreamRoute `yaml:"upstream_routes,omitempty"`
	UpstreamTimeout   int                   `yaml:"upstream_timeout_seconds,omitempty"`
	TrustedSubnets    []string              `yaml:"trusted_subnets,omitempty"`
	Cache             config.CacheConfig    `yaml:"cache"`
}

// clusterSections mirrors the cluster-replicated fields of *config.Config.
// Sections are sorted alphabetically; filtering is last because it can be large
// (blocklists with inline domains). We deliberately omit `version` (the doc
// carries `schema_version` at root) AND `api` (node-local since M2).
type clusterSections struct {
	Auth             config.AuthConfig         `yaml:"auth"`
	DHCPServer       config.DHCPServerConfig   `yaml:"dhcp_server,omitempty"`
	DNS              shadowDNSConfig           `yaml:"dns"`
	LocalDNS         config.LocalDNSConfig     `yaml:"local_dns"`
	QueryLog         config.QueryLogConfig     `yaml:"query_log"`
	ScheduleBindings []config.ScheduleBinding  `yaml:"schedule_bindings,omitempty"`
	Schedules        []config.Schedule         `yaml:"schedules,omitempty"`
	Filtering        config.FilteringConfig    `yaml:"filtering"`
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

	// block_page is only started when policy is "redirect"; omit the section
	// for other policies so port 8053 does not falsely imply a running server.
	filtering := snap.Filtering
	if filtering.BlockPolicy != "redirect" {
		filtering.BlockPage = config.BlockPageConfig{}
	}
	// Strip runtime refresh state — these track scheduler outcomes and belong
	// in status endpoints, not in a config backup that users may hand-edit.
	for i := range filtering.Blocklists {
		filtering.Blocklists[i].LastRefreshAt = ""
		filtering.Blocklists[i].LastRefreshStatus = ""
		filtering.Blocklists[i].LastRefreshError = ""
	}

	node := w.cluster.Node()
	doc := shadowDoc{
		SchemaVersion: 1,
		Node:          node.Node,
		clusterSections: clusterSections{
			DNS: shadowDNSConfig{
				Mode:              snap.DNS.Mode,
				DNSSECMode:        snap.DNS.DNSSECMode,
				UpstreamResolvers: snap.DNS.UpstreamResolvers,
				UpstreamRoutes:    snap.DNS.UpstreamRoutes,
				UpstreamTimeout:   snap.DNS.UpstreamTimeout,
				TrustedSubnets:    snap.DNS.TrustedSubnets,
				Cache:             snap.DNS.Cache,
			},
			Filtering:        filtering,
			LocalDNS:         snap.LocalDNS,
			QueryLog:         snap.QueryLog,
			Auth:             snap.Auth,
			Schedules:        snap.Schedules,
			ScheduleBindings: snap.Bindings,
			DHCPServer:       snap.DHCPServer,
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
