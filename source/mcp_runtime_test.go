// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
)

// buildRuntimeFixture builds the fake stdio server that can name its pid and
// take its time, which is what retirement and reconnect tests need.
func buildRuntimeFixture(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakeruntime.exe")
	cmd := exec.Command("go", "build", "-o", out, "./internal/mcp/fakeruntime")
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakeruntime: %v\n%s", err, outBytes)
	}
	return out
}

func writeRuntimeConfig(t *testing.T, dir string, servers map[string]map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureServer(exe, pidFile string) MCPServerConfig {
	return MCPServerConfig{
		Command: exe,
		Env:     []string{"FAKE_MCP_PIDFILE=" + pidFile},
	}
}

func processRunning(t *testing.T, pid int) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
		if err != nil {
			t.Logf("tasklist: %v", err)
			return true
		}
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// One bad entry must not take the session with it: the good server connects,
// the broken one is a status.
func TestMCPRuntimeIsolatesAFailedServer(t *testing.T) {
	exe := buildRuntimeFixture(t)
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "good.pid")
	rt := NewMCPRuntime(MCPRuntimeOptions{
		CWD: dir,
		Inline: map[string]MCPServerConfig{
			"good":   fixtureServer(exe, pidFile),
			"broken": {Command: filepath.Join(dir, "does-not-exist")},
		},
		ConnectTimeout: 5 * time.Second,
	})
	defer rt.Close()

	if err := rt.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	good, ok := rt.Status("good")
	if !ok || good.State != MCPStateConnected || good.ToolCount != 3 {
		t.Fatalf("good=%+v want connected with three tools", good)
	}
	broken, ok := rt.Status("broken")
	if !ok || broken.State != MCPStateFailed || broken.Error == "" {
		t.Fatalf("broken=%+v want failed with an error", broken)
	}
	if got := len(rt.Tools()); got != 3 {
		t.Fatalf("tools=%d want only the good server's", got)
	}

	a, err := Create(context.Background(), Options{
		APIKey:     "k",
		Model:      Model{ID: "m"},
		Local:      LocalOptions{CWD: dir},
		MCPRuntime: rt,
	})
	if err != nil {
		t.Fatalf("Create with one broken server: %v", err)
	}
	if !containsName(a.registry.Names(), "mcp_good_echo") {
		t.Fatalf("registry=%v want the good server's tool", a.registry.Names())
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// The agent borrowed the connection; closing the conversation must not
	// close it.
	if status, ok := rt.Status("good"); !ok || status.State != MCPStateConnected {
		t.Fatalf("good after agent close=%+v want still connected", status)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// A tool handler resolves the session by name, so after a reconnect it must
// reach the new process.
func TestMCPRuntimeReconnectReachesTheNewSession(t *testing.T) {
	exe := buildRuntimeFixture(t)
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "one.pid")
	rt := NewMCPRuntime(MCPRuntimeOptions{
		CWD:            dir,
		Inline:         map[string]MCPServerConfig{"one": fixtureServer(exe, pidFile)},
		ConnectTimeout: 5 * time.Second,
	})
	defer rt.Close()
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	first, err := rt.Call(context.Background(), "one", "pid", nil)
	if err != nil {
		t.Fatalf("Call pid: %v", err)
	}
	if err := rt.Reconnect(context.Background(), "one"); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	second, err := rt.Call(context.Background(), "one", "pid", nil)
	if err != nil {
		t.Fatalf("Call pid after reconnect: %v", err)
	}
	if first == second {
		t.Fatalf("pid=%q before and after reconnect; want a new process", first)
	}
}

// An unchanged configuration keeps the process; a reload is not a restart.
func TestMCPRuntimeReloadKeepsUnchangedSessions(t *testing.T) {
	exe := buildRuntimeFixture(t)
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "one.pid")
	rt := NewMCPRuntime(MCPRuntimeOptions{
		CWD:            dir,
		Inline:         map[string]MCPServerConfig{"one": fixtureServer(exe, pidFile)},
		ConnectTimeout: 5 * time.Second,
	})
	defer rt.Close()
	ctx := context.Background()
	if err := rt.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := rt.Call(ctx, "one", "pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	version := rt.Version()
	if err := rt.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := rt.Call(ctx, "one", "pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("pid changed on an unchanged reload: %s -> %s", before, after)
	}
	if rt.Version() != version {
		t.Fatalf("version moved on an unchanged reload: %d -> %d", version, rt.Version())
	}
}

// Removing a server retires its process, and a call already in flight finishes
// before the session is closed under it.
func TestMCPRuntimeRetiresAfterInFlightCalls(t *testing.T) {
	exe := buildRuntimeFixture(t)
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "one.pid")
	inline := map[string]MCPServerConfig{"one": fixtureServer(exe, pidFile)}
	rt := NewMCPRuntime(MCPRuntimeOptions{CWD: dir, Inline: inline, ConnectTimeout: 5 * time.Second})
	defer rt.Close()
	ctx := context.Background()
	if err := rt.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	pid, err := rt.Call(ctx, "one", "pid", nil)
	if err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		text string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		text, callErr := rt.Call(ctx, "one", "slow", json.RawMessage(`{"ms":300,"text":"finished"}`))
		done <- outcome{text, callErr}
	}()
	time.Sleep(50 * time.Millisecond)

	// Removing the server takes the write lock, so this reload blocks until the
	// slow call has finished.
	rt.opts.Inline = map[string]MCPServerConfig{}
	if err := rt.Reload(ctx); err != nil {
		t.Fatalf("Reload after removal: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.text != "finished" {
			t.Fatalf("in-flight call=%q err=%v want it to finish", got.text, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight call never finished")
	}
	if _, ok := rt.Status("one"); ok {
		t.Fatal("a removed server is still in the statuses")
	}
	pidNum, _ := strconv.Atoi(pid)
	if pidNum > 0 && processRunning(t, pidNum) {
		t.Fatalf("the fixture process %d is still running", pidNum)
	}
}

// Disabled entries are shown, not started, and a disabled project entry keeps
// shadowing the user file.
func TestMCPRuntimeReportsDisabledEntries(t *testing.T) {
	exe := buildRuntimeFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pidFile := filepath.Join(t.TempDir(), "shadowed.pid")
	writeRuntimeConfig(t, home, map[string]map[string]any{
		"shadowed": {"command": exe, "env": map[string]string{"FAKE_MCP_PIDFILE": pidFile}},
	})
	dir := t.TempDir()
	writeRuntimeConfig(t, dir, map[string]map[string]any{
		"shadowed": {"command": exe, "enabled": false},
	})

	rt := NewMCPRuntime(MCPRuntimeOptions{
		CWD:            dir,
		SettingSources: []SettingSource{SettingSourceUser, SettingSourceProject},
	})
	defer rt.Close()
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	status, ok := rt.Status("shadowed")
	if !ok || status.Enabled || status.State != MCPStateDisabled {
		t.Fatalf("shadowed=%+v want disabled", status)
	}
	if len(rt.Tools()) != 0 {
		t.Fatalf("tools=%v want none", rt.Tools())
	}
	if _, err := rt.Call(context.Background(), "shadowed", "echo", nil); err == nil {
		t.Fatal("a disabled server answered a call")
	}
	if _, err := os.Stat(pidFile); err == nil {
		t.Fatal("a disabled server was started")
	}
}

// Concurrent calls and reloads are what a host does when a person reconnects
// mid-turn; this is the -race test.
func TestMCPRuntimeConcurrentCallsAndReloads(t *testing.T) {
	exe := buildRuntimeFixture(t)
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "one.pid")
	rt := NewMCPRuntime(MCPRuntimeOptions{
		CWD:            dir,
		Inline:         map[string]MCPServerConfig{"one": fixtureServer(exe, pidFile)},
		ConnectTimeout: 5 * time.Second,
	})
	defer rt.Close()
	ctx := context.Background()
	if err := rt.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = rt.Call(ctx, "one", "echo", json.RawMessage(`{"text":"hi"}`))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 5; j++ {
			_ = rt.Reload(ctx)
			_ = rt.Reconnect(ctx, "one")
			_ = rt.Statuses()
			_ = rt.Tools()
		}
	}()
	wg.Wait()

	// Whatever the interleaving, the server ends connected and callable.
	if status, _ := rt.Status("one"); status.State != MCPStateConnected {
		t.Fatalf("status=%+v want connected", status)
	}
	if _, err := rt.Call(ctx, "one", "echo", json.RawMessage(`{"text":"after"}`)); err != nil {
		t.Fatalf("Call after the race: %v", err)
	}
}

