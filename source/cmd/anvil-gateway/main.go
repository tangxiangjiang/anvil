// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Command anvil-gateway is the OpenAI-compatible model gateway (milestone 0.12).
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

	"github.com/anvil-dev/anvil/internal/gateway"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to gateway config (.json / .yaml / .yml)")
		listen     = flag.String("listen", "", "override listen addr (host:port)")
		showVer    = flag.Bool("version", false, "print version")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `anvil-gateway — model catalog + auth + OpenAI-compatible proxy

Usage:
  anvil-gateway --config gateway.yaml
  anvil-gateway --config gateway.json

Client auth: Bearer token from env named by token_env (default ANVIL_GATEWAY_TOKEN).
Upstream keys: per-upstream api_key_env (never accepted from clients).
Config: JSON or YAML (same schema).

Endpoints:
  GET  /v1/models
  POST /v1/chat/completions  (stream + non-stream)
  GET  /healthz

Point Anvil at the gateway:
  ANVIL_BASE_URL=http://127.0.0.1:8080/v1
  ANVIL_API_KEY=<gateway-token>
  ANVIL_PROVIDER=openai
  ANVIL_MODEL=<public model id from config>

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
	cfg, err := gateway.LoadFile(*configPath)
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
		fmt.Fprintf(os.Stderr, "set %s to a non-empty gateway client token\n", cfg.TokenEnv)
		os.Exit(1)
	}

	srv, err := gateway.NewServer(cfg, log.Default())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func version() string {
	if i, ok := debug.ReadBuildInfo(); ok && i.Main.Version != "" && i.Main.Version != "(devel)" {
		return i.Main.Version
	}
	return "0.12.0-dev"
}
