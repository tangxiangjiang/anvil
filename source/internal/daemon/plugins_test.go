// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestResolvePluginsDeny(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:0"
	cfg.Plugins = PluginsConfig{
		Available: []string{"sample", "evil"},
		Deny:      []string{"evil"},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	ids, err := cfg.ResolvePlugins([]string{"sample"})
	if err != nil || len(ids) != 1 || ids[0] != "sample" {
		t.Fatalf("%v %v", ids, err)
	}
	if _, err := cfg.ResolvePlugins([]string{"evil"}); err == nil {
		t.Fatal("expected deny")
	}
	defs, err := cfg.ResolvePlugins(nil)
	if err != nil || len(defs) != 1 || defs[0] != "sample" {
		t.Fatalf("defaults %v %v", defs, err)
	}
}

func TestHelloOKAdvertisesPluginCaps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANVIL_AGENTD_TOKEN", "test-token")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "tcp://" + ln.Addr().String()
	_ = ln.Close()

	cfg := DefaultConfig()
	cfg.Listen = addr
	cfg.Repos = []RepoEntry{{ID: "demo", CWD: dir}}
	cfg.Plugins = PluginsConfig{Available: []string{"sample"}}
	cfg.NodeID = "test-node"
	srv, err := NewServer(cfg, &EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	waitReady(t, addr)
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	ok, err := cl.Hello("test-token", "unit")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range ok.Capabilities {
		if c == "plugin.sample" {
			found = true
		}
	}
	if !found {
		t.Fatalf("caps=%v", ok.Capabilities)
	}
}

func TestJobAssignDeniedPluginNeverInits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANVIL_AGENTD_TOKEN", "test-token")
	initCh := make(chan string, 1)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "tcp://" + ln.Addr().String()
	_ = ln.Close()

	cfg := DefaultConfig()
	cfg.Listen = addr
	cfg.Repos = []RepoEntry{{ID: "demo", CWD: dir}}
	cfg.Plugins = PluginsConfig{Available: []string{"sample"}, Deny: []string{"blocked"}}
	cfg.NodeID = "test-node"
	srv, err := NewServer(cfg, probeRunner{initCh: initCh}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	waitReady(t, addr)
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	if _, err := cl.Hello("test-token", "unit"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Assign(JobAssignBody{
		JobID: "j1", RepoID: "demo", Prompt: "x",
		Plugins: []string{"blocked"}, ForceStub: true,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for deny error")
		default:
		}
		env, err := cl.ReadMsg()
		if err != nil {
			t.Fatal(err)
		}
		if env.Kind == KindError {
			var eb ErrorBody
			_ = DecodeBody(env, &eb)
			if !strings.Contains(eb.Message, "denied") && !strings.Contains(eb.Message, "allowlist") {
				t.Fatalf("msg=%q", eb.Message)
			}
			select {
			case id := <-initCh:
				t.Fatalf("plugin should not init, got %s", id)
			case <-time.After(50 * time.Millisecond):
			}
			return
		}
	}
}

func TestPluginCapabilityLabels(t *testing.T) {
	cfg := Config{Plugins: PluginsConfig{Available: []string{"sample", "x"}, Deny: []string{"x"}}}
	labels := cfg.PluginCapabilityLabels()
	if len(labels) != 1 || labels[0] != "plugin.sample" {
		t.Fatalf("%v", labels)
	}
}

func waitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := Dial(addr)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server not ready")
}

type probeRunner struct {
	initCh chan string
}

func (p probeRunner) Run(ctx context.Context, job JobAssignBody, cwd string, emit func(JobEventBody)) JobDoneBody {
	_ = ctx
	_ = cwd
	_ = emit
	for _, id := range job.Plugins {
		p.initCh <- id
	}
	return JobDoneBody{JobID: job.JobID, Status: "finished", Result: "ok", Steps: 1}
}
