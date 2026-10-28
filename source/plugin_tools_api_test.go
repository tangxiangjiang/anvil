// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestPluginSampleLoadedViaOptions(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey:  "k",
		Model:   Model{ID: "m"},
		Local:   LocalOptions{CWD: dir},
		Plugins: []string{"sample"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !toolPresent(a, "plugin_ping") {
		t.Fatal("plugin_ping missing")
	}
	res := a.registry.Execute(context.Background(), "plugin_ping", json.RawMessage(`{}`))
	if res.IsError || !strings.Contains(res.Content, "pong") {
		t.Fatalf("%+v", res)
	}
}

func TestPluginSampleAbsentByDefault(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if toolPresent(a, "plugin_ping") {
		t.Fatal("plugin_ping must be absent without Options.Plugins")
	}
}

func TestPluginDeniedByPolicy(t *testing.T) {
	dir := t.TempDir()
	_, err := Create(context.Background(), Options{
		APIKey:  "k",
		Model:   Model{ID: "m"},
		Local:   LocalOptions{CWD: dir},
		Plugins: []string{"sample"},
		PluginPolicy: &PluginPolicy{
			Deny: []string{"sample"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected deny, got %v", err)
	}
}

func TestPluginInLoop(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey:  "k",
		Model:   Model{ID: "m"},
		Local:   LocalOptions{CWD: dir},
		Plugins: []string{"sample"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = &pluginPingCompleter{}
	run, err := a.Send(context.Background(), "ping")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil || res.Result != "ponged" {
		t.Fatalf("%v %+v", err, res)
	}
}

type pluginPingCompleter struct{ n int }

func (c *pluginPingCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	c.n++
	if c.n == 1 {
		found := false
		for _, t := range req.Tools {
			if t.Name == "plugin_ping" {
				found = true
				break
			}
		}
		if !found {
			return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "no tool"}}, nil
		}
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID: "1", Name: "plugin_ping", Args: json.RawMessage(`{}`),
			}},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ponged"}}, nil
}

func TestExecPluginRoundTrip(t *testing.T) {
	bin := buildFakePlugin(t)
	dir := t.TempDir()
	plugRoot := filepath.Join(dir, "plugins", "fakeexec")
	if err := os.MkdirAll(plugRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	man := `{
		"id":"fakeexec","version":"0.1.0","anvil_api":1,
		"capabilities":["tools.fake_echo"],
		"permissions":["fs.read","net.none","process.spawn"],
		"command":` + jsonQuote(bin) + `
	}`
	if err := os.WriteFile(filepath.Join(plugRoot, "plugin.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey:     "k",
		Model:      Model{ID: "m"},
		Local:      LocalOptions{CWD: dir},
		Plugins:    []string{"fakeexec"},
		PluginDirs: []string{filepath.Join(dir, "plugins")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !toolPresent(a, "fake_echo") {
		t.Fatal("fake_echo missing")
	}
	res := a.registry.Execute(context.Background(), "fake_echo", mustJSON(map[string]any{"message": "hi"}))
	if res.IsError || res.Content != "echo:hi" {
		t.Fatalf("%+v", res)
	}
}

func TestExecPluginCrashBoundedError(t *testing.T) {
	bin := buildFakePlugin(t)
	dir := t.TempDir()
	plugRoot := filepath.Join(dir, "plugins", "fakeexec")
	_ = os.MkdirAll(plugRoot, 0o755)
	man := `{
		"id":"fakeexec","version":"0.1.0","anvil_api":1,
		"capabilities":["tools.fake_echo"],
		"permissions":["fs.read","net.none","process.spawn"],
		"command":` + jsonQuote(bin) + `
	}`
	_ = os.WriteFile(filepath.Join(plugRoot, "plugin.json"), []byte(man), 0o644)
	a, err := Create(context.Background(), Options{
		APIKey:     "k",
		Model:      Model{ID: "m"},
		Local:      LocalOptions{CWD: dir},
		Plugins:    []string{"fakeexec"},
		PluginDirs: []string{filepath.Join(dir, "plugins")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Inject crash env by re-exec is hard; call after Close to simulate dead process.
	_ = a.plugins.Close(context.Background())
	res := a.registry.Execute(context.Background(), "fake_echo", mustJSON(map[string]any{"message": "x"}))
	if !res.IsError {
		t.Fatalf("expected error after plugin closed, got %+v", res)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func buildFakePlugin(t *testing.T) string {
	t.Helper()
	src := filepath.Join("internal", "plugin", "fakecmd")
	out := filepath.Join(t.TempDir(), "fakecmd")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./"+filepath.ToSlash(src))
	cmd.Dir = "."
	cmd.Env = os.Environ()
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakecmd: %v\n%s", err, outBytes)
	}
	return out
}
