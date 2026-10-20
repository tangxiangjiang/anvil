// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

// fakeMCPServer speaks the Streamable HTTP shape the SDK client expects: JSON
// bodies, a session id on initialize, and 202 for notifications. healthy is
// optional; when it reports false the server answers 500, which is how a
// reconnect test gets a server that failed and then recovered.
func fakeMCPServer(t *testing.T, healthy *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy != nil && !healthy.Load() {
			http.Error(w, "down for maintenance", http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "test-session")
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "echoes text",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"text": map[string]any{"type": "string"}},
				},
			}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeProjectMCP(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, path, body)
	return path
}

func readMCPFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s does not parse: %v\n%s", path, err, raw)
	}
	return out
}

// mcpEntry returns one server object from a `mcpServers` map.
func mcpEntry(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	servers, _ := doc["mcpServers"].(map[string]any)
	entry, _ := servers[name].(map[string]any)
	if entry == nil {
		t.Fatalf("%s has no %q entry: %v", "mcp.json", name, doc)
	}
	return entry
}

// serversByName indexes a listing result.
func serversByName(t *testing.T, res map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	list, _ := res["servers"].([]any)
	for _, item := range list {
		server, _ := item.(map[string]any)
		name, _ := server["name"].(string)
		out[name] = server
	}
	return out
}

// waitForMCPState polls the listing until the server reaches want. States are
// pushed as they happen, so a test that wants a settled one has to wait.
func waitForMCPState(t *testing.T, h *harness, params MCPListParams, name, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last map[string]any
	for {
		res := resultOf(t, h.callOK(MethodMCPList, params))
		servers := serversByName(t, res)
		last = servers[name]
		if last != nil && last["state"] == want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached %q; last=%v", name, want, last)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The milestone's first acceptance: one broken server does not refuse the
// session, and the listing says which is which.
func TestSessionOpensWithOneBrokenServer(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	fake := fakeMCPServer(t, nil)
	writeProjectMCP(t, dir, fmt.Sprintf(`{"mcpServers": {
		"good": {"url": %q},
		"broken": {"command": "anvil-test-no-such-command"}
	}}`, fake.URL))

	sessionID := startSession(t, h, SessionParams{SettingSources: []string{"project"}})
	if sessionID == "" {
		t.Fatal("a broken MCP server refused the session")
	}

	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{
		SettingSources: []string{"project"},
		IncludeTools:   true,
	}))
	servers := serversByName(t, res)
	good := servers["good"]
	if good == nil || good["state"] != "connected" {
		t.Fatalf("good=%v want connected", good)
	}
	if count, _ := good["tool_count"].(float64); count != 1 {
		t.Fatalf("good tool_count=%v want 1", good["tool_count"])
	}
	tools, _ := good["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("good tools=%v want the one tool", good["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "echo" || tool["description"] != "echoes text" {
		t.Fatalf("tool=%v want the fake server's echo tool", tool)
	}
	broken := servers["broken"]
	if broken == nil || broken["state"] != "failed" {
		t.Fatalf("broken=%v want failed", broken)
	}
	if msg, _ := broken["error"].(string); msg == "" {
		t.Fatalf("broken=%v want the reason it failed", broken)
	}
}

// The tools list is what a panel opens; the chat card only needs the count.
func TestMCPListIncludesToolsOnlyWhenAsked(t *testing.T) {
	h, dir := initHost(t, "")
	fake := fakeMCPServer(t, nil)
	writeProjectMCP(t, dir, fmt.Sprintf(`{"mcpServers": {"good": {"url": %q}}}`, fake.URL))

	startSession(t, h, SessionParams{SettingSources: []string{"project"}})

	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{SettingSources: []string{"project"}}))
	good := serversByName(t, res)["good"]
	if good == nil || good["state"] != "connected" {
		t.Fatalf("good=%v want connected", good)
	}
	if _, ok := good["tools"]; ok {
		t.Fatalf("tools=%v want none without include_tools", good["tools"])
	}
}

