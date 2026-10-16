// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill lays down a minimal skill package under the workspace root.
func writeSkill(t *testing.T, root, id, body string) string {
	t.Helper()
	dir := filepath.Join(root, ".anvil", "skills", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A resumed session carries the earlier conversation into the next request.
// The assertion is on the *model's* request body, not on the store: the store
// holding records is not the property that matters — the model seeing them is.
// (The listing/resume round trip itself is pinned by
// TestResumeContinuesAPersistedSession; what is added here is the history, and
// the usage the listing reports.)
func TestResumeContinuesTheConversation(t *testing.T) {
	llm := newStubLLM(t,
		textTurn("noted."),
		textTurn("it was ZEBRA."),
	)
	h, _ := initHost(t, llm.baseURL())

	newResult := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := newResult["session_id"].(string)
	agentID, _ := newResult["agent_id"].(string)
	if sessionID == "" || agentID == "" {
		t.Fatalf("session.new returned no identity: %v", newResult)
	}
	promptRun(t, h, sessionID, "remember: the codeword is ZEBRA", autoApprove)
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	listed := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	sessions, _ := listed["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("want the one stored agent, got %v", listed)
	}
	entry, _ := sessions[0].(map[string]any)
	if runs, _ := entry["runs"].(float64); int(runs) != 1 {
		t.Fatalf("want one run recorded, got %v", entry["runs"])
	}
	usage, _ := entry["usage"].(map[string]any)
	if usage == nil {
		t.Fatalf("the listing carries no usage: %v", entry)
	}
	if total, _ := usage["total_tokens"].(float64); total <= 0 {
		t.Fatalf("want a non-zero cumulative usage, got %v", usage)
	}

	resumed := resultOf(t, h.callOK(MethodSessionResume, SessionParams{AgentID: agentID}))
	resumedID, _ := resumed["session_id"].(string)
	promptRun(t, h, resumedID, "what was the codeword?", autoApprove)

	// The second prompt does not mention ZEBRA, so finding it in the request
	// means the prior turn travelled with it.
	if body := llm.lastRequestBody(t); !strings.Contains(body, "ZEBRA") {
		t.Fatalf("the resumed turn did not carry the earlier conversation:\n%s", body)
	}
}

// Switching modes re-opens the same conversation under new options. Agent.Resume
// is what makes that possible: the agent id survives, the history is reloaded,
// and the toolset is rebuilt from the new options. (What plan mode withholds is
// pinned by TestPlanModeWithholdsWriteTools; this is the switch.)
func TestModeSwitchKeepsTheConversation(t *testing.T) {
	llm := newStubLLM(t,
		textTurn("I would edit main.go"),
		textTurn("edited."),
	)
	h, _ := initHost(t, llm.baseURL())

	newResult := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := newResult["session_id"].(string)
	agentID, _ := newResult["agent_id"].(string)
	promptRun(t, h, sessionID, "how would you change main.go?", autoApprove)
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	planned := resultOf(t, h.callOK(MethodSessionResume, SessionParams{
		AgentID:        agentID,
		PermissionMode: ModePlan,
	}))
	if planned["permission_mode"] != ModePlan {
		t.Fatalf("mode was not applied: %v", planned["permission_mode"])
	}
	plannedID, _ := planned["session_id"].(string)
	promptRun(t, h, plannedID, "go on", autoApprove)

	body := llm.lastRequestBody(t)
	if !strings.Contains(body, "how would you change main.go?") {
		t.Fatalf("re-opening the session dropped the conversation:\n%s", body)
	}
	if !strings.Contains(body, "Plan mode is on") {
		t.Fatalf("the new mode did not take effect:\n%s", body)
	}
}

func TestSkillsListFindsProjectSkills(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	writeSkill(t, dir, "commit-style", "---\nname: Commit style\ndescription: How commits are written here.\n---\n\nUse the imperative mood.\n")
	writeSkill(t, dir, "release-notes", "---\ndescription: Draft the release notes.\n---\n\nSummarize the diff.\n")
	// Marked always-on: it is injected without being selected.
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "skills.json"), []byte(`{"always":["commit-style"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res := resultOf(t, h.callOK(MethodSkillsList, SkillsParams{}))
	skills, _ := res["skills"].([]any)
	if len(skills) != 2 {
		t.Fatalf("want two skills, got %v", res)
	}
	first, _ := skills[0].(map[string]any)
	if first["id"] != "commit-style" {
		t.Fatalf("skills are not sorted by id: %v", skills)
	}
	if first["name"] != "Commit style" || first["description"] != "How commits are written here." {
		t.Fatalf("front matter was not read: %v", first)
	}
	if first["path"] != ".anvil/skills/commit-style/SKILL.md" {
		t.Fatalf("path is not workspace-relative: %v", first["path"])
	}
	if first["always"] != true {
		t.Fatalf("the always list was not applied: %v", first)
	}
	second, _ := skills[1].(map[string]any)
	if second["name"] != "release-notes" || second["always"] != nil {
		t.Fatalf("a nameless skill should fall back to its id: %v", second)
	}
}

func TestRulesListReportsWhatWasLoaded(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Use tabs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".anvil", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "rules", "style.md"), []byte("Prefer early returns.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cursor", "rules", "source.mdc"), []byte("Keep Go under source/.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The client reports the layers it put on the session, so this is the
	// session's list and not a generic one.
	res := resultOf(t, h.callOK(MethodRulesList, RulesParams{LoadProject: true, LoadCursor: false}))
	files, _ := res["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("want the project layer only, got %v", res)
	}
	first, _ := files[0].(map[string]any)
	if first["path"] != "AGENTS.md" || first["included"] != true {
		t.Fatalf("unexpected first entry: %v", first)
	}
	second, _ := files[1].(map[string]any)
	if second["path"] != ".anvil/rules/style.md" {
		t.Fatalf("unexpected second entry: %v", second)
	}
	if bytes, _ := second["bytes"].(float64); bytes <= 0 {
		t.Fatalf("no size reported: %v", second)
	}

	both := resultOf(t, h.callOK(MethodRulesList, RulesParams{LoadProject: true, LoadCursor: true}))
	files, _ = both["files"].([]any)
	if len(files) != 3 {
		t.Fatalf("the cursor layer was not reported: %v", both)
	}
	last, _ := files[2].(map[string]any)
	if last["path"] != ".cursor/rules/source.mdc" {
		t.Fatalf("unexpected cursor entry: %v", last)
	}
}

func TestRulesListIsEmptyWhenNoLayerIsAskedFor(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Use tabs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := resultOf(t, h.callOK(MethodRulesList, RulesParams{}))
	files, _ := res["files"].([]any)
	if len(files) != 0 {
		t.Fatalf("a layer that is off must not be reported: %v", res)
	}
}

// A closed session's conversation is still readable, because session.transcript
// is keyed by agent id rather than by session. That is what makes the reload
// case work: the window comes back, no session is open, and the panel still has
// something to fill the tab with.
//
// The assertion is on the wire shape the panel folds, so it covers the three
// things a replay needs and a listing could not give it: the user turn, the
// tool call with its arguments, and where the run ended.
func TestSessionTranscriptReplaysAPersistedConversation(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "read_file", `{"path":"note.txt"}`),
		textTurn("noted."),
	)
	h, dir := initHost(t, llm.baseURL())
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	newResult := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := newResult["session_id"].(string)
	agentID, _ := newResult["agent_id"].(string)
	if out := promptRun(t, h, sessionID, "read the note", autoApprove); out.done == nil {
		t.Fatalf("the run produced no done: %+v", out)
	}
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	res := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	entries, _ := res["entries"].([]any)
	// The user turn, the assistant turn that called the tool, the tool's result,
	// the assistant turn that ended the run, and the run's boundary.
	if len(entries) != 5 {
		t.Fatalf("want the whole conversation back, got %d: %v", len(entries), res)
	}
	if dropped, _ := res["dropped"].(float64); dropped != 0 {
		t.Fatalf("nothing was dropped, got %v", res["dropped"])
	}

	first, _ := entries[0].(map[string]any)
	if first["kind"] != "message" || first["role"] != "user" || first["content"] != "read the note" {
		t.Fatalf("first entry = %v", first)
	}
	call, _ := entries[1].(map[string]any)
	calls, _ := call["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("the assistant turn carries no tool call: %v", call)
	}
	// The arguments cross the pipe as JSON, not as a string: a replayed card
	// reads them exactly where a live one reads the event's.
	tc, _ := calls[0].(map[string]any)
	args, _ := tc["args"].(map[string]any)
	if tc["name"] != "read_file" || args["path"] != "note.txt" {
		t.Fatalf("tool call = %v", tc)
	}
	result, _ := entries[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "call_1" || result["content"] == "" {
		t.Fatalf("tool result = %v", result)
	}
	last, _ := entries[3].(map[string]any)
	if last["role"] != "assistant" || last["content"] != "noted." {
		t.Fatalf("the closing turn = %v", last)
	}
	end, _ := entries[4].(map[string]any)
	if end["kind"] != "run_end" || end["status"] != "finished" {
		t.Fatalf("run boundary = %v", end)
	}
}

// An agent the workspace does not have is a host error, not an empty
// conversation: the panel tells "this session is gone" from "this session had
// nothing in it", and only the first one clears the remembered id.
func TestSessionTranscriptRejectsAnUnknownAgent(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	m := h.call(MethodSessionTranscript, SessionTranscriptParams{AgentID: "agent-nope"})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeHostError {
		t.Fatalf("want a host error, got %v", m)
	}
	if !strings.Contains(rpcErr.Message, "not found") {
		t.Fatalf("want a not-found message, got %q", rpcErr.Message)
	}

	empty := h.call(MethodSessionTranscript, SessionTranscriptParams{})
	if _, ok := rpcErrorOf(empty); !ok {
		t.Fatalf("an empty agent id must be refused, got %v", empty)
	}
}
