package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dblock/dblock/internal/api"
	"github.com/dblock/dblock/internal/auth"
	"github.com/dblock/dblock/internal/config"
	dnsengine "github.com/dblock/dblock/internal/dns"
	"github.com/dblock/dblock/internal/filter"
	dlog "github.com/dblock/dblock/internal/log"
)

// app is a package-level var so that the rebuildDNS closure can reference it
// before it is assigned. It is written once, before any DNS query arrives.
var app *api.App

func main() {
	cfgFile := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	dir := filepath.Dir(*cfgFile)

	cfg, err := config.Load(dir)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = &config.Config{}
			cfg.Defaults()
			if saveErr := config.Save(dir, cfg); saveErr != nil {
				log.Fatalf("create default config: %v", saveErr)
			}
			log.Printf("created default config at %s", filepath.Join(dir, "config.yaml"))
		} else {
			log.Fatalf("load config: %v", err)
		}
	}

	authStore := auth.NewStore(cfg.Auth)
	filterEng := filter.New(cfg.Filtering)
	queryLog := dlog.New(cfg.QueryLog.MaxEntries)

	// dnsServer is replaced on a full DNS restart (listen-config change only).
	var dnsServer *dnsengine.Server

	rebuildDNS := func(newCfg *config.Config) error {
		newLocalRes := dnsengine.NewLocalResolver(newCfg.LocalDNS.Entries)

		var newFwd *dnsengine.Forwarder
		var newRec *dnsengine.Recursor
		if newCfg.DNS.Mode == "recursive" {
			newRec = dnsengine.NewRecursor(newCfg.DNS.TrustedSubnets)
		} else {
			newFwd = dnsengine.NewForwarder(newCfg.DNS)
		}

		var newCache *dnsengine.Cache
		if newCfg.DNS.Cache.Enabled {
			newCache = dnsengine.NewCache(newCfg.DNS.Cache.MaxEntries)
		}

		newHandler := dnsengine.NewHandler(dnsengine.HandlerConfig{
			DNSCfg:        newCfg.DNS,
			FilterEngine:  app.GetFilterEng,
			LocalResolver: newLocalRes,
			Forwarder:     newFwd,
			Recursor:      newRec,
			Cache:         newCache,
			QueryLog:      queryLog,
		})

		// If the listen config (port, address family) hasn't changed, we can
		// swap the handler in-place without restarting the listeners.
		if dnsServer != nil && !dnsServer.ListenCfgChanged(newCfg.DNS) {
			dnsServer.UpdateHandler(newHandler)
			return nil
		}

		// Listen config changed: restart the server.
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

	// Create App before the DNS handler so we can pass app.GetFilterEng as a getter.
	app = api.NewApp(dir, cfg, authStore, filterEng, queryLog, rebuildDNS)

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

	handler := dnsengine.NewHandler(dnsengine.HandlerConfig{
		DNSCfg:        cfg.DNS,
		FilterEngine:  app.GetFilterEng,
		LocalResolver: localRes,
		Forwarder:     fwd,
		Recursor:      rec,
		Cache:         cache,
		QueryLog:      queryLog,
	})

	dnsServer = dnsengine.New(cfg.DNS, handler)

	apiSrv := api.NewServer(app)

	if err := apiSrv.Start(); err != nil {
		log.Fatalf("start api server: %v", err)
	}
	log.Printf("management API listening on :%d", cfg.API.Port)

	if err := dnsServer.Start(); err != nil {
		log.Fatalf("start dns server: %v", err)
	}
	log.Printf("DNS server listening on :%d (mode=%s)", cfg.DNS.Listen.Port, cfg.DNS.Mode)

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
