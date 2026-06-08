package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"os/signal"
	"syscall"
	"time"

	"github.com/dblock/dblock/internal/api"
	"github.com/dblock/dblock/internal/auth"
	"github.com/dblock/dblock/internal/cluster"
	"github.com/dblock/dblock/internal/config"
	"github.com/dblock/dblock/internal/dhcp"
	dnsengine "github.com/dblock/dblock/internal/dns"
	"github.com/dblock/dblock/internal/filter"
	filterCats "github.com/dblock/dblock/internal/filter/categories"
	dlog "github.com/dblock/dblock/internal/log"
	"github.com/dblock/dblock/internal/metrics"
	"github.com/dblock/dblock/internal/refresh"
)

// Build metadata. Override at link time with:
//   go build -ldflags "-X main.version=v1.2.3 -X main.commit=$(git rev-parse --short HEAD)"
var (
	version = "dev"
	commit  = "unknown"
)

// buildDNSHandler constructs the DNS query pipeline for the current config.
// The filter engine getter is wired through App so handler rebuilds after
// Raft applies pick up the fresh filter.
//
// M4.7: cache is now provided by the caller and persists across rebuilds
// — config edits (blocklist add, profile rename, etc.) used to wipe the
// whole cache as a side effect of constructing a fresh handler-and-cache
// pair. Hot domains had to be re-fetched after any change.
//
// M5.1: when m is non-nil, every query exit observes outcome+duration
// via m.ObserveQuery so /metrics counters reflect live traffic.
func buildDNSHandler(cfg *config.Config, getEng func() *filter.Engine, queryLog *dlog.QueryLog, dhcpMgr *dhcp.Manager, cache *dnsengine.Cache, m *metrics.Metrics) *dnsengine.Handler {
	localRes := dnsengine.NewLocalResolver(cfg.LocalDNS.Entries)

	var fwd *dnsengine.Forwarder
	var rec *dnsengine.Recursor
	if cfg.DNS.Mode == "recursive" {
		rec = dnsengine.NewRecursor(cfg.DNS.TrustedSubnets)
	} else {
		fwd = dnsengine.NewForwarder(cfg.DNS)
	}

	var dhcpLookup func(ip string) (string, string, string, bool)
	if dhcpMgr != nil {
		dhcpLookup = func(ip string) (string, string, string, bool) {
			if l, ok := dhcpMgr.LookupByIP(ip); ok {
				return l.Hostname, l.MAC, l.ClientID, true
			}
			return "", "", "", false
		}
	}

	var observe func(string, time.Duration)
	if m != nil {
		observe = m.ObserveQuery
	}

	return dnsengine.NewHandler(dnsengine.HandlerConfig{
		DNSCfg:        cfg.DNS,
		FilterEngine:  getEng,
		LocalResolver: localRes,
		Forwarder:     fwd,
		Recursor:      rec,
		Cache:         cache,
		QueryLog:      queryLog,
		DhcpLookup:    dhcpLookup,
		ObserveQuery:  observe,
	})
}

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()

	node, m1Snapshot, err := cluster.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("load config.yaml: %v", err)
	}
	if m1Snapshot != nil {
		log.Printf("detected cluster seed in config.yaml; will migrate after bootstrap")
	}

	// Decide bootstrap mode.
	hasRaft := raftStateExists(node)
	wantBootstrap := !hasRaft && node.Bootstrap.Token == ""
	suppressRaft := os.Getenv("DBLOCK_TEST_MODE") == "1"
	mtlsEnabled := node.Node.Cluster.MTLS.Enabled

	// M5.3 — when joining with mTLS enabled, fetch the cluster CA + a
	// freshly-signed leaf cert from the leader BEFORE cluster.New so the
	// TLS-wrapped Raft transport can bind. This does NOT consume the
	// token — the regular join (below, async) consumes it and runs
	// AddVoter once our Raft is up and reachable.
	if !hasRaft && node.Bootstrap.Token != "" && mtlsEnabled {
		if err := mtlsBootstrapFromLeader(node); err != nil {
			log.Fatalf("mtls bootstrap: %v", err)
		}
	}

	c, err := cluster.New(node, cluster.Options{
		Bootstrap:       wantBootstrap,
		SuppressRaftLog: suppressRaft,
		MTLSEnabled:     mtlsEnabled,
	})
	if err != nil {
		log.Fatalf("start cluster: %v", err)
	}
	defer c.Close()

	// Self-enrol path: kick the join in a background goroutine so HTTP
	// comes up to answer health probes while we wait for the leader to
	// AddVoter us. Runs in mTLS mode too — by the time the leader does
	// AddVoter, our Raft is already listening on TLS.
	if !hasRaft && node.Bootstrap.Token != "" {
		go func() {
			if err := joinExistingCluster(node); err != nil {
				log.Fatalf("join cluster: %v", err)
			}
		}()
	}

	// Wait for leader; for single-node bootstrap this is near-instant.
	if wantBootstrap {
		if err := c.WaitForLeader(15 * time.Second); err != nil {
			log.Fatalf("wait for leader: %v", err)
		}
		if m1Snapshot != nil {
			if err := c.ImportFromM1(*m1Snapshot); err != nil {
				log.Printf("M1 migration failed: %v", err)
			} else {
				log.Printf("M1 config migrated into bbolt")
			}
		}
		// Generate the cluster-wide secret on the bootstrap node. Joining
		// nodes pick it up automatically via Raft replication.
		if err := c.EnsureClusterSecret(); err != nil {
			log.Printf("ensure cluster secret: %v", err)
		}
		// M3: the reserved default profile + bundled DoH category seed.
		// Idempotent — re-runs every bootstrap but only mutates state once.
		if err := c.EnsureDefaultProfile(); err != nil {
			log.Printf("ensure default profile: %v", err)
		}
		if err := c.EnsureDohCategoryOnDefaultProfile(filterCats.Catalog["doh"].Bundled); err != nil {
			log.Printf("ensure DoH category: %v", err)
		}
	}

	// Prime services from the current snapshot. Fill any zero-valued knobs
	// (query log capacity, cache size, etc.) with M1 defaults so a fresh
	// cluster has the same behaviour as a fresh M1 node. Then merge the
	// node-local listen settings into the DNS config so the engine binds
	// to the right port.
	snap, err := c.Store().Snapshot()
	if err != nil {
		log.Fatalf("snapshot: %v", err)
	}
	snap.Defaults()
	mergeNodeLocal(snap, node)

	authStore := auth.NewStore(snap.Auth)
	queryLog := dlog.New(snap.QueryLog.MaxEntries)

	var app *api.App
	var dnsServer *dnsengine.Server
	var encryptedSrv *dnsengine.EncryptedServer // populated below; closure captures by name
	var dhcpMgr *dhcp.Manager                   // populated below; closure captures by name
	// M4.7: long-lived DNS cache. Survives every Raft apply (config
	// edits used to wipe the whole cache as a side effect of
	// constructing a fresh handler-and-cache pair). Allocated once
	// before rebuildDNS is called.
	var dnsCache *dnsengine.Cache
	if snap.DNS.Cache.Enabled {
		dnsCache = dnsengine.NewCache(snap.DNS.Cache.MaxEntries)
	}

	// M5.4 — blocklist refresh scheduler. Started on every node; only the
	// leader does work each tick. Declared up here so the metrics
	// constructor can read its failure counter map.
	var refreshSched *refresh.Scheduler

	// M5.1 — Prometheus exporter. Built before the App so we can pass it
	// into NewApp / buildDNSHandler. Reads cache/cluster/dhcp state via
	// callbacks because all three pointers may be reallocated after
	// boot (cache on max_entries change, dhcp manager on enable/disable).
	prom := metrics.New(metrics.Options{
		Build: metrics.BuildInfo{Version: version, Commit: commit},
		CacheStats: func() metrics.CacheSnapshot {
			if dnsCache == nil {
				return metrics.CacheSnapshot{Enabled: false}
			}
			s := dnsCache.Snapshot()
			return metrics.CacheSnapshot{
				Size: s.Size, MaxEntries: s.MaxEntries,
				Hits: s.Hits, Misses: s.Misses, Evictions: s.Evictions,
				Enabled: true,
			}
		},
		Cluster: func() metrics.ClusterSnapshot {
			servers := c.MembersFromRaftConfig()
			return metrics.ClusterSnapshot{
				IsLeader:         c.IsLeader(),
				RaftTerm:         c.Raft().CurrentTerm(),
				CommitIndex:      c.Raft().CommitIndex(),
				Members:          len(servers),
				ReachableMembers: len(servers), // best-effort; full probe lives in /cluster/health
			}
		},
		Dhcp: func() *metrics.DhcpSnapshot {
			if dhcpMgr == nil {
				return nil
			}
			last := dhcpMgr.LastPollAt()
			var age float64
			if !last.IsZero() {
				age = time.Since(last).Seconds()
			}
			return &metrics.DhcpSnapshot{
				Source:          dhcpMgr.Source(),
				Leases:          len(dhcpMgr.Snapshot()),
				AnomaliesOpen:   countOpenAnomalies(dhcpMgr.Anomalies()),
				LastPollAgeSecs: age,
				PollErrorsTotal: dhcpMgr.PollErrorsTotal(),
			}
		},
		Blocklists: func() []metrics.BlocklistSnapshot {
			snap, err := c.Store().Snapshot()
			if err != nil {
				return nil
			}
			var failures map[string]uint64
			if refreshSched != nil {
				failures = refreshSched.PerBlocklistFailures()
			}
			out := make([]metrics.BlocklistSnapshot, 0, len(snap.Filtering.Blocklists))
			for _, bl := range snap.Filtering.Blocklists {
				var last time.Time
				if bl.LastRefreshAt != "" {
					if t, err := time.Parse(time.RFC3339, bl.LastRefreshAt); err == nil {
						last = t
					}
				}
				out = append(out, metrics.BlocklistSnapshot{
					ID:            bl.ID,
					LastRefreshAt: last,
					Failures:      failures[bl.ID],
				})
			}
			return out
		},
		RequireAuth: func() bool { return node.Node.API.Metrics.RequireAuth },
		AuthOK:      func(r *http.Request) bool { return app != nil && app.CheckBasicAuth(r) },
	})

	rebuildDNS := func(newCfg *config.Config) error {
		newCfg.Defaults()
		mergeNodeLocal(newCfg, node)
		// M4.7: rebuild the cache only when caching toggles on/off or
		// max_entries changes. Otherwise reuse the existing pointer so
		// hot entries survive config edits.
		if newCfg.DNS.Cache.Enabled {
			if dnsCache == nil || dnsCache.Snapshot().MaxEntries != newCfg.DNS.Cache.MaxEntries {
				dnsCache = dnsengine.NewCache(newCfg.DNS.Cache.MaxEntries)
			}
		} else {
			dnsCache = nil
		}
		if app != nil {
			app.SetDNSCache(dnsCache)
		}
		newHandler := buildDNSHandler(newCfg, app.GetFilterEng, queryLog, dhcpMgr, dnsCache, prom)
		// M4: DoH and DoT must see the same fresh handler the plain UDP/TCP
		// server is about to get — otherwise local-DNS / blocklist / SafeSearch
		// changes via the API only take effect on UDP, and DoH keeps serving
		// the boot-time handler until the process restarts.
		if encryptedSrv != nil {
			encryptedSrv.UpdateHandler(newHandler)
		}
		if dnsServer != nil && !dnsServer.ListenCfgChanged(newCfg.DNS) {
			dnsServer.UpdateHandler(newHandler)
			return nil
		}
		newServer := dnsengine.New(newCfg.DNS, newHandler)
		if dnsServer != nil {
			dnsServer.Shutdown()
		}
		if err := newServer.Start(); err != nil {
			return fmt.Errorf("start rebuilt dns server: %w", err)
		}
		dnsServer = newServer
		return nil
	}

	app = api.NewApp(c, authStore, queryLog, rebuildDNS)
	app.SetDNSCache(dnsCache)
	app.SetMetrics(prom)
	app.SetMetricsRequireAuth(node.Node.API.Metrics.RequireAuth)

	// M3.6 — read-only DHCP integration. Node-local; each node polls its
	// own configured connector. Operators typically point every node at
	// the same central DHCP source for consistent cluster behavior.
	if node.Node.DHCP.Enabled {
		conn, err := dhcp.New(dhcp.Config{
			Kind:           node.Node.DHCP.Kind,
			URL:            node.Node.DHCP.URL,
			FilePath:       node.Node.DHCP.FilePath,
			Username:       node.Node.DHCP.Username,
			Password:       node.Node.DHCP.Password,
			RefreshSeconds: node.Node.DHCP.RefreshSeconds,
		})
		if err != nil {
			log.Fatalf("init DHCP connector: %v", err)
		}
		refresh := time.Duration(node.Node.DHCP.RefreshSeconds) * time.Second
		if refresh <= 0 {
			refresh = 60 * time.Second
		}
		dhcpMgr = dhcp.NewManager(conn, refresh)
		dhcpMgr.Start()
		defer dhcpMgr.Shutdown()
		app.SetDhcpManager(dhcpMgr)
		log.Printf("DHCP integration enabled (kind=%s refresh=%s)", node.Node.DHCP.Kind, refresh)
	}

	// Initial DNS handler + server.
	dnsServer = dnsengine.New(snap.DNS, buildDNSHandler(snap, app.GetFilterEng, queryLog, dhcpMgr, dnsCache, prom))

	// Shadow YAML writer mirrors bbolt to <data_dir>/config.yaml.
	shadow := cluster.NewShadowWriter(c, time.Second)
	shadow.Start()
	defer shadow.Stop()

	// Hourly query-log aggregator. Each query the DNS engine processes is
	// reflected here via the QueryLog's Append hook — for M2 we tee in the
	// handler. Simplest: have the aggregator pull from queryLog via Observe
	// calls wired from the DNS handler. The aggregator is started even on
	// non-leaders; it silently no-ops the commit until leadership arrives.
	agg := dlog.NewAggregator(dlog.AggregatorConfig{
		NodeID: node.Node.ID,
	}, c)
	agg.Start()
	defer agg.Stop()

	// Tee DNS-engine query events into the aggregator. We do this by
	// wrapping the queryLog's Append. For minimal blast radius in M2, the
	// aggregator observes by hooking into the same path the DNS handler
	// already calls.
	queryLog.SetObserver(func(e dlog.Entry) {
		agg.Observe(e.Client, e.Domain, e.Outcome)
	})

	// M5.4 — start the blocklist refresh scheduler. Started on every node;
	// only the current Raft leader actually fetches per tick. Test mode
	// drops the tick interval to 1 s so acceptance tests don't wait.
	refreshTick := 10 * time.Second
	if os.Getenv("DBLOCK_TEST_MODE") == "1" {
		refreshTick = time.Second
	}
	refreshSched = refresh.New(c, refresh.Options{Tick: refreshTick})
	refreshSched.Start()
	defer refreshSched.Stop()

	// M4.6 — optional HTTPS for the management API. When disabled (the
	// default), behaviour is identical to M1-M3: plain HTTP on api_address.
	apiTLSOpts := api.TLSOptions{Enabled: node.Node.API.TLS.Enabled}
	if apiTLSOpts.Enabled {
		apiTLSOpts.Mode = node.Node.API.TLS.Mode
		if apiTLSOpts.Mode == "" {
			apiTLSOpts.Mode = "single_port"
		}
		apiTLSOpts.HTTPSAddress = node.Node.API.TLS.HTTPSAddress
		apiTLSOpts.HSTS = node.Node.API.TLS.HSTS
		// Reuse the same cert dblock uses for DoH/DoT. EnsureSelfSignedCert
		// generates one on first boot if neither cert_file nor ACME is set.
		certFile, keyFile, err := dnsengine.EnsureSelfSignedCert(
			node.Node.DataDir,
			node.Node.DNS.TLS.CertFile,
			node.Node.DNS.TLS.KeyFile,
			node.Node.ID,
		)
		if err != nil {
			log.Fatalf("prepare API TLS cert: %v", err)
		}
		apiTLSOpts.CertFile = certFile
		apiTLSOpts.KeyFile = keyFile
	}
	apiSrv := api.NewServerWithTLS(app, apiTLSOpts)
	if err := apiSrv.Start(); err != nil {
		log.Fatalf("start api server: %v", err)
	}
	if apiTLSOpts.Enabled {
		log.Printf("management API listening on %s (HTTPS mode=%s hsts=%v)",
			node.Node.APIAddress, apiTLSOpts.Mode, apiTLSOpts.HSTS)
		if apiTLSOpts.Mode == "dual_port" && apiTLSOpts.HTTPSAddress != "" {
			log.Printf("management API HTTPS listening on %s", apiTLSOpts.HTTPSAddress)
		}
	} else {
		log.Printf("management API listening on %s", node.Node.APIAddress)
	}

	if err := dnsServer.Start(); err != nil {
		log.Fatalf("start dns server: %v", err)
	}
	log.Printf("DNS server listening on :%d (mode=%s)", snap.DNS.Listen.Port, snap.DNS.Mode)

	// M4: encrypted DNS listeners (DoH/DoT). Both share a single TLS cert.
	// Either port at 0 disables that transport; both at 0 skips the
	// EncryptedServer entirely so non-M4 deployments behave exactly like
	// they did before.
	var acmeMgr *dnsengine.AcmeManager
	if snap.DNS.Listen.DoHPort > 0 || snap.DNS.Listen.DoTPort > 0 {
		// Always materialise the self-signed cert: it's the ACME fallback
		// AND it's what serves DoH during the first-boot window before
		// autocert finishes issuing.
		certFile, keyFile, err := dnsengine.EnsureSelfSignedCert(
			node.Node.DataDir,
			node.Node.DNS.TLS.CertFile,
			node.Node.DNS.TLS.KeyFile,
			node.Node.ID,
		)
		if err != nil {
			log.Fatalf("prepare TLS cert: %v", err)
		}

		// ACME wrapper — when enabled, autocert manages the live cert.
		// Start the HTTP-01 challenge listener BEFORE the DoH/DoT
		// listeners so the very first cert request (lazy on handshake)
		// can complete the challenge.
		if acme := node.Node.DNS.TLS.Acme; acme != nil && acme.Enabled {
			acmeMgr, err = dnsengine.NewAcmeManager(dnsengine.AcmeConfig{
				Enabled:           true,
				Email:             acme.Email,
				Domains:           acme.Domains,
				DirectoryURL:      acme.DirectoryURL,
				HTTPChallengePort: acme.HTTPChallengePort,
				CacheDir:          filepath.Join(node.Node.DataDir, "tls", "acme-cache"),
				FallbackCertFile:  certFile,
				FallbackKeyFile:   keyFile,
			})
			if err != nil {
				log.Fatalf("init ACME: %v", err)
			}
			if err := acmeMgr.Start(); err != nil {
				log.Fatalf("start ACME HTTP-01 listener: %v", err)
			}
			log.Printf("ACME enabled (directory=%s domains=%v http=%s)",
				acmeDirectoryLabel(acme.DirectoryURL), acme.Domains, acmeMgr.Addr())
		}

		encryptedSrv, err = dnsengine.NewEncryptedServer(
			buildDNSHandler(snap, app.GetFilterEng, queryLog, dhcpMgr, dnsCache, prom),
			snap.DNS.Listen.DoHPort,
			snap.DNS.Listen.DoTPort,
			certFile, keyFile,
			acmeMgr,
		)
		if err != nil {
			log.Fatalf("build encrypted DNS server: %v", err)
		}
		if err := encryptedSrv.Start(); err != nil {
			log.Fatalf("start encrypted DNS server: %v", err)
		}
		certLabel := certFile
		if acmeMgr != nil {
			certLabel = "ACME-managed"
		}
		if snap.DNS.Listen.DoHPort > 0 {
			log.Printf("DoH server listening on :%d (cert=%s)", snap.DNS.Listen.DoHPort, certLabel)
		}
		if snap.DNS.Listen.DoTPort > 0 {
			log.Printf("DoT server listening on :%d (cert=%s)", snap.DNS.Listen.DoTPort, certLabel)
		}
	}

	log.Printf("dblock M2 node %q ready (raft=%s)", node.Node.ID, node.Node.RaftAddress)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down…")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := apiSrv.Shutdown(ctx); err != nil {
		log.Printf("api shutdown: %v", err)
	}
	dnsServer.Shutdown()
	if encryptedSrv != nil {
		encryptedSrv.Shutdown()
	}
	if acmeMgr != nil {
		acmeMgr.Shutdown()
	}
	log.Println("done")
}

