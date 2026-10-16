// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

// startSession opens a session with the given params and returns its id.
func startSession(t *testing.T, h *harness, p SessionParams) string {
	t.Helper()
	res := resultOf(t, h.callOK(MethodSessionNew, p))
	id, _ := res["session_id"].(string)
	if id == "" {
		t.Fatalf("session.new returned no id: %v", res)
	}
	return id
}

func promptRun(t *testing.T, h *harness, sessionID, message string, answer func(map[string]any) map[string]any) runOutcome {
	t.Helper()
	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: message})
	return h.collectRun(15*time.Second, answer)
}

// waitApproval reads notifications until the approval.request arrives. Events
// may precede it, so a bare next() would be flaky.
func waitApproval(t *testing.T, h *harness) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m := h.next(time.Until(deadline))
		if m["method"] == NotifyApprovalRequest {
			return paramsOf(t, m)
		}
	}
	t.Fatal("no approval.request arrived")
	return nil
}

func toolResultsOf(t *testing.T, events []map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range events {
		p := paramsOf(t, e)
		if p["type"] == "tool_result" {
			out = append(out, p)
		}
	}
	return out
}

// --- the gap this phase closes ----------------------------------------------

// A tool that used to run unattended now asks. run_tests and shell are the
// point: before 0.35 only write_file, edit_file, apply_patch, and shell were
// gated, and run_tests was not among them.
func TestGatedToolsAskBeforeRunning(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "run_tests", `{}`),
		toolTurn("call_2", "shell", `{"command":"echo hi"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{TestCommand: []string{"echo", "ok"}})

	var asked []string
	out := promptRun(t, h, sessionID, "run things", func(req map[string]any) map[string]any {
		tool, _ := req["tool"].(string)
		asked = append(asked, tool)
		return map[string]any{"decision": DecisionAllow}
	})

	if len(asked) != 2 || asked[0] != "run_tests" || asked[1] != "shell" {
		t.Fatalf("want run_tests then shell to ask, got %v", asked)
	}
	if status := paramsOf(t, out.done)["status"]; status != "finished" {
		t.Fatalf("run did not finish: %v", paramsOf(t, out.done))
	}
}

// Egress is gated at the SDK layer, not here: web_search and fetch_url are only
// registered when a search provider is configured, and the host configures none
// yet (see the p_0.35.3 notes). The gate itself is pinned by
// TestEgressIsGatedByTheWebGroup in the root package.

func TestApprovalRequestCarriesAToolPreview(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi\n"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	var preview map[string]any
	out := promptRun(t, h, sessionID, "write", func(req map[string]any) map[string]any {
		preview, _ = req["preview"].(map[string]any)
		// The other fields the client needs to render a prompt.
		for _, key := range []string{"request_id", "session_id", "run_id", "tool", "args", "decisions"} {
			if _, ok := req[key]; !ok {
				t.Fatalf("approval request is missing %q: %v", key, req)
			}
		}
		return map[string]any{"decision": DecisionAllow}
	})

	if len(out.approvals) != 1 {
		t.Fatalf("want one approval, got %d", len(out.approvals))
	}
	if preview["kind"] != anvil.PreviewDiff || preview["path"] != "hello.txt" {
		t.Fatalf("unexpected preview: %v", preview)
	}
	if diff, _ := preview["diff"].(string); !strings.Contains(diff, "+hi") {
		t.Fatalf("preview diff does not describe the write:\n%v", diff)
	}
}

// A diff preview carries both bodies, so the client can open a real diff
// editor instead of applying the unified diff itself. The thing an editor-side
// applier would get wrong is the thing the user is approving.
func TestApprovalPreviewCarriesBothBodies(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "edit_file", `{"path":"a.txt","old_string":"beta","new_string":"gamma"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := startSession(t, h, SessionParams{})

	var preview map[string]any
	promptRun(t, h, sessionID, "edit it", func(req map[string]any) map[string]any {
		preview, _ = req["preview"].(map[string]any)
		return map[string]any{"decision": DecisionDeny}
	})

	if preview["before"] != "alpha\nbeta\n" || preview["after"] != "alpha\ngamma\n" {
		t.Fatalf("unexpected bodies: %v", preview)
	}
	if _, ok := preview["truncated"]; ok {
		t.Fatalf("a two-line preview must not be marked truncated: %v", preview)
	}
}

// --- decisions --------------------------------------------------------------

func TestDenyReachesTheModelAsAToolError(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi"}`),
		textTurn("understood"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	out := promptRun(t, h, sessionID, "write", func(map[string]any) map[string]any {
		return map[string]any{"decision": DecisionDeny, "message": "not this file"}
	})

	if status := paramsOf(t, out.done)["status"]; status != "finished" {
		t.Fatalf("a deny must not kill the run: %v", paramsOf(t, out.done))
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 1 || results[0]["is_error"] != true {
		t.Fatalf("want one errored tool result, got %v", results)
	}
	if content, _ := results[0]["content"].(string); !strings.Contains(content, "not this file") {
		t.Fatalf("the deny reason must reach the model: %v", results[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("a denied write must not touch the workspace")
	}
}

func TestAllowSessionStopsAsking(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"one"}`),
		toolTurn("call_2", "write_file", `{"path":"b.txt","content":"two"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	asks := 0
	out := promptRun(t, h, sessionID, "write twice", func(map[string]any) map[string]any {
		asks++
		return map[string]any{"decision": DecisionAllowSession}
	})

	if asks != 1 {
		t.Fatalf("allow_session must suppress the second prompt, got %d asks", asks)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s was not written: %v", name, err)
		}
	}
	if len(out.approvals) != 1 {
		t.Fatalf("want one approval notification, got %d", len(out.approvals))
	}
}

func TestAllowSessionWithScopeOnlyCoversThatPath(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"one"}`),
		toolTurn("call_2", "write_file", `{"path":"b.txt","content":"two"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	var asked []string
	promptRun(t, h, sessionID, "write twice", func(req map[string]any) map[string]any {
		preview, _ := req["preview"].(map[string]any)
		path, _ := preview["path"].(string)
		asked = append(asked, path)
		// Echo the preview path back as the scope: only that file is remembered.
		return map[string]any{"decision": DecisionAllowSession, "scope": path}
	})

	if len(asked) != 2 {
		t.Fatalf("a scoped allow must not cover another path, asked %v", asked)
	}
}

func TestAllowSessionCanBeNarrowedToATool(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"one"}`),
		toolTurn("call_2", "shell", `{"command":"echo hi"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	var asked []string
	promptRun(t, h, sessionID, "do both", func(req map[string]any) map[string]any {
		tool, _ := req["tool"].(string)
		asked = append(asked, tool)
		return map[string]any{"decision": DecisionAllowSession}
	})

	if len(asked) != 2 || asked[0] != "write_file" || asked[1] != "shell" {
		t.Fatalf("allow_session for one tool must not cover another, asked %v", asked)
	}
}

func TestApprovalArgsRewriteIsHonoured(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"draft.txt","content":"rough"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	promptRun(t, h, sessionID, "write", func(map[string]any) map[string]any {
		return map[string]any{
			"decision": DecisionAllow,
			// RawMessage, so the harness marshals this as an object rather than
			// as a string holding JSON.
			"args": json.RawMessage(`{"path":"final.txt","content":"polished"}`),
		}
	})

	data, err := os.ReadFile(filepath.Join(dir, "final.txt"))
	if err != nil || string(data) != "polished" {
		t.Fatalf("rewritten args were not applied: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.txt")); !os.IsNotExist(err) {
		t.Fatal("the proposed path must not be written when args are rewritten")
	}
}

func TestApprovalResponseWithoutAPendingRequestIsRejected(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	m := h.call(MethodApprovalResponse, ApprovalResponseParams{
		RequestID: "appr-nothing",
		Decision:  DecisionAllow,
	})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeNoApproval {
		t.Fatalf("want CodeNoApproval, got %v", m)
	}
}

func TestUnknownDecisionIsRejected(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	m := h.call(MethodApprovalResponse, ApprovalResponseParams{
		RequestID: "appr-1",
		Decision:  "maybe",
	})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("want CodeInvalidParams for an unknown decision, got %v", m)
	}
	if strings.Contains(rpcErr.Message, "no pending") {
		t.Fatalf("a malformed answer must not be reported as a stale one: %s", rpcErr.Message)
	}
}

// --- nothing can hang forever ------------------------------------------------

func TestApprovalTimeoutDenies(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{ApprovalTimeoutMS: 100})

	// The policy never answers, so only the timeout can release the call.
	out := promptRun(t, h, sessionID, "write", nil)

	if status := paramsOf(t, out.done)["status"]; status != "finished" {
		t.Fatalf("a timed-out approval must not kill the run: %v", paramsOf(t, out.done))
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 1 || results[0]["is_error"] != true {
		t.Fatalf("want an errored tool result, got %v", results)
	}
	if content, _ := results[0]["content"].(string); !strings.Contains(content, "timed out") {
		t.Fatalf("the model should be told the approval timed out: %v", results[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("a timed-out approval must deny the call")
	}
}

func TestCancelReleasesAPendingApproval(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi"}`),
		textTurn("never reached"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{ApprovalTimeoutMS: -1}) // wait indefinitely

	ack := resultOf(t, h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "write"}))
	runID, _ := ack["run_id"].(string)

	// Wait for the approval, then cancel without answering it.
	req := waitApproval(t, h)
	if req["tool"] != "write_file" {
		t.Fatalf("expected a write_file approval, got %v", req)
	}
	h.callOK(MethodCancel, CancelParams{SessionID: sessionID, RunID: runID})

	out := h.collectRun(15*time.Second, nil)
	if status := paramsOf(t, out.done)["status"]; status != "cancelled" {
		t.Fatalf("done status=%v want cancelled", status)
	}
}

func TestSessionCloseReleasesAPendingApproval(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi"}`),
		textTurn("never reached"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{ApprovalTimeoutMS: -1})

	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "write"})
	if req := waitApproval(t, h); req["tool"] != "write_file" {
		t.Fatalf("expected a write_file approval, got %v", req)
	}
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	out := h.collectRun(15*time.Second, nil)
	if status := paramsOf(t, out.done)["status"]; status != "cancelled" {
		t.Fatalf("done status=%v want cancelled", status)
	}
}

func TestShutdownReleasesAPendingApproval(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi"}`),
		textTurn("never reached"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{ApprovalTimeoutMS: -1})

	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "write"})
	if req := waitApproval(t, h); req["tool"] != "write_file" {
		t.Fatalf("expected a write_file approval, got %v", req)
	}
	h.callOK(MethodShutdown, map[string]any{})

	out := h.collectRun(15*time.Second, nil)
	if out.done == nil {
		t.Fatal("shutdown must still let the run report its terminal state")
	}
}

