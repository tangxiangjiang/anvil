// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// spawnTurn is the parent's call that starts a child. The call id is fixed so a
// test can look for it on the child's events.
func spawnTurn(tools string) turn {
	return toolTurn("call_spawn", "spawn_agent", fmt.Sprintf(
		`{"prompt":"say hello","description":"Greeter","tools":%s}`, tools))
}

// eventParamsOf is paramsOf with a plain map, for loops that read many fields.
func eventParamsOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	return paramsOf(t, m)
}

// TestSubagentEventsReachTheWireWithTheirIdentity is the host half of the
// nested-card contract: a child's steps arrive on the same `event` stream,
// tagged with the child's agent id, the spawn call, and its depth.
func TestSubagentEventsReachTheWireWithTheirIdentity(t *testing.T) {
	llm := newStubLLM(t,
		spawnTurn(`["read"]`),
		textTurn("hello from the child"),
		textTurn("the child said hello"),
	)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)
	out := promptRun(t, h, sessionID, "delegate this", autoApprove)

	parentRunID, _ := paramsOf(t, out.done)["run_id"].(string)
	childAgentID, childEvents, parentEvents := "", 0, 0
	for _, e := range out.events {
		p := paramsOf(t, e)
		if p["parent_tool_call_id"] == "call_spawn" {
			childEvents++
			childAgentID, _ = p["agent_id"].(string)
			if childAgentID == "" || childAgentID == agentID {
				t.Fatalf("child event carries agent_id=%q; the parent is %q", childAgentID, agentID)
			}
			if depth, _ := p["depth"].(float64); int(depth) != 1 {
				t.Fatalf("child event depth=%v, want 1", p["depth"])
			}
			if runID, _ := p["run_id"].(string); runID == "" || runID == parentRunID {
				t.Fatalf("child event run_id=%q, want the child's own run (%q is the parent's)", runID, parentRunID)
			}
			continue
		}
		parentEvents++
		if got, _ := p["agent_id"].(string); got != agentID {
			t.Fatalf("parent event agent_id=%q, want %q", got, agentID)
		}
		if _, ok := p["parent_tool_call_id"]; ok {
			t.Fatalf("parent event carries parent_tool_call_id: %v", p)
		}
	}
	if childEvents == 0 {
		t.Fatalf("no child event reached the wire; agent id %q was never named", childAgentID)
	}
	if parentEvents == 0 {
		t.Fatalf("the parent's own events lost their agent identity: %v", eventTypes(out.events))
	}
	if childAgentID == agentID {
		t.Fatalf("the child was not a second agent")
	}
	// The child's message must have reached the wire as its own events, not only
	// as the spawn tool's result. The stub streams text in chunks, so the check
	// is on the concatenation of what the child's assistant events carried.
	var streamed strings.Builder
	for _, e := range out.events {
		p := paramsOf(t, e)
		if p["parent_tool_call_id"] == "call_spawn" && p["type"] == "assistant" {
			streamed.WriteString(fmt.Sprint(p["text"]))
		}
	}
	if !strings.Contains(streamed.String(), "hello from the child") {
		t.Fatalf("the child's answer never streamed through: %q", streamed.String())
	}
}

// TestSessionListNestsAChildUnderItsParent pins the listing half: a child is a
// session like any other, but it says whose it is and what it was called.
func TestSessionListNestsAChildUnderItsParent(t *testing.T) {
	llm := newStubLLM(t,
		spawnTurn(`["read"]`),
		textTurn("hello from the child"),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)
	promptRun(t, h, sessionID, "delegate this", autoApprove)
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	listed := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	rows, _ := listed["sessions"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want the parent and its child, got %v", listed)
	}
	var parent, child map[string]any
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["agent_id"] == agentID {
			parent = row
		} else {
			child = row
		}
	}
	if parent == nil || child == nil {
		t.Fatalf("the listing lost one of the two agents: %v", rows)
	}
	if sub, _ := parent["subagent"].(bool); sub {
		t.Fatalf("the session the person opened is marked a subagent: %v", parent)
	}
	if sub, _ := child["subagent"].(bool); !sub {
		t.Fatalf("the child is not marked a subagent: %v", child)
	}
	if got, _ := child["parent_agent_id"].(string); got != agentID {
		t.Fatalf("child parent_agent_id=%q, want %q", got, agentID)
	}
	if depth, _ := child["depth"].(float64); int(depth) != 1 {
		t.Fatalf("child depth=%v, want 1", child["depth"])
	}
	if got, _ := child["task_title"].(string); got != "Greeter" {
		t.Fatalf("child task_title=%q, want %q", got, "Greeter")
	}
}