// acmeDirectoryLabel returns the directory URL or "Let's Encrypt (production)"
// when empty — just for the startup log line.
func acmeDirectoryLabel(url string) string {
	if url == "" {
		return "Let's Encrypt (production)"
	}
	return url
}

// countOpenAnomalies returns the number of unacknowledged anomalies in
// the slice. Used by the M5.1 dhcp_anomalies_open gauge.
func countOpenAnomalies(anomalies []dhcp.Anomaly) int {
	n := 0
	for _, a := range anomalies {
		if a.AcknowledgedAt == nil {
			n++
		}
	}
	return n
}

// mtlsBootstrapFromLeader POSTs the join token to the leader's pre-Raft
// mTLS bootstrap endpoint and writes the returned CA + leaf to disk.
// Runs synchronously BEFORE cluster.New so the TLS-wrapped Raft
// transport has its keypair on disk at bind time.
//
// The leader validates the token but does NOT consume it; the regular
// /api/v1/cluster/join call (kicked off post-cluster.New) consumes the
// token and runs AddVoter once our Raft is up.
func mtlsBootstrapFromLeader(node *cluster.NodeYAML) error {
	body, err := json.Marshal(map[string]string{
		"token":   node.Bootstrap.Token,
		"node_id": node.Node.ID,
	})
	if err != nil {
		return err
	}
	url := node.Bootstrap.LeaderAddress + "/api/v1/cluster/mtls-bootstrap"
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		buf, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var r struct {
				CACertPEM   []byte `json:"ca_cert_pem"`
				LeafCertPEM []byte `json:"leaf_cert_pem"`
				LeafKeyPEM  []byte `json:"leaf_key_pem"`
			}
			if err := json.Unmarshal(buf, &r); err != nil {
				return fmt.Errorf("decode mtls-bootstrap response: %w", err)
			}
			dir := cluster.MtlsDir(node.Node.DataDir)
			if err := os.MkdirAll(dir, 0700); err != nil {
				return err
			}
			p := cluster.MtlsPaths(node.Node.DataDir)
			if err := os.WriteFile(p.CACertFile, r.CACertPEM, 0644); err != nil {
				return err
			}
			if err := os.WriteFile(p.NodeCert, r.LeafCertPEM, 0644); err != nil {
				return err
			}
			if err := os.WriteFile(p.NodeKey, r.LeafKeyPEM, 0600); err != nil {
				return err
			}
			log.Printf("mTLS bundle from %s persisted under %s", node.Bootstrap.LeaderAddress, dir)
			return nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return fmt.Errorf("mtls-bootstrap rejected (status %d): %s", resp.StatusCode, string(buf))
		}
		lastErr = fmt.Errorf("mtls-bootstrap status %d: %s", resp.StatusCode, string(buf))
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("mtls-bootstrap: %w", lastErr)
}

