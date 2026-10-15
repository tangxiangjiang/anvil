// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Command anvil-web is the fleet ops console (milestone 0.25).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/anvil-dev/anvil/internal/web"
	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/delivery"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to anvil-web config (.json / .yaml / .yml)")
		showVer    = flag.Bool("version", false, "print version")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `anvil-web — fleet ops console (auth + BFF to anvil-scheduler)

Usage:
  anvil-web --config web.yaml

Browser auth: SQLite users/sessions (7-day Bearer token in localStorage).
Server uses ANVIL_SCHEDULER_TOKEN to call scheduler (never exposed to browser).

First start: open the UI install wizard to create the root admin
  (or optionally set ANVIL_WEB_ADMIN_USER / ANVIL_WEB_ADMIN_PASSWORD).

Adding UI features: register in static/js/nav.js + static/js/features/*.js
and add /api/v1 handlers on the web Server.

`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println(version())
		return
	}
	if *configPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	cfg, err := web.LoadFile(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if cfg.SchedulerToken() == "" {
		fmt.Fprintf(os.Stderr, "set %s for scheduler access\n", cfg.SchedulerTokenEnv)
		os.Exit(1)
	}

	store, err := auth.Open(cfg.DBPath, cfg.SessionTTL())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer store.Close()
	// Optional env bootstrap; otherwise the browser install wizard creates the admin.
	if err := store.EnsureAdmin(cfg.AdminUser(), cfg.AdminPassword()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if need, _ := store.NeedsSetup(); need {
		log.Printf("anvil-web: no users yet — open http://%s and complete the install wizard", cfg.Listen)
	}

	delStore, err := delivery.Open(store.DB())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := delStore.EnsureSeed(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	srv := web.New(cfg, store, delStore, staticFS, log.Default())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
