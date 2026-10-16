// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

// initHost starts a harness and completes the initialize handshake.
func initHost(t *testing.T, baseURL string) (*harness, string) {
	t.Helper()
	dir := newWorkspace(t)
	h := startHarness(t, Config{
		Provider: string(anvil.ProviderOpenAI),
		BaseURL:  baseURL,
		Model:    "stub-model",
	})
	res := resultOf(t, h.callOK(MethodInitialize, InitializeParams{
		Protocol: ProtocolVersion,
		CWD:      dir,
		Client:   ClientInfo{Name: "vscode", Version: "test"},
	}))
	if got := res["cwd"]; got != dir {
		t.Fatalf("cwd=%v want %v", got, dir)
	}
	if got, _ := res["protocol"].(float64); int(got) != ProtocolVersion {
		t.Fatalf("protocol=%v want %d", res["protocol"], ProtocolVersion)
	}
	return h, dir
}

// TestPromptStreamsEventsToDone is the phase's core end-to-end case: create a
// session, run one turn that calls a tool, and observe the documented event
// stream terminated by a single done.
func TestPromptStreamsEventsToDone(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi"}`),
		textTurn("All done"),
	)
	h, dir := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)
	if sessionID == "" || agentID == "" {
		t.Fatalf("session.new result incomplete: %v", sess)
	}

	ack := resultOf(t, h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "write a file"}))
	runID, _ := ack["run_id"].(string)
	if runID == "" {
		t.Fatalf("prompt returned no run id: %v", ack)
	}

	out := h.collectRunOK(15 * time.Second)
	events, done := out.events, out.done

	types := eventTypes(events)
	for _, want := range []string{"progress", "tool_call", "tool_result", "assistant"} {
		if !contains(types, want) {
			t.Fatalf("missing %q in event stream %v", want, types)
		}
	}
	// The SDK's own done event is deliberately not forwarded; only the
	// RunResult-derived notification is.
	if contains(types, "done") {
		t.Fatalf("SDK done leaked into the event stream: %v", types)
	}
	for _, e := range events {
		p := paramsOf(t, e)
		if p["session_id"] != sessionID {
			t.Fatalf("event has wrong session id: %v", p)
		}
		if p["run_id"] != runID {
			t.Fatalf("event has wrong run id: %v", p)
		}
	}

	var toolName string
	for _, e := range events {
		p := paramsOf(t, e)
		if p["type"] == "tool_call" {
			toolName, _ = p["name"].(string)
		}
	}
	if toolName != "write_file" {
		t.Fatalf("tool_call name=%q want write_file", toolName)
	}

	dp := paramsOf(t, done)
	if dp["status"] != "finished" {
		t.Fatalf("done status=%v want finished (%v)", dp["status"], dp)
	}
	if dp["result"] != "All done" {
		t.Fatalf("done result=%v want All done", dp["result"])
	}
	if got, _ := dp["steps"].(float64); int(got) != 2 {
		t.Fatalf("done steps=%v want 2", dp["steps"])
	}
	usage, _ := dp["usage"].(map[string]any)
	if usage == nil || usage["total_tokens"] != float64(16) {
		t.Fatalf("done usage=%v want total_tokens 16", dp["usage"])
	}

	if got, err := os.ReadFile(filepath.Join(dir, "hello.txt")); err != nil || string(got) != "hi" {
		t.Fatalf("tool did not write the file: %q %v", got, err)
	}
}

// TestPromptRejectsConcurrentRun enforces the one-run-per-session rule.
func TestPromptRejectsConcurrentRun(t *testing.T) {
	llm := newStubLLM(t)
	llm.blockUntilCancel = true
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)

	first := resultOf(t, h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "one"}))
	runID, _ := first["run_id"].(string)

	second := h.call(MethodPrompt, PromptParams{SessionID: sessionID, Message: "two"})
	rpcErr, ok := rpcErrorOf(second)
	if !ok {
		t.Fatalf("second prompt was accepted: %v", second)
	}
	if rpcErr.Code != CodeRunInProgress {
		t.Fatalf("code=%d want %d (%s)", rpcErr.Code, CodeRunInProgress, rpcErr.Message)
	}
	if !strings.Contains(rpcErr.Message, runID) {
		t.Fatalf("error should name the active run %s: %s", runID, rpcErr.Message)
	}
}

// TestCancelEndsRunWithCancelledStatus covers the cancel round trip.
func TestCancelEndsRunWithCancelledStatus(t *testing.T) {
	llm := newStubLLM(t)
	llm.blockUntilCancel = true
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	ack := resultOf(t, h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "hang"}))
	runID, _ := ack["run_id"].(string)

	h.callOK(MethodCancel, CancelParams{SessionID: sessionID, RunID: runID})

	done := h.collectRunOK(15 * time.Second).done
	dp := paramsOf(t, done)
	if dp["status"] != "cancelled" {
		t.Fatalf("done status=%v want cancelled (%v)", dp["status"], dp)
	}
	if dp["code"] != string(anvil.CodeCancelled) {
		t.Fatalf("done code=%v want %s", dp["code"], anvil.CodeCancelled)
	}
}

// TestCancelWithNoActiveRunIsAnError keeps clients honest about run ids.
func TestCancelWithNoActiveRunIsAnError(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)

	m := h.call(MethodCancel, CancelParams{SessionID: sessionID})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeNoRun {
		t.Fatalf("want CodeNoRun, got %v", m)
	}
}