// TestChildApprovalNamesTheChild is the accountability bit: the same caller,
// the same policy, but the card says which agent is asking.
func TestChildApprovalNamesTheChild(t *testing.T) {
	llm := newStubLLM(t,
		spawnTurn(`["write"]`),
		toolTurn("call_write", "write_file", `{"path":"child.txt","content":"from the child"}`),
		textTurn("wrote it"),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)

	var writeCards []map[string]any
	out := promptRun(t, h, sessionID, "delegate this", func(req map[string]any) map[string]any {
		if req["tool"] == "write_file" {
			writeCards = append(writeCards, req)
		}
		return map[string]any{"decision": DecisionAllow}
	})
	if len(out.approvals) < 2 {
		t.Fatalf("want a card for the spawn and one for the child's write, got %d", len(out.approvals))
	}
	if len(writeCards) == 0 {
		t.Fatalf("the child's write never asked")
	}
	card := writeCards[0]
	if got, _ := card["agent_id"].(string); got == "" || got == agentID {
		t.Fatalf("the child's card names agent_id=%q; the session's own agent is %q", got, agentID)
	}
	if depth, _ := card["depth"].(float64); int(depth) != 1 {
		t.Fatalf("the child's card depth=%v, want 1", card["depth"])
	}
}

// TestSubagentsCanBeTurnedOff: the parameter is real, not decorative. Without
// it the child tool does not exist, and the model is told so.
func TestSubagentsCanBeTurnedOff(t *testing.T) {
	llm := newStubLLM(t,
		spawnTurn(`["read"]`),
		textTurn("cannot delegate"),
	)
	h, _ := initHost(t, llm.baseURL())

	off := false
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{Subagents: &off}))
	sessionID, _ := sess["session_id"].(string)
	out := promptRun(t, h, sessionID, "delegate this", autoApprove)

	var result map[string]any
	for _, e := range out.events {
		p := paramsOf(t, e)
		if p["type"] == "tool_result" && p["tool_call_id"] == "call_spawn" {
			result = p
		}
	}
	if result == nil {
		t.Fatalf("the model's spawn call produced no result: %v", eventTypes(out.events))
	}
	if msg := fmt.Sprint(result["content"]); !strings.Contains(strings.ToLower(msg), "unknown tool") {
		t.Fatalf("spawn_agent ran in a session with subagents off: %q", msg)
	}
}

// longShellCommand is still running when a test cancels it.
func longShellCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 30 127.0.0.1 > nul"
	}
	return "sleep 30"
}