// A state transition is pushed, so a panel does not have to poll to notice that
// a server came up.
func TestMCPStatusNotificationDescribesTheTransition(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	fake := fakeMCPServer(t, nil)
	writeProjectMCP(t, dir, fmt.Sprintf(`{"mcpServers": {"good": {"url": %q}}}`, fake.URL))

	startSession(t, h, SessionParams{SettingSources: []string{"project"}})

	// session.new connects before it answers, so the notifications are already
	// queued when the reply arrives.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := h.next(time.Second)
		if m["method"] != NotifyMCPStatus {
			continue
		}
		params := paramsOf(t, m)
		server, _ := params["server"].(map[string]any)
		if server["name"] != "good" || server["state"] != "connected" {
			continue
		}
		sources, _ := params["setting_sources"].([]any)
		if len(sources) != 1 || sources[0] != "project" {
			t.Fatalf("setting_sources=%v want [project]", params["setting_sources"])
		}
		return
	}
	t.Fatal("no connected notification for the good server")
}

func TestMCPAddWritesTheProjectFileAndKeepsUnknownFields(t *testing.T) {
	h, dir := initHost(t, "")
	path := writeProjectMCP(t, dir, `{"mcpServers": {}, "otherTool": {"keep": true}}`)

	res := resultOf(t, h.callOK(MethodMCPAdd, MCPAddParams{
		Scope: "project",
		Server: MCPServerDraftWire{
			Name:      "docs",
			Transport: "stdio",
			Command:   "anvil-test-no-such-command",
			Args:      []string{"--stdio"},
			Env:       map[string]string{"TOKEN": "s3cret-value"},
		},
		SettingSources: []string{"project"},
	}))

	doc := readMCPFile(t, path)
	if _, ok := doc["otherTool"]; !ok {
		t.Fatalf("the write dropped a field it does not own: %v", doc)
	}
	entry := mcpEntry(t, doc, "docs")
	if entry["command"] != "anvil-test-no-such-command" {
		t.Fatalf("entry=%v want the command", entry)
	}
	env, _ := entry["env"].(map[string]any)
	if env["TOKEN"] != "s3cret-value" {
		t.Fatalf("env=%v want the value written", env)
	}
	if _, ok := entry["enabled"]; ok {
		t.Fatalf("entry=%v want no enabled key for the default", entry)
	}

	// The listing reports the keys, never the values.
	docs := serversByName(t, res)["docs"]
	if docs == nil {
		t.Fatalf("result=%v want the new server listed", res)
	}
	names, _ := docs["env_names"].([]any)
	if len(names) != 1 || names[0] != "TOKEN" {
		t.Fatalf("env_names=%v want [TOKEN]", docs["env_names"])
	}
	if strings.Contains(fmt.Sprint(res), "s3cret-value") {
		t.Fatalf("the listing carried a secret value: %v", res)
	}
}

func TestMCPAddToUserScopeWritesTheUserFile(t *testing.T) {
	h, _ := initHost(t, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}

	h.callOK(MethodMCPAdd, MCPAddParams{
		Scope: "user",
		Server: MCPServerDraftWire{
			Name:    "docs",
			Command: "anvil-test-no-such-command",
		},
		SettingSources: []string{"user"},
	})

	path := filepath.Join(home, ".cursor", "mcp.json")
	entry := mcpEntry(t, readMCPFile(t, path), "docs")
	if entry["command"] != "anvil-test-no-such-command" {
		t.Fatalf("entry=%v want the command in the user file", entry)
	}
}

