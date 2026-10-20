// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil"
)

// isolateHome points the user layer and the OAuth token directory at the test's
// temp tree. Without it, a test that selects the user source would read the
// machine's real ~/.cursor/mcp.json and start whatever it names. Both variables
// are set because the home directory is resolved per platform.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// The user rule file resolves through ANVIL_HOME first, so pointing only the
	// home directory would leave every test sharing one policy file (0.47).
	t.Setenv("ANVIL_HOME", filepath.Join(home, ".anvil"))
	return home
}

func writeMCPFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The panel's card is only as good as this answer: a server the host names here
// is one the session will try to load, and the file it came from is what a
// person edits to change it.
func TestMCPListReportsTheProjectFile(t *testing.T) {
	h, dir := initHost(t, "")
	path := filepath.Join(dir, ".cursor", "mcp.json")
	// Both servers are unreachable on purpose: the listing must describe them
	// from the file without a dial deciding the answer, and the ports are closed
	// so the host's background reload fails at once instead of waiting.
	writeMCPFile(t, path, `{"mcpServers": {
		"files": {"command": "anvil-test-no-such-command"},
		"docs": {"url": "http://127.0.0.1:1/mcp"}
	}}`)

	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{SettingSources: []string{"project"}}))

	// The runtime is what this host asks the SDK for; an older host has no such
	// field, and the extension degrades on its absence.
	if res["runtime"] != true {
		t.Fatalf("runtime=%v want true", res["runtime"])
	}
	servers, _ := res["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("servers=%v want two", servers)
	}
	first, _ := servers[0].(map[string]any)
	if first["name"] != "docs" || first["transport"] != "http" || first["source"] != path {
		t.Fatalf("docs=%v want http from %s", first, path)
	}
	second, _ := servers[1].(map[string]any)
	if second["name"] != "files" || second["transport"] != "stdio" {
		t.Fatalf("files=%v want stdio", second)
	}
	// State describes the runtime, never the file. A server that was never
	// reached is not connected, whatever the file says about it.
	for _, server := range servers {
		info, _ := server.(map[string]any)
		switch info["state"] {
		case nil, "", "connecting", "failed", "disabled":
		default:
			t.Fatalf("%v claims state %q without a connection", info["name"], info["state"])
		}
		if count, _ := info["tool_count"].(float64); count != 0 {
			t.Fatalf("%v tool_count=%v want 0", info["name"], count)
		}
	}

	sources, _ := res["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources=%v want the project layer", sources)
	}
	source, _ := sources[0].(map[string]any)
	if source["source"] != "project" || source["path"] != path || source["exists"] != true {
		t.Fatalf("source=%v want the present project file", source)
	}
}

// With the setting off the client sends no source, and the host reads no file.
func TestMCPListReadsNothingWhenNoSourceIsAsked(t *testing.T) {
	h, dir := initHost(t, "")
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"), `{"mcpServers": {"files": {"command": "npx"}}}`)

	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{}))
	if servers, _ := res["servers"].([]any); len(servers) != 0 {
		t.Fatalf("servers=%v want none", servers)
	}
	if sources, _ := res["sources"].([]any); len(sources) != 0 {
		t.Fatalf("sources=%v want none", sources)
	}
}

// An enabled layer with no file is a note for the panel, not a failure.
func TestMCPListReportsAnAbsentProjectFile(t *testing.T) {
	h, _ := initHost(t, "")

	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{SettingSources: []string{"project"}}))
	sources, _ := res["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources=%v want the project layer", sources)
	}
	source, _ := sources[0].(map[string]any)
	if source["exists"] != false {
		t.Fatalf("source=%v want exists=false", source)
	}
}

// A malformed file is a problem beside the listing, not a failed call: the panel
// has to show the file's line beside the last good statuses, and it cannot do
// that with an error string alone.
func TestMCPListReportsAMalformedFileAsAProblem(t *testing.T) {
	h, dir := initHost(t, "")
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, path, `{"mcpServers": {`)

	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{SettingSources: []string{"project"}}))

	problem, ok := res["problem"].(map[string]any)
	if !ok {
		t.Fatalf("result=%v want a problem", res)
	}
	if problem["path"] != path {
		t.Fatalf("problem=%v want the path %s", problem, path)
	}
	if msg, _ := problem["message"].(string); msg == "" {
		t.Fatalf("problem=%v want a message", problem)
	}
	if line, _ := problem["line"].(float64); line < 1 {
		t.Fatalf("problem=%v want the line the file breaks on", problem)
	}
	// Nothing parsed, so nothing is listed — an invented empty server set would
	// read as "you have no servers".
	if servers, _ := res["servers"].([]any); len(servers) != 0 {
		t.Fatalf("servers=%v want none", servers)
	}
}

// The host and the session read the same field, so they accept the same enum.
func TestMCPListRejectsAnUnknownSource(t *testing.T) {
	h, _ := initHost(t, "")

	m := h.call(MethodMCPList, MCPListParams{SettingSources: []string{"galaxy"}})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("response=%v want invalid params", m)
	}
}

// The acceptance's first clause: turning the setting on is what puts the source
// on the session's options. This is checked at buildOptions rather than through
// a run, because the source never reaches the wire again after this point.
func TestBuildOptionsCarriesTheSelectedSettingSources(t *testing.T) {
	dir := newWorkspace(t)
	// The user layer is read from the home directory; pointing it at the
	// workspace keeps the test off the machine's real mcp.json, which may name
	// servers this test would then try to start.
	isolateHome(t)
	srv := New(Config{CWD: dir, Model: "stub-model"}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-test", srv.cwdPath(), ModeAsk, false)

	opts, rpcErr := srv.buildOptions(context.Background(), SessionParams{SettingSources: []string{"project", " user "}}, sess)
	if rpcErr != nil {
		t.Fatalf("buildOptions: %s", rpcErr.Message)
	}
	if len(opts.Local.SettingSources) != 2 ||
		opts.Local.SettingSources[0] != anvil.SettingSourceProject ||
		opts.Local.SettingSources[1] != anvil.SettingSourceUser {
		t.Fatalf("setting sources=%v want [project user]", opts.Local.SettingSources)
	}
	if !opts.Local.HasSettingSource(anvil.SettingSourceProject) {
		t.Fatal("the project source is not enabled")
	}

	// Off is the empty list, which is the SDK's inline-only behaviour.
	opts, rpcErr = srv.buildOptions(context.Background(), SessionParams{}, sess)
	if rpcErr != nil {
		t.Fatalf("buildOptions: %s", rpcErr.Message)
	}
	if len(opts.Local.SettingSources) != 0 {
		t.Fatalf("setting sources=%v want none", opts.Local.SettingSources)
	}
}