// --- permission modes ---------------------------------------------------------

func TestPlanModeWithholdsWriteTools(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"hello.txt","content":"hi"}`),
		textTurn("here is the plan"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{PermissionMode: ModePlan})

	asks := 0
	out := promptRun(t, h, sessionID, "add a feature", func(map[string]any) map[string]any {
		asks++
		return map[string]any{"decision": DecisionAllow}
	})

	if asks != 0 {
		t.Fatalf("plan mode must not prompt for a tool it withdrew, got %d asks", asks)
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 1 || results[0]["is_error"] != true {
		t.Fatalf("want the withdrawn tool to fail, got %v", results)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("plan mode must not write")
	}
}

func TestPlanModePrefixesThePrompt(t *testing.T) {
	llm := newStubLLM(t, textTurn("plan"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{PermissionMode: ModePlan})
	promptRun(t, h, sessionID, "add a feature", autoApprove)

	body := llm.lastRequestBody(t)
	if !strings.Contains(body, "Plan mode is on.") || !strings.Contains(body, "add a feature") {
		t.Fatalf("plan directive did not reach the model:\n%s", body)
	}
}

func TestAcceptEditsSkipsTheWritePrompt(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"one"}`),
		toolTurn("call_2", "shell", `{"command":"echo hi"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{PermissionMode: ModeAcceptEdits})

	var asked []string
	promptRun(t, h, sessionID, "do both", func(req map[string]any) map[string]any {
		tool, _ := req["tool"].(string)
		asked = append(asked, tool)
		return map[string]any{"decision": DecisionAllow}
	})

	if len(asked) != 1 || asked[0] != "shell" {
		t.Fatalf("accept_edits must only ask about shell, asked %v", asked)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("the edit should have gone through: %v", err)
	}
}

// Bypass still answers every call itself — it just installs a callback that
// approves rather than no callback at all, so that a deny written in the
// workspace's policy file has something to hold against. See
// TestBypassStillHonoursADenyRule for the other half.
func TestBypassApprovesWithoutAsking(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"one"}`),
		toolTurn("call_2", "shell", `{"command":"echo hi"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{PermissionMode: ModeBypass})

	asks := 0
	promptRun(t, h, sessionID, "do both", func(map[string]any) map[string]any {
		asks++
		return map[string]any{"decision": DecisionAllow}
	})

	if asks != 0 {
		t.Fatalf("bypass must not prompt, got %d asks", asks)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("bypass must let the write through: %v", err)
	}
}

// --- the workspace's own policy file -----------------------------------------

// writePolicy drops a .anvil/permissions.json into a workspace that has already
// been initialized. Sessions read it when they are opened, so a policy written
// here applies to every session this test opens next.
func writePolicy(t *testing.T, dir, body string) {
	t.Helper()
	anvilDir := filepath.Join(dir, ".anvil")
	if err := os.MkdirAll(anvilDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(anvilDir, "permissions.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Bypass skips the asking. It does not skip the policy: a deny the user wrote
// down is the one thing a "run everything" mode may not quietly overrule.
func TestBypassStillHonoursADenyRule(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"rm -rf build"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writePolicy(t, dir, `{"version":1,"rules":[
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*","reason":"no destructive commands"}
	]}`)
	sessionID := startSession(t, h, SessionParams{PermissionMode: ModeBypass})

	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	asks := 0
	out := promptRun(t, h, sessionID, "clean up", func(map[string]any) map[string]any {
		asks++
		return map[string]any{"decision": DecisionAllow}
	})

	if asks != 0 {
		t.Fatalf("bypass must not prompt even when a rule denies, got %d asks", asks)
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 1 || results[0]["is_error"] != true {
		t.Fatalf("the deny must reach the model as a tool error, got %v", results)
	}
	content, _ := results[0]["content"].(string)
	for _, want := range []string{"permissions.json", "no destructive commands"} {
		if !strings.Contains(content, want) {
			t.Errorf("the refusal %q does not mention %q", content, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "build")); err != nil {
		t.Fatalf("a denied command must not have run: %v", err)
	}
}

// A workspace that commits a policy file gets it enforced without the client
// having to ask: an undefined load_permissions means on.
func TestWorkspacePolicyIsOnByDefault(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"secret.txt","content":"one"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writePolicy(t, dir, `{"version":1,"rules":[
		{"effect":"deny","tool":"write_file","pattern":"secret.txt"}
	]}`)
	sessionID := startSession(t, h, SessionParams{})

	asks := 0
	out := promptRun(t, h, sessionID, "write", func(map[string]any) map[string]any {
		asks++
		return map[string]any{"decision": DecisionAllow}
	})

	if asks != 0 {
		t.Fatalf("a deny must decide before the prompt, got %d asks", asks)
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 1 || results[0]["is_error"] != true {
		t.Fatalf("the rule should have refused the write, got %v", results)
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.txt")); err == nil {
		t.Fatal("a denied write must not reach the disk")
	}
}

// The switch exists so a client that manages policy elsewhere can turn the file
// off — and then the same file has no effect at all.
func TestLoadPermissionsFalseIgnoresTheFile(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"secret.txt","content":"one"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writePolicy(t, dir, `{"version":1,"rules":[
		{"effect":"deny","tool":"write_file","pattern":"secret.txt"}
	]}`)
	off := false
	sessionID := startSession(t, h, SessionParams{LoadPermissions: &off})

	out := promptRun(t, h, sessionID, "write", autoApprove)
	results := toolResultsOf(t, out.events)
	if len(results) != 1 || results[0]["is_error"] == true {
		t.Fatalf("with the file switched off the write should go through, got %v", results)
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.txt")); err != nil {
		t.Fatalf("the write should have reached the disk: %v", err)
	}
}

// The point of the whole feature, end to end: in one turn the model runs a
// read-only command and a destructive one, and only the second one is worth a
// prompt. `ls` is admitted by the analyzer through a `readonly` rule; nothing
// admits `rm -rf build`, so it still asks — and a deny leaves the directory.
func TestReadonlyRuleSilencesOnlyTheHarmlessCommand(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"ls"}`),
		toolTurn("call_2", "shell", `{"command":"rm -rf build"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writePolicy(t, dir, `{"version":1,"rules":[
		{"effect":"allow","tool":"shell","pattern":"readonly","reason":"read-only commands"}
	]}`)
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := startSession(t, h, SessionParams{})

	var asked []string
	out := promptRun(t, h, sessionID, "look then clean", func(req map[string]any) map[string]any {
		cmd, _ := req["args"].(map[string]any)
		asked = append(asked, fmt.Sprint(cmd["command"]))
		return map[string]any{"decision": DecisionDeny}
	})

	if len(asked) != 1 || asked[0] != "rm -rf build" {
		t.Fatalf("only the destructive command should have asked, asked %v", asked)
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 2 || results[0]["is_error"] == true {
		t.Fatalf("the allowed command must have run, got %v", results)
	}
	if _, err := os.Stat(filepath.Join(dir, "build")); err != nil {
		t.Fatalf("the denied command must not have run: %v", err)
	}
}

// A rule file that cannot be read fails the session instead of being skipped:
// an unreadable policy is not the same thing as no policy.
func TestMalformedPolicyFailsTheSession(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	writePolicy(t, dir, `{"version":1,"rules":[{"effect":"deny"}]}`)

	m := h.call(MethodSessionNew, SessionParams{})
	rpcErr, ok := rpcErrorOf(m)
	if !ok {
		t.Fatalf("session.new should fail on a malformed policy, got %v", m)
	}
	if !strings.Contains(rpcErr.Message, "permissions.json") {
		t.Fatalf("the error must name the file, got %q", rpcErr.Message)
	}
}

func TestSessionResultReportsTheMode(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	res := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	if res["permission_mode"] != ModeAsk {
		t.Fatalf("default mode should be ask, got %v", res["permission_mode"])
	}
	res = resultOf(t, h.callOK(MethodSessionNew, SessionParams{PermissionMode: ModePlan}))
	if res["permission_mode"] != ModePlan {
		t.Fatalf("mode=%v want plan", res["permission_mode"])
	}

	m := h.call(MethodSessionNew, SessionParams{PermissionMode: "yolo"})
	if rpcErr, ok := rpcErrorOf(m); !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("an unknown mode must be rejected, got %v", m)
	}
}

func TestCapabilitiesAdvertiseApproval(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	m := resultOf(t, h.callOK(MethodInitialize, InitializeParams{
		Protocol: ProtocolVersion, CWD: newWorkspace(t),
	}))
	caps, _ := m["capabilities"].([]any)
	var got []string
	for _, c := range caps {
		s, _ := c.(string)
		got = append(got, s)
	}
	if !contains(got, "approval") {
		t.Fatalf("capabilities must advertise approval, got %v", got)
	}
}

// --- group selection ----------------------------------------------------------

func TestExplicitPermissionGroupsOverrideTheDefault(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"one"}`),
		toolTurn("call_2", "shell", `{"command":"echo hi"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{PermissionGroups: []string{"shell"}})

	var asked []string
	promptRun(t, h, sessionID, "do both", func(req map[string]any) map[string]any {
		tool, _ := req["tool"].(string)
		asked = append(asked, tool)
		return map[string]any{"decision": DecisionAllow}
	})

	if len(asked) != 1 || asked[0] != "shell" {
		t.Fatalf("only the named group should ask, got %v", asked)
	}
}

func TestGroupsForMode(t *testing.T) {
	if got := groupsForMode(ModeBypass, nil); got != nil {
		t.Fatalf("bypass must gate nothing, got %v", got)
	}
	ask := groupsForMode(ModeAsk, nil)
	if len(ask) != len(DefaultPermissionGroups) {
		t.Fatalf("ask must use the default groups, got %v", ask)
	}
	edits := groupsForMode(ModeAcceptEdits, nil)
	if len(edits) != len(DefaultPermissionGroups)-2 {
		t.Fatalf("accept_edits must drop write and edit, got %v", edits)
	}
	for _, g := range edits {
		if g == "write" || g == "edit" {
			t.Fatalf("accept_edits still gates %q: %v", g, edits)
		}
	}
	named := groupsForMode(ModeAsk, []string{"shell"})
	if len(named) != 1 || named[0] != "shell" {
		t.Fatalf("an explicit list must win, got %v", named)
	}
}

func TestApprovalTimeoutResolution(t *testing.T) {
	if got := approvalTimeout(0); got != DefaultApprovalTimeout {
		t.Fatalf("zero should mean the default, got %v", got)
	}
	if got := approvalTimeout(-1); got != 0 {
		t.Fatalf("negative should mean no timeout, got %v", got)
	}
	if got := approvalTimeout(250); got != 250*time.Millisecond {
		t.Fatalf("got %v", got)
	}
}
