// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Command agentd is the per-machine Anvil worker daemon (milestone 0.11).
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

	"github.com/anvil-dev/anvil/internal/daemon"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to agentd JSON config")
		listen     = flag.String("listen", "", "override listen (unix path or tcp://host:port)")
		showVer    = flag.Bool("version", false, "print version")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `agentd — Anvil local worker daemon

Usage:
  agentd --config agentd.json
  agentd --listen tcp://127.0.0.1:17654 --config agentd.json

Auth token is read from the env named by config token_env (default ANVIL_AGENTD_TOKEN).
LLM credentials for embedded workers: ANVIL_API_KEY, ANVIL_BASE_URL, ANVIL_MODEL, ANVIL_PROVIDER.

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

	cfg, err := daemon.LoadJSONFile(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *listen != "" {
		cfg.Listen = *listen
		if err := cfg.Normalize(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if cfg.Token() == "" {
		fmt.Fprintf(os.Stderr, "set %s to a non-empty controller token\n", cfg.TokenEnv)
		os.Exit(1)
	}

	srv, err := daemon.NewServer(cfg, nil, log.Default())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() { errCh <- srv.Serve(ctx) }()
	if cfg.Cluster.Enabled {
		edge := daemon.NewClusterEdge(cfg, srv, log.Default())
		go func() { errCh <- edge.Run(ctx) }()
	}

	err = <-errCh
	_ = srv.Close()
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func version() string {
	if i, ok := debug.ReadBuildInfo(); ok && i.Main.Version != "" && i.Main.Version != "(devel)" {
		return i.Main.Version
	}
	return "0.11.0-dev"
}
