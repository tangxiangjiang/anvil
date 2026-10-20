// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil"
)

// The wire block is mapped, not reinterpreted: every field the client sent
// arrives at the SDK unchanged, and Required stays false so a language server
// that cannot start later is a missing tool rather than a refused session.
func TestBuildOptionsMapsLSP(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	opts, rpcErr := h.srv.buildOptions(context.Background(), SessionParams{
		LSP: &LSPParams{Enabled: true, Servers: []LSPServerParams{{
			Language: "go",
			Pattern:  "**/*.go",
			Command:  "gopls",
			Args:     []string{"-remote=auto"},
		}}},
	}, &session{mode: ModeAsk, loadPermissions: true})
	if rpcErr != nil {
		t.Fatalf("buildOptions: %+v", rpcErr)
	}
	want := anvil.LSPOptions{
		Enabled: true,
		Servers: []anvil.LSPServerConfig{{
			Language: "go",
			Pattern:  "**/*.go",
			Command:  "gopls",
			Args:     []string{"-remote=auto"},
		}},
	}
	if !reflect.DeepEqual(opts.LSP, want) {
		t.Fatalf("LSP=%+v want %+v", opts.LSP, want)
	}
	if opts.LSP.Required {
		t.Fatal("Required must stay false: a missing server may not refuse the session")
	}
}

// An absent block and a switched-off block both leave the options empty, which
// is what keeps an unconfigured client's session.new exactly as it was.
func TestBuildOptionsLeavesLSPOff(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	for name, params := range map[string]SessionParams{
		"absent": {},
		"off":    {LSP: &LSPParams{Enabled: false, Servers: []LSPServerParams{{Language: "go", Command: "gopls"}}}},
	} {
		opts, rpcErr := h.srv.buildOptions(context.Background(), params, &session{mode: ModeAsk, loadPermissions: true})
		if rpcErr != nil {
			t.Fatalf("%s: buildOptions: %+v", name, rpcErr)
		}
		if opts.LSP.Enabled || len(opts.LSP.Servers) != 0 {
			t.Fatalf("%s: LSP should be off, got %+v", name, opts.LSP)
		}
	}
}

// An entry that could never start a process is refused before the session
// exists, with the entry's index named: "something is wrong with lsp" is not
// an actionable message.
func TestBuildOptionsRefusesAnIncompleteLSPEntry(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	cases := []struct {
		name   string
		server LSPServerParams
		want   string
	}{
		{"no command", LSPServerParams{Language: "go"}, "lsp.servers[0]: command is required"},
		{"no selector", LSPServerParams{Command: "gopls"}, "lsp.servers[0]: language or pattern is required"},
		{"blank command", LSPServerParams{Language: "go", Command: "  "}, "lsp.servers[0]: command is required"},
	}
	for _, tc := range cases {
		_, rpcErr := h.srv.buildOptions(context.Background(), SessionParams{
			LSP: &LSPParams{Enabled: true, Servers: []LSPServerParams{tc.server}},
		}, &session{mode: ModeAsk, loadPermissions: true})
		if rpcErr == nil {
			t.Fatalf("%s: want an error", tc.name)
		}
		if !strings.Contains(rpcErr.Message, tc.want) {
			t.Fatalf("%s: message %q does not name the problem (%q)", tc.name, rpcErr.Message, tc.want)
		}
	}
}

// A command that exists but cannot run (or does not exist) must not stop a
// session from opening. The tools that did start stay available.
func TestALanguageServerThatCannotStartDoesNotRefuseTheSession(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	res := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		LSP: &LSPParams{Enabled: true, Servers: []LSPServerParams{{
			Language: "go",
			Command:  "anvil-nonexistent-lsp-binary",
		}}},
	}))
	if id, _ := res["session_id"].(string); id == "" {
		t.Fatalf("session.new returned no session: %v", res)
	}
}

// The test and build commands have been in the protocol since 0.18; the host
// now says so when the first word is empty rather than handing the SDK a
// command it cannot run.
func TestBuildOptionsRefusesAnEmptyCommandWord(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	for name, params := range map[string]SessionParams{
		"test":  {TestCommand: []string{" "}},
		"build": {BuildCommand: []string{""}},
	} {
		_, rpcErr := h.srv.buildOptions(context.Background(), params, &session{mode: ModeAsk, loadPermissions: true})
		if rpcErr == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}
