// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/mcp"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestCustomToolAndCollision(t *testing.T) {
	dir := t.TempDir()
	called := false
	opts := anvil.Options{
		APIKey: "k",
		Model:  anvil.Model{ID: "m"},
		Local:  anvil.LocalOptions{CWD: dir},
		Tools: []anvil.ToolSpec{{
			Name:        "ping",
			Description: "ping",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				called = true
				return "pong", nil
			},
		}},
	}
	a, err := anvil.Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Collision with built-in
	_, err = anvil.Create(context.Background(), anvil.Options{
		APIKey: "k",
		Model:  anvil.Model{ID: "m"},
		Local:  anvil.LocalOptions{CWD: dir},
		Tools: []anvil.ToolSpec{{
			Name:    "read_file",
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) { return "", nil },
		}},
	})
	if err == nil {
		t.Fatal("expected collision error")
	}

	comp := &scriptCompleter{replies: []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "ping", Args: json.RawMessage(`{}`)}}},
		{Role: llm.RoleAssistant, Content: "done"},
	}}
	reg := tools.NewRegistry(dir, false)
	_ = reg.Register(&tools.FuncTool{Name: "ping", Handler: opts.Tools[0].Handler, Parameters: opts.Tools[0].Parameters})
	res := runtime.Loop(context.Background(), comp, reg, runtime.Config{CWD: dir, Model: "m", UserMsg: "x", MaxSteps: 5}, nil)
	if res.Status != "finished" || !called {
		t.Fatalf("status=%s called=%v err=%s", res.Status, called, res.Err)
	}
}

func TestPermissionDenyWrite(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	reg.SetPermission(func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		return nil, context.Canceled // any error denies
	})
	// replace with proper deny message style via anvil hook path
	reg.SetPermission(func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		return nil, errDeny("no writes")
	})
	res := reg.Execute(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`))
	if !res.IsError || res.Content != "no writes" {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("file should not exist")
	}
}

type denyErr string

func (e denyErr) Error() string { return string(e) }
func errDeny(s string) error    { return denyErr(s) }

func TestCompressOverBudget(t *testing.T) {
	var msgs []llm.Message
	for i := 0; i < 20; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "msg-" + string(rune('a'+i%26)) + " " + long(100)})
	}
	out, err := compress.Apply(context.Background(), nil, "m", msgs, compress.Options{MaxChars: 500, KeepRecent: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !out.DidCompress {
		t.Fatal("expected compress")
	}
	if len(out.Messages) != 5 { // summary + 4 recent
		t.Fatalf("len=%d", len(out.Messages))
	}
}

func TestMCPFakeServer(t *testing.T) {
	exe := buildFakeMCP(t)
	sess, err := mcp.Start(context.Background(), mcp.ServerConfig{
		Name:    "fake",
		Command: exe,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	tools := sess.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("%+v", tools)
	}
	out, err := sess.Call(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || out != "echo:hi" {
		t.Fatalf("out=%q err=%v", out, err)
	}

	dir := t.TempDir()
	a, err := anvil.Create(context.Background(), anvil.Options{
		APIKey: "k",
		Model:  anvil.Model{ID: "m"},
		Local:  anvil.LocalOptions{CWD: dir},
		MCPServers: map[string]anvil.MCPServerConfig{
			"fake": {Command: exe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
}

type scriptCompleter struct {
	replies []llm.Message
	n       int
}

func (s *scriptCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if s.n >= len(s.replies) {
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "end"}}, nil
	}
	m := s.replies[s.n]
	s.n++
	return &llm.Response{Message: m}, nil
}

func long(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func buildFakeMCP(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakemcp.exe")
	cmd := exec.Command("go", "build", "-o", out, "./internal/mcp/fakecmd")
	cmd.Dir = findSourceRoot(t)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake mcp: %v\n%s", err, outBytes)
	}
	return out
}

func findSourceRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// tests may run with cwd = source/
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
		return wd
	}
	return wd
}