// RefreshMCP refuses during a run, and the next Send picks the change up by
// itself. The running-turn rule is what keeps a tool registry stable.
func TestAgentRefreshMCPDefersAndSendRefreshes(t *testing.T) {
	exe := buildRuntimeFixture(t)
	dir := t.TempDir()
	writeRuntimeConfig(t, dir, map[string]map[string]any{
		"one": {"command": exe},
	})
	rt := NewMCPRuntime(MCPRuntimeOptions{
		CWD:            dir,
		SettingSources: []SettingSource{SettingSourceProject},
		ConnectTimeout: 5 * time.Second,
	})
	defer rt.Close()
	ctx := context.Background()
	if err := rt.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir}, MCPRuntime: rt,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	if err := a.RefreshMCP(); err == nil {
		t.Fatal("RefreshMCP accepted a refresh while a run was marked in progress")
	}
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()

	// A new server appears in the file; an idle refresh registers its tools.
	writeRuntimeConfig(t, dir, map[string]map[string]any{
		"one": {"command": exe},
		"two": {"command": exe},
	})
	if err := rt.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.RefreshMCP(); err != nil {
		t.Fatalf("RefreshMCP: %v", err)
	}
	if !containsName(a.registry.Names(), "mcp_two_echo") {
		t.Fatalf("registry=%v want the new server's tool", a.registry.Names())
	}

	// A third server appears; Send is what notices without an explicit refresh.
	writeRuntimeConfig(t, dir, map[string]map[string]any{
		"one":   {"command": exe},
		"two":   {"command": exe},
		"three": {"command": exe},
	})
	if err := rt.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	a.complete = fixedResponse{resp: &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}
	run, err := a.Send(ctx, "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !containsName(a.registry.Names(), "mcp_three_echo") {
		t.Fatalf("registry=%v want Send to have refreshed", a.registry.Names())
	}
}

// The non-runtime path is unchanged: inline servers still start and fail fast.
func TestInlineMCPServersStillFailFastWithoutARuntime(t *testing.T) {
	dir := t.TempDir()
	_, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
		MCPServers: map[string]MCPServerConfig{
			"broken": {Command: filepath.Join(dir, "does-not-exist")},
		},
	})
	if err == nil {
		t.Fatal("a broken inline server did not fail Create")
	}
}