// Disabling writes the key, re-enabling removes it: an entry that was never
// touched must not gain an `enabled: true` on re-enable.
func TestMCPSetEnabledWritesAndDeletesTheKey(t *testing.T) {
	h, dir := initHost(t, "")
	path := writeProjectMCP(t, dir, `{"mcpServers": {
		"docs": {"command": "anvil-test-no-such-command", "note": "keep me"}
	}}`)
	params := func(enabled bool) MCPSetEnabledParams {
		return MCPSetEnabledParams{
			Scope:          "project",
			Name:           "docs",
			Enabled:        enabled,
			SettingSources: []string{"project"},
		}
	}

	h.callOK(MethodMCPSetEnabled, params(false))
	entry := mcpEntry(t, readMCPFile(t, path), "docs")
	if entry["enabled"] != false {
		t.Fatalf("entry=%v want enabled false", entry)
	}
	if entry["note"] != "keep me" {
		t.Fatalf("entry=%v want the unknown per-server field kept", entry)
	}
	res := resultOf(t, h.callOK(MethodMCPList, MCPListParams{SettingSources: []string{"project"}}))
	docs := serversByName(t, res)["docs"]
	if docs == nil || docs["enabled"] != false || docs["state"] != "disabled" {
		t.Fatalf("docs=%v want disabled", docs)
	}

	h.callOK(MethodMCPSetEnabled, params(true))
	entry = mcpEntry(t, readMCPFile(t, path), "docs")
	if _, ok := entry["enabled"]; ok {
		t.Fatalf("entry=%v want the enabled key gone", entry)
	}
	waitForMCPState(t, h, MCPListParams{SettingSources: []string{"project"}}, "docs", string(anvil.MCPStateFailed))
}

// An edit form that does not carry the checkbox must not turn a disabled server
// back on: the file's value is what the update inherits.
func TestMCPUpdateKeepsTheDisabledState(t *testing.T) {
	h, dir := initHost(t, "")
	path := writeProjectMCP(t, dir, `{"mcpServers": {
		"docs": {"command": "anvil-test-old", "enabled": false, "vendor": {"keep": true}}
	}}`)

	res := resultOf(t, h.callOK(MethodMCPUpdate, MCPUpdateParams{
		Scope: "project",
		Server: MCPServerDraftWire{
			Name:    "docs",
			Command: "anvil-test-new",
		},
		SettingSources: []string{"project"},
	}))

	entry := mcpEntry(t, readMCPFile(t, path), "docs")
	if entry["command"] != "anvil-test-new" {
		t.Fatalf("entry=%v want the new command", entry)
	}
	if entry["enabled"] != false {
		t.Fatalf("entry=%v want it still disabled", entry)
	}
	if _, ok := entry["vendor"]; !ok {
		t.Fatalf("entry=%v want the unknown field kept", entry)
	}
	if docs := serversByName(t, res)["docs"]; docs == nil || docs["enabled"] != false {
		t.Fatalf("docs=%v want it reported disabled", docs)
	}
}

func TestMCPRemoveDeletesOnlyThatEntry(t *testing.T) {
	h, dir := initHost(t, "")
	path := writeProjectMCP(t, dir, `{"mcpServers": {
		"docs": {"command": "anvil-test-no-such-command"},
		"notes": {"command": "anvil-test-no-such-command"}
	}, "otherTool": {"keep": true}}`)

	res := resultOf(t, h.callOK(MethodMCPRemove, MCPRemoveParams{
		Scope:          "project",
		Name:           "docs",
		SettingSources: []string{"project"},
	}))

	doc := readMCPFile(t, path)
	if _, ok := doc["otherTool"]; !ok {
		t.Fatalf("the removal dropped a field it does not own: %v", doc)
	}
	if _, ok := mcpEntry(t, doc, "notes")["command"]; !ok {
		t.Fatalf("the removal dropped a sibling: %v", doc)
	}
	if servers := serversByName(t, res); len(servers) != 1 || servers["notes"] == nil {
		t.Fatalf("servers=%v want only notes", res["servers"])
	}
}

