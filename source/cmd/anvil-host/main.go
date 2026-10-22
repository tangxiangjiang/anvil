// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Command anvil-host is the stdio sidecar an editor client spawns to drive
// Anvil sessions.
//
// One process serves one editor window: it reads newline-delimited JSON-RPC 2.0
// on stdin and writes replies plus event notifications on stdout. stdout
// carries the protocol and nothing else, so all diagnostics go to stderr.
//
// It is a standalone capability. It links the public anvil package only and has
// no relationship to the scheduler / agentd / cluster plane; see
// doc/design/v0.x/35-vscode-extension.md.
//
// Provider credentials are read from the environment (ANVIL_API_KEY and
// friends), which the client populates from its secret store. The key is never
// part of the protocol.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/anvil-dev/anvil/internal/editorhost"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print the host and protocol version, then exit")
		cwd         = flag.String("cwd", "", "workspace root; initialize may override it")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("anvil-host %s (protocol %d)\n", editorhost.Version, editorhost.ProtocolVersion)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := editorhost.New(editorhost.Config{
		CWD:      *cwd,
		Provider: os.Getenv("ANVIL_PROVIDER"),
		BaseURL:  os.Getenv("ANVIL_BASE_URL"),
		Model:    os.Getenv("ANVIL_MODEL"),
		Logf:     func(format string, args ...any) { fmt.Fprintf(os.Stderr, "anvil-host: "+format+"\n", args...) },
	}, os.Stdin, os.Stdout)

	if err := srv.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "anvil-host: %v\n", err)
		os.Exit(1)
	}
}
