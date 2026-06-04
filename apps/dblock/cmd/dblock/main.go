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
	"os/signal"
	"syscall"
	"time"

	"github.com/dblock/dblock/internal/api"
	"github.com/dblock/dblock/internal/auth"
	"github.com/dblock/dblock/internal/cluster"
	"github.com/dblock/dblock/internal/config"
	dnsengine "github.com/dblock/dblock/internal/dns"
	"github.com/dblock/dblock/internal/filter"
	filterCats "github.com/dblock/dblock/internal/filter/categories"
	dlog "github.com/dblock/dblock/internal/log"
)

// buildDNSHandler constructs the DNS query pipeline for the current config.
// The filter engine getter is wired through App so handler rebuilds after
// Raft applies pick up the fresh filter.
func buildDNSHandler(cfg *config.Config, getEng func() *filter.Engine, queryLog *dlog.QueryLog) *dnsengine.Handler {
	localRes := dnsengine.NewLocalResolver(cfg.LocalDNS.Entries)

	var fwd *dnsengine.Forwarder
	var rec *dnsengine.Recursor
	if cfg.DNS.Mode == "recursive" {
		rec = dnsengine.NewRecursor(cfg.DNS.TrustedSubnets)
	} else {
		fwd = dnsengine.NewForwarder(cfg.DNS)
	}

	var cache *dnsengine.Cache
	if cfg.DNS.Cache.Enabled {
		cache = dnsengine.NewCache(cfg.DNS.Cache.MaxEntries)
	}

	return dnsengine.NewHandler(dnsengine.HandlerConfig{
		DNSCfg:        cfg.DNS,
		FilterEngine:  getEng,
		LocalResolver: localRes,
		Forwarder:     fwd,
		Recursor:      rec,
		Cache:         cache,
		QueryLog:      queryLog,
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

	c, err := cluster.New(node, cluster.Options{
		Bootstrap:       wantBootstrap,
		SuppressRaftLog: suppressRaft,
	})
	if err != nil {
		log.Fatalf("start cluster: %v", err)
	}
	defer c.Close()

	// Self-enrol path: this is a fresh node with a join token. Kick the join
	// in a background goroutine so HTTP can come up to answer health probes
	// while we wait for the leader to AddVoter us.
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

	rebuildDNS := func(newCfg *config.Config) error {
		newCfg.Defaults()
		mergeNodeLocal(newCfg, node)
		newHandler := buildDNSHandler(newCfg, app.GetFilterEng, queryLog)
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

	// Initial DNS handler + server.
	dnsServer = dnsengine.New(snap.DNS, buildDNSHandler(snap, app.GetFilterEng, queryLog))

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

	apiSrv := api.NewServer(app)
	if err := apiSrv.Start(); err != nil {
		log.Fatalf("start api server: %v", err)
	}
	log.Printf("management API listening on %s", node.Node.APIAddress)

	if err := dnsServer.Start(); err != nil {
		log.Fatalf("start dns server: %v", err)
	}
	log.Printf("DNS server listening on :%d (mode=%s)", snap.DNS.Listen.Port, snap.DNS.Mode)

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
	log.Println("done")
}

// mergeNodeLocal overlays the node-local DNS listen settings onto a
// cluster-replicated config snapshot. The cluster never replicates listen
// ports because they're host-specific.
func mergeNodeLocal(cfg *config.Config, node *cluster.NodeYAML) {
	cfg.DNS.Listen = config.ListenConfig{
		Port: node.Node.DNS.Listen.Port,
		IPv4: node.Node.DNS.Listen.IPv4,
		IPv6: node.Node.DNS.Listen.IPv6,
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