// A malformed file is refused before anything is written: the bytes on disk are
// the user's, and a half-edit is worse than a failure.
func TestMCPWriteRefusesAMalformedFile(t *testing.T) {
	h, dir := initHost(t, "")
	path := writeProjectMCP(t, dir, `{"mcpServers": {`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	m := h.call(MethodMCPAdd, MCPAddParams{
		Scope:          "project",
		Server:         MCPServerDraftWire{Name: "docs", Command: "anvil-test-no-such-command"},
		SettingSources: []string{"project"},
	})
	if _, ok := rpcErrorOf(m); !ok {
		t.Fatalf("a write to a malformed file succeeded: %v", m)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the file changed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestMCPReconnectRestartsAFailedServer(t *testing.T) {
	h, dir := initHost(t, "")
	var healthy atomic.Bool
	fake := fakeMCPServer(t, &healthy)
	writeProjectMCP(t, dir, fmt.Sprintf(`{"mcpServers": {"good": {"url": %q}}}`, fake.URL))

	startSession(t, h, SessionParams{SettingSources: []string{"project"}})
	waitForMCPState(t, h, MCPListParams{SettingSources: []string{"project"}}, "good", string(anvil.MCPStateFailed))

	healthy.Store(true)
	res := resultOf(t, h.callOK(MethodMCPReconnect, MCPReconnectParams{
		Name:           "good",
		SettingSources: []string{"project"},
		IncludeTools:   true,
	}))
	good := serversByName(t, res)["good"]
	if good == nil || good["state"] != "connected" {
		t.Fatalf("good=%v want connected after reconnect", good)
	}
	if count, _ := good["tool_count"].(float64); count != 1 {
		t.Fatalf("tool_count=%v want 1", good["tool_count"])
	}
	if _, ok := good["connected_at"].(string); !ok {
		t.Fatalf("good=%v want the time it connected", good)
	}
}

func TestMCPManagementRefusesAnUnknownScopeOrName(t *testing.T) {
	h, _ := initHost(t, "")

	m := h.call(MethodMCPAdd, MCPAddParams{
		Scope:          "galaxy",
		Server:         MCPServerDraftWire{Name: "docs", Command: "anvil-test-no-such-command"},
		SettingSources: []string{"project"},
	})
	if rpcErr, ok := rpcErrorOf(m); !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("response=%v want invalid params", m)
	}

	m = h.call(MethodMCPRemove, MCPRemoveParams{Scope: "project", Name: " ", SettingSources: []string{"project"}})
	if rpcErr, ok := rpcErrorOf(m); !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("response=%v want invalid params", m)
	}

	// Signing in to a server no file declares has nothing to open.
	m = h.call(MethodMCPAuthenticate, MCPAuthenticateParams{Name: "docs", SettingSources: []string{"project"}})
	if rpcErr, ok := rpcErrorOf(m); !ok || rpcErr.Code != CodeHostError {
		t.Fatalf("response=%v want a host error", m)
	}
}

// Shutdown owns the processes: the runtimes outlive sessions but not the host.
func TestShutdownClosesEveryRuntime(t *testing.T) {
	h, dir := initHost(t, "")
	fake := fakeMCPServer(t, nil)
	writeProjectMCP(t, dir, fmt.Sprintf(`{"mcpServers": {"good": {"url": %q}}}`, fake.URL))
	startSession(t, h, SessionParams{SettingSources: []string{"project"}})
	waitForMCPState(t, h, MCPListParams{SettingSources: []string{"project"}}, "good", string(anvil.MCPStateConnected))

	h.srv.mcpMu.Lock()
	before := len(h.srv.mcp)
	h.srv.mcpMu.Unlock()
	if before == 0 {
		t.Fatal("no runtime was registered")
	}

	h.srv.closeAll()

	h.srv.mcpMu.Lock()
	after := len(h.srv.mcp)
	h.srv.mcpMu.Unlock()
	if after != 0 {
		t.Fatalf("runtimes=%d after shutdown want 0", after)
	}
}
