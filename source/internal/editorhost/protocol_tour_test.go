// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestProtocolTour is the offline acceptance path for the editor-host wire:
// handshake, catalogues, empty-file MCP listing, builtin tools, and session
// admin. It must stay free of live providers and MCP child processes so CI can
// run it on every push (p_0.45.1).
func TestProtocolTour(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_read", "read_file", `{"path":"note.txt"}`),
		textTurn("read"),
		toolTurn("call_write", "write_file", `{"path":"out.txt","content":"tour"}`),
		textTurn("wrote"),
		toolTurn("call_shell", "shell", `{"command":"echo tour-ok"}`),
		textTurn("shelled"),
		textTurn("resumed"),
	)
	h, dir := initHost(t, llm.baseURL())

	// initHost already completed initialize; re-assert the advertised set.
	init := resultOf(t, h.callOK(MethodInitialize, InitializeParams{
		Protocol: ProtocolVersion,
		CWD:      dir,
		Client:   ClientInfo{Name: "protocol-tour", Version: "test"},
	}))
	caps, _ := init["capabilities"].([]any)
	for _, want := range []string{"sessions", "approval", "mcp", "skills", "rules"} {
		if !capabilityHas(caps, want) {
			t.Fatalf("capabilities missing %q: %v", want, caps)
		}
	}

	// models.list hits an OpenAI-compatible /v1/models — not the chat stub.
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "stub-model", "owned_by": "tour"}},
		})
	}))
	t.Cleanup(catalog.Close)
	models := resultOf(t, h.callOK(MethodModelsList, ModelsParams{BaseURL: catalog.URL + "/v1"}))
	if list, _ := models["models"].([]any); len(list) != 1 {
		t.Fatalf("models.list=%v want one entry", models)
	}

	h.callOK(MethodSkillsList, SkillsParams{})
	h.callOK(MethodRulesList, RulesParams{LoadProject: true, LoadCursor: false})
	h.callOK(MethodPermissionsGet, nil)

	// MCP listing: empty project file, no child processes.
	mcpPath := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, mcpPath, `{"mcpServers":{}}`)
	mcp := resultOf(t, h.callOK(MethodMCPList, MCPListParams{SettingSources: []string{"project"}}))
	if servers, _ := mcp["servers"].([]any); len(servers) != 0 {
		t.Fatalf("empty mcp.json must list no servers, got %v", servers)
	}
	sources, _ := mcp["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources=%v want the project layer", sources)
	}
	source, _ := sources[0].(map[string]any)
	if source["source"] != "project" || source["exists"] != true {
		t.Fatalf("source=%v want present project file", source)
	}
	if path, _ := source["path"].(string); path != mcpPath {
		t.Fatalf("source path=%q want %q", path, mcpPath)
	}

	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello-tour"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		SettingSources: []string{"project"},
	}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)
	if sessionID == "" || agentID == "" {
		t.Fatalf("session.new incomplete: %v", sess)
	}

	assertFinished(t, promptRun(t, h, sessionID, "read the note", autoApprove))
	assertFinished(t, promptRun(t, h, sessionID, "write out.txt", autoApprove))
	if got, err := os.ReadFile(filepath.Join(dir, "out.txt")); err != nil || string(got) != "tour" {
		t.Fatalf("out.txt = %q (%v), want tour", got, err)
	}
	assertFinished(t, promptRun(t, h, sessionID, "echo something", autoApprove))

	listed := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	if sessions, _ := listed["sessions"].([]any); !sessionListed(sessions, agentID) {
		t.Fatalf("session.list missing %s: %v", agentID, listed)
	}

	renamed := resultOf(t, h.callOK(MethodSessionRename, SessionRenameParams{
		AgentID: agentID, Title: "protocol tour",
	}))
	if renamed["title"] != "protocol tour" {
		t.Fatalf("rename=%v", renamed)
	}

	transcript := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	if entries, _ := transcript["entries"].([]any); len(entries) == 0 {
		t.Fatalf("transcript empty: %v", transcript)
	}

	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	resumed := resultOf(t, h.callOK(MethodSessionResume, SessionParams{
		AgentID:        agentID,
		SettingSources: []string{"project"},
	}))
	resumedID, _ := resumed["session_id"].(string)
	if resumedID == "" || resumed["agent_id"] != agentID {
		t.Fatalf("resume=%v", resumed)
	}
	assertFinished(t, promptRun(t, h, resumedID, "still here?", autoApprove))

	h.callOK(MethodSessionDelete, SessionDeleteParams{AgentID: agentID})
	after := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	if sessions, _ := after["sessions"].([]any); sessionListed(sessions, agentID) {
		t.Fatalf("deleted agent still listed: %v", after)
	}
}

func assertFinished(t *testing.T, out runOutcome) {
	t.Helper()
	if out.done == nil {
		t.Fatal("run produced no done")
	}
	if status := paramsOf(t, out.done)["status"]; status != "finished" {
		t.Fatalf("run status=%v want finished: %v", status, paramsOf(t, out.done))
	}
}

func capabilityHas(caps []any, want string) bool {
	for _, c := range caps {
		if s, _ := c.(string); s == want {
			return true
		}
	}
	return false
}

func sessionListed(sessions []any, agentID string) bool {
	for _, raw := range sessions {
		m, _ := raw.(map[string]any)
		if m["agent_id"] == agentID {
			return true
		}
	}
	return false
}
