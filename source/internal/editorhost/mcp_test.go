// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil"
)

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
	writeMCPFile(t, path, `{"mcpServers": {
		"files": {"command": "npx", "args": ["server-files"]},
		"docs": {"url": "https://example.test/mcp"}
	}}`)

	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{SettingSources: []string{"project"}}))

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
	// There is no connected state on the wire. A field that said "running"
	// would only mean the file mentioned it.
	if _, ok := first["connected"]; ok {
		t.Fatalf("the listing invented a connection state: %v", first)
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

// A malformed file is the interesting failure: an empty listing would hide the
// one thing the user can fix.
func TestMCPListFailsOnAMalformedFile(t *testing.T) {
	h, dir := initHost(t, "")
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"), `{"mcpServers": {`)

	m := h.call(MethodMCPList, MCPListParams{SettingSources: []string{"project"}})
	rpcErr, ok := rpcErrorOf(m)
	if !ok {
		t.Fatalf("a malformed file produced a result: %v", m)
	}
	if rpcErr.Code != CodeHostError {
		t.Fatalf("code=%d want %d", rpcErr.Code, CodeHostError)
	}
	if !strings.Contains(rpcErr.Message, "parse") || !strings.Contains(rpcErr.Message, "mcp.json") {
		t.Fatalf("message=%q want it to name the parse failure and the file", rpcErr.Message)
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
	srv := New(Config{CWD: dir, Model: "stub-model"}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-test", srv.cwdPath(), ModeAsk, false)

	opts, rpcErr := srv.buildOptions(SessionParams{SettingSources: []string{"project", " user "}}, sess)
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
	opts, rpcErr = srv.buildOptions(SessionParams{}, sess)
	if rpcErr != nil {
		t.Fatalf("buildOptions: %s", rpcErr.Message)
	}
	if len(opts.Local.SettingSources) != 0 {
		t.Fatalf("setting sources=%v want none", opts.Local.SettingSources)
	}
}