// mergeNodeLocal overlays the node-local DNS listen settings onto a
// cluster-replicated config snapshot. The cluster never replicates listen
// ports because they're host-specific.
func mergeNodeLocal(cfg *config.Config, node *cluster.NodeYAML) {
	cfg.DNS.Listen = config.ListenConfig{
		Port:    node.Node.DNS.Listen.Port,
		IPv4:    node.Node.DNS.Listen.IPv4,
		IPv6:    node.Node.DNS.Listen.IPv6,
		DoHPort: node.Node.DNS.Listen.DoHPort,
		DoTPort: node.Node.DNS.Listen.DoTPort,
	}
}

// fileExists is a tiny helper used by the M1-migration and bootstrap probes.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// raftStateExists returns true when this node has already participated in a
// Raft cluster (so the binary should NOT bootstrap a new one).
func raftStateExists(node *cluster.NodeYAML) bool {
	for _, p := range []string{"raft/raft-log.bolt", "raft/raft-stable.bolt"} {
		if fi, err := os.Stat(node.DataPath(p)); err == nil && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// joinExistingCluster runs the self-enrolment flow on a fresh node with a
// bootstrap token. It posts to <leader>/api/v1/cluster/join with our identity
// and the token. The leader validates the token via Raft, then AddVoter's us.
// Once Raft replication completes the FSM will catch up and the node serves
// traffic.
//
// We retry briefly to tolerate the leader not being immediately reachable
// when the joining node boots faster than the leader.
func joinExistingCluster(node *cluster.NodeYAML) error {
	body, err := json.Marshal(map[string]string{
		"token":        node.Bootstrap.Token,
		"node_id":      node.Node.ID,
		"raft_address": node.Node.RaftAddress,
		"api_address":  node.Node.APIAddress,
	})
	if err != nil {
		return err
	}

	url := node.Bootstrap.LeaderAddress + "/api/v1/cluster/join"
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		buf, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			log.Printf("enrolled into cluster via %s (response: %s)", node.Bootstrap.LeaderAddress, string(buf))
			return nil
		}
		// 403 / 4xx → token rejected; abort.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return fmt.Errorf("join rejected (status %d): %s", resp.StatusCode, string(buf))
		}
		// 5xx → retry; leader might be re-electing.
		lastErr = fmt.Errorf("join status %d: %s", resp.StatusCode, string(buf))
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("join cluster: %w", lastErr)
}
