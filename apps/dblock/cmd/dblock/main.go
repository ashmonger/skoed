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

	// app is declared before rebuildDNS so the closure captures it by reference.
	// It is assigned before any DNS query arrives, so the closure reads the final value.
	var app *api.App
	var dnsServer *dnsengine.Server

	rebuildDNS := func(newCfg *config.Config) error {
		newHandler := buildDNSHandler(newCfg, app.GetFilterEng, queryLog)

		// Swap the handler in-place when only the blocklist/filter config changed.
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

	app = api.NewApp(dir, cfg, authStore, filterEng, queryLog, rebuildDNS)

	handler := buildDNSHandler(cfg, app.GetFilterEng, queryLog)

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