// listTasks reads the tree's task table through the host RPC.
func listTasks(t *testing.T, h *harness, sessionID string) []map[string]any {
	t.Helper()
	res := resultOf(t, h.callOK(MethodTasksList, TasksListParams{SessionID: sessionID}))
	raw, _ := res["tasks"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		if row, ok := v.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func waitTaskStatus(t *testing.T, h *harness, sessionID, taskID, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last []map[string]any
	for time.Now().Before(deadline) {
		last = listTasks(t, h, sessionID)
		for _, row := range last {
			if row["id"] == taskID && row["status"] == want {
				return row
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("task %s never became %q: %v", taskID, want, last)
	return nil
}

// taskIDFromText pulls the task id out of the tool result the model reads.
func taskIDFromText(t *testing.T, text string) string {
	t.Helper()
	m := regexp.MustCompile(`task-[A-Za-z0-9]+`).FindString(text)
	if m == "" {
		t.Fatalf("no task id in %q", text)
	}
	return m
}

func toolResultText(t *testing.T, out runOutcome, callID string) string {
	t.Helper()
	for _, e := range out.events {
		p := paramsOf(t, e)
		if p["type"] == "tool_result" && p["tool_call_id"] == callID {
			return fmt.Sprint(p["content"])
		}
	}
	t.Fatalf("no tool result for %s: %v", callID, eventTypes(out.events))
	return ""
}

// TestTasksRPCsOverABackgroundShell drives the three methods against a real
// session: a shell job that finishes, its output paged by offset, and a second
// one cancelled mid-flight.
func TestTasksRPCsOverABackgroundShell(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_bg", "shell", `{"command":"echo hello","run_in_background":true}`),
		textTurn("started"),
		toolTurn("call_long", "shell", fmt.Sprintf(`{"command":%q,"run_in_background":true}`, longShellCommand())),
		textTurn("started the long one"),
	)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)

	out := promptRun(t, h, sessionID, "start a job", autoApprove)
	firstID := taskIDFromText(t, toolResultText(t, out, "call_bg"))

	row := waitTaskStatus(t, h, sessionID, firstID, "finished")
	if row["kind"] != "shell" {
		t.Fatalf("kind=%v, want shell", row["kind"])
	}
	if exit, _ := row["exit_code"].(float64); exit != 0 {
		t.Fatalf("exit_code=%v, want 0", row["exit_code"])
	}
	if _, ok := row["ended_at"]; !ok {
		t.Fatalf("a finished task has no ended_at: %v", row)
	}
	if started, _ := row["started_at"].(string); started == "" {
		t.Fatalf("a task has no started_at: %v", row)
	}

	read := resultOf(t, h.callOK(MethodTasksOutput, TasksOutputParams{SessionID: sessionID, TaskID: firstID}))
	if got, _ := read["output"].(string); !strings.Contains(got, "hello") {
		t.Fatalf("tasks.output=%q, want the command's output", got)
	}
	if read["status"] != "finished" {
		t.Fatalf("status=%v, want finished", read["status"])
	}
	next, _ := read["next_offset"].(float64)
	if next <= 0 {
		t.Fatalf("next_offset=%v, want the end of what was read", read["next_offset"])
	}
	// Reading at the end is an empty chunk at the same offset, not an error.
	again := resultOf(t, h.callOK(MethodTasksOutput, TasksOutputParams{SessionID: sessionID, TaskID: firstID, Offset: int(next)}))
	if got, _ := again["output"].(string); got != "" {
		t.Fatalf("a read at the end returned %q", got)
	}

	// Start a job that will still be running, and stop it through the RPC.
	out = promptRun(t, h, sessionID, "start the long one", autoApprove)
	longID := taskIDFromText(t, toolResultText(t, out, "call_long"))
	cancelled := resultOf(t, h.callOK(MethodTasksCancel, TasksCancelParams{SessionID: sessionID, TaskID: longID}))
	task, _ := cancelled["task"].(map[string]any)
	if task["status"] != "cancelled" {
		t.Fatalf("tasks.cancel answered %v, want cancelled", task)
	}
	final := waitTaskStatus(t, h, sessionID, longID, "cancelled")
	if _, ok := final["ended_at"]; !ok {
		t.Fatalf("a cancelled task has no ended_at: %v", final)
	}
	// The reader is told why the bytes stop.
	read = resultOf(t, h.callOK(MethodTasksOutput, TasksOutputParams{SessionID: sessionID, TaskID: longID}))
	if got, _ := read["output"].(string); !strings.Contains(got, "command cancelled") {
		t.Fatalf("cancelled output=%q, want the cancellation note", got)
	}

	// The `task` notification reported both edges of both tasks.
	var starts, ends int
	for _, n := range out.tasks {
		p := paramsOf(t, n)
		row, _ := p["task"].(map[string]any)
		if row["status"] == "running" {
			starts++
		} else {
			ends++
		}
		if p["session_id"] != sessionID {
			t.Fatalf("task notification routed to %v, want %s", p["session_id"], sessionID)
		}
	}
	if starts == 0 || ends == 0 {
		t.Fatalf("task notifications: %d starts, %d ends; want both", starts, ends)
	}
}

// TestTaskRPCsRefuseWhatTheyDoNotOwn: a task id is scoped to the session tree
// that made it, and an unknown id is `no task`, not an empty row.
func TestTaskRPCsRefuseWhatTheyDoNotOwn(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)

	if rpcErr, ok := rpcErrorOf(h.call(MethodTasksList, TasksListParams{SessionID: "sess-nope"})); !ok || rpcErr.Code != CodeNoSession {
		t.Fatalf("tasks.list on an unknown session: %v", rpcErr)
	}
	if rpcErr, ok := rpcErrorOf(h.call(MethodTasksOutput, TasksOutputParams{SessionID: sessionID, TaskID: "task-nope"})); !ok || rpcErr.Code != CodeNoTask {
		t.Fatalf("tasks.output on an unknown task: %v", rpcErr)
	}
	if rpcErr, ok := rpcErrorOf(h.call(MethodTasksCancel, TasksCancelParams{SessionID: sessionID, TaskID: "task-nope"})); !ok || rpcErr.Code != CodeNoTask {
		t.Fatalf("tasks.cancel on an unknown task: %v", rpcErr)
	}

	// After the session closes, its table is gone with its agent: the RPC says
	// so rather than serving rows nobody owns any more.
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})
	if rpcErr, ok := rpcErrorOf(h.call(MethodTasksList, TasksListParams{SessionID: sessionID})); !ok || rpcErr.Code != CodeNoSession {
		t.Fatalf("tasks.list after session.close: %v", rpcErr)
	}
}

