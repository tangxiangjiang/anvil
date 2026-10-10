// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Command anvil-scheduler is the cluster controller + app HTTP plane (milestone 0.24).
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

	"github.com/anvil-dev/anvil/internal/scheduler"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to scheduler config (.json / .yaml / .yml)")
		showVer    = flag.Bool("version", false, "print version")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `anvil-scheduler — cluster controller + app HTTP + gateway catalog

Usage:
  anvil-scheduler --config scheduler.yaml

Apps speak HTTP (Bearer token from token_env):
  GET  /v1/health
  GET|PUT /v1/llm
  GET  /v1/nodes
  GET  /v1/models
  POST /v1/jobs

agentd joins listen_cluster (cluster.join / heartbeat / assign / done).
LLM credentials are configured by apps (PUT /v1/llm) and pushed to agentd
on join_ok and cluster.config. Scheduler does not proxy chat.

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
	cfg, err := scheduler.LoadFile(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if cfg.Token() == "" {
		fmt.Fprintf(os.Stderr, "set %s to a non-empty scheduler token\n", cfg.TokenEnv)
		os.Exit(1)
	}

	hub := scheduler.NewHub(cfg, log.Default())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := hub.Run(ctx); err != nil {
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