// TestResumeContinuesAPersistedSession covers session.list plus resume.
func TestResumeContinuesAPersistedSession(t *testing.T) {
	llm := newStubLLM(t, textTurn("first"), textTurn("second"))
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)

	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "hello"})
	done := h.collectRunOK(15 * time.Second).done
	if paramsOf(t, done)["status"] != "finished" {
		t.Fatalf("first run did not finish: %v", done)
	}

	listing := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	sessions, _ := listing["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("session.list returned %d rows want 1: %v", len(sessions), listing)
	}
	row, _ := sessions[0].(map[string]any)
	if row["agent_id"] != agentID {
		t.Fatalf("listed agent_id=%v want %s", row["agent_id"], agentID)
	}
	if row["last_status"] != "finished" {
		t.Fatalf("listed last_status=%v want finished", row["last_status"])
	}

	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	resumed := resultOf(t, h.callOK(MethodSessionResume, SessionParams{AgentID: agentID}))
	resumedSession, _ := resumed["session_id"].(string)
	if resumedSession == "" || resumedSession == sessionID {
		t.Fatalf("resume returned a bad session id: %v", resumed)
	}
	if resumed["agent_id"] != agentID {
		t.Fatalf("resume changed the agent id: %v", resumed)
	}

	h.callOK(MethodPrompt, PromptParams{SessionID: resumedSession, Message: "again"})
	done2 := h.collectRunOK(15 * time.Second).done
	dp := paramsOf(t, done2)
	if dp["status"] != "finished" || dp["result"] != "second" {
		t.Fatalf("resumed run did not produce the second scripted answer: %v", dp)
	}
	if llm.callCount() != 2 {
		t.Fatalf("stub saw %d calls want 2", llm.callCount())
	}
}

// TestInitializeMustComeFirst gates every other method.
func TestInitializeMustComeFirst(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h := startHarness(t, Config{
		Provider: string(anvil.ProviderOpenAI),
		BaseURL:  llm.baseURL(),
		Model:    "stub-model",
	})
	m := h.call(MethodSessionNew, SessionParams{})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidRequest {
		t.Fatalf("want CodeInvalidRequest before initialize, got %v", m)
	}
}

// TestProtocolMismatchIsRejected pins the versioning rule.
func TestProtocolMismatchIsRejected(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h := startHarness(t, Config{BaseURL: llm.baseURL(), Model: "stub-model"})
	m := h.call(MethodInitialize, InitializeParams{Protocol: ProtocolVersion + 1, CWD: newWorkspace(t)})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("want CodeInvalidParams for a future protocol, got %v", m)
	}
}

// TestUnknownMethodAndParseError covers the JSON-RPC error surface.
func TestUnknownMethodAndParseError(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	m := h.call("session.explode", map[string]any{})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("want CodeMethodNotFound, got %v", m)
	}

	if _, err := h.in.Write([]byte("{not json\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	m = h.next(5 * time.Second)
	if m["id"] != nil {
		t.Fatalf("parse error must carry a null id: %v", m)
	}
	rpcErr, ok = rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeParse {
		t.Fatalf("want CodeParse, got %v", m)
	}
}

// TestSessionErrors covers the session-not-found paths.
func TestSessionErrors(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	for _, method := range []string{MethodPrompt, MethodSessionClose, MethodCancel} {
		params := map[string]any{"session_id": "sess-nope"}
		if method == MethodPrompt {
			params["message"] = "hi"
		}
		m := h.call(method, params)
		rpcErr, ok := rpcErrorOf(m)
		if !ok || rpcErr.Code != CodeNoSession {
			t.Fatalf("%s: want CodeNoSession, got %v", method, m)
		}
	}

	m := h.call(MethodPrompt, map[string]any{"session_id": "sess-nope", "message": "  "})
	if rpcErr, ok := rpcErrorOf(m); !ok || rpcErr.Code != CodeNoSession {
		t.Fatalf("prompt with blank message: want CodeNoSession first, got %v", m)
	}
}

// TestSessionNewRequiresAModel keeps a misconfigured client from failing deeper.
func TestSessionNewRequiresAModel(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	dir := newWorkspace(t)
	h := startHarness(t, Config{Provider: string(anvil.ProviderOpenAI), BaseURL: llm.baseURL()})
	h.callOK(MethodInitialize, InitializeParams{Protocol: ProtocolVersion, CWD: dir})

	m := h.call(MethodSessionNew, SessionParams{})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("want CodeInvalidParams when no model resolves, got %v", m)
	}
}

// TestUnknownSettingSourceIsRejected guards the enum.
func TestUnknownSettingSourceIsRejected(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	m := h.call(MethodSessionNew, SessionParams{SettingSources: []string{"galaxy"}})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("want CodeInvalidParams, got %v", m)
	}
}

// TestShutdownEndsServe checks the clean-close path.
func TestShutdownEndsServe(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	h.callOK(MethodShutdown, map[string]any{})
	// The reply is queued before Serve tears the sessions down, so poll for the
	// teardown instead of assuming it already happened.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.srv.session(sessionID); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s survived shutdown", sessionID)
}

// TestProjectEventDropsSDKDone documents the one deliberate projection gap.
func TestProjectEventDropsSDKDone(t *testing.T) {
	if _, ok := projectEvent("s", "r", anvil.Event{Type: anvil.EventDone, Text: "final"}); ok {
		t.Fatal("anvil.EventDone must not be forwarded as an event")
	}
	got, ok := projectEvent("s", "r", anvil.Event{Type: anvil.EventToolCall, Name: "shell", Step: 2})
	if !ok || got.Type != "tool_call" || got.Name != "shell" || got.Step != 2 {
		t.Fatalf("tool_call projection lost fields: %+v", got)
	}
}

// TestProjectDoneFallsBackToRunID covers a Wait that returns no result.
func TestProjectDoneFallsBackToRunID(t *testing.T) {
	got := projectDone("s", "run-1", nil, errShutdown)
	if got.RunID != "run-1" || got.Status != string(anvil.StatusError) || got.Err == "" {
		t.Fatalf("unexpected done projection: %+v", got)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