// TestSubagentTaskIsAnAgentRow: a background spawn is one row of the same
// table, its output is the child's final message, and cancelling it stops the
// child.
func TestSubagentTaskIsAnAgentRow(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_bg_spawn", "spawn_agent",
			`{"prompt":"say hello","description":"Greeter","tools":["read"],"background":true}`),
		textTurn("started it"),
	)
	// The child runs on its own goroutine, so its request races the parent's
	// next one. The subagent brief is what tells them apart.
	llm.setRoute(func(body string) (turn, bool) {
		if strings.Contains(body, "You are a subagent") {
			return textTurn("hello from the background child"), true
		}
		return turn{}, false
	})
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	out := promptRun(t, h, sessionID, "delegate this", autoApprove)
	taskID := taskIDFromText(t, toolResultText(t, out, "call_bg_spawn"))

	row := waitTaskStatus(t, h, sessionID, taskID, "finished")
	if row["kind"] != "agent" {
		t.Fatalf("kind=%v, want agent", row["kind"])
	}
	if got, _ := row["child_agent_id"].(string); got == "" {
		t.Fatalf("agent task has no concrete child identity: %v", row)
	}
	if got, _ := row["parent_tool_call_id"].(string); got != "call_bg_spawn" {
		t.Fatalf("parent_tool_call_id=%q, want call_bg_spawn", got)
	}
	parentRunID, _ := paramsOf(t, out.done)["run_id"].(string)
	if got, _ := row["parent_run_id"].(string); got != parentRunID {
		t.Fatalf("parent_run_id=%q, want %q", got, parentRunID)
	}
	read := resultOf(t, h.callOK(MethodTasksOutput, TasksOutputParams{SessionID: sessionID, TaskID: taskID}))
	if got, _ := read["output"].(string); !strings.Contains(got, "hello from the background child") {
		t.Fatalf("an agent task's output is its final message, got %q", got)
	}
	// Asked again at the end, an ended agent task is an empty chunk at the same
	// offset — its message is not a stream that repeats.
	next, _ := read["next_offset"].(float64)
	again := resultOf(t, h.callOK(MethodTasksOutput, TasksOutputParams{SessionID: sessionID, TaskID: taskID, Offset: int(next)}))
	if got, _ := again["output"].(string); got != "" {
		t.Fatalf("an agent task repeated its message: %q", got)
	}
}
