// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

// completerFunc scripts one model in tests without an HTTP server.
type completerFunc func(context.Context, llm.Request) (*llm.Response, error)

func (f completerFunc) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return f(ctx, req)
}

func subagentTestOptions(dir string) Options {
	return Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
		Subagents: SubagentOptions{
			Enabled: true,
		},
	}
}

func lastUserText(req llm.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == llm.RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

// trailingToolResults counts tool messages that follow the newest user message,
// which is how a script tells this run's turns apart from earlier ones.
func trailingToolResults(req llm.Request) []llm.Message {
	var out []llm.Message
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role == llm.RoleUser {
			break
		}
		if m.Role == llm.RoleTool {
			out = append(out, m)
		}
	}
	return out
}

func textResponse(s string) *llm.Response {
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: s}}
}

func callResponse(id, name, args string) *llm.Response {
	return &llm.Response{Message: llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: id, Name: name, Args: json.RawMessage(args)}},
	}}
}

func findToolResult(events []Event, name string) *Event {
	for i := range events {
		if events[i].Type == EventToolResult && events[i].Name == name {
			return &events[i]
		}
	}
	return nil
}

func collectEvents(t *testing.T, run *Run) []Event {
	t.Helper()
	var events []Event
	for ev := range run.Stream(context.Background()) {
		events = append(events, ev)
	}
	return events
}

// TestSubagentForeground checks the whole foreground path: the model calls
// spawn_agent, the child answers, the answer becomes the tool result, and the
// child is listed as a descendant of the parent with its identity intact.
func TestSubagentForeground(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var childEvents []SubagentEvent
	opts := subagentTestOptions(dir)
	opts.Subagents.OnEvent = func(ev SubagentEvent) {
		mu.Lock()
		childEvents = append(childEvents, ev)
		mu.Unlock()
	}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if lastUserText(req) == "say hello" {
			return textResponse("child answer"), nil
		}
		if len(trailingToolResults(req)) > 0 {
			return textResponse("parent done"), nil
		}
		return callResponse("call-1", "spawn_agent", `{"prompt":"say hello","description":"Greeter"}`), nil
	})

	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "parent done" {
		t.Fatalf("status=%s result=%q err=%s", res.Status, res.Result, res.Err)
	}

	tr := findToolResult(events, "spawn_agent")
	if tr == nil {
		t.Fatal("no spawn_agent tool result")
	}
	if tr.IsError {
		t.Fatalf("spawn failed: %s", tr.Content)
	}
	if !strings.Contains(tr.Content, "finished in 1 steps.") || !strings.Contains(tr.Content, "child answer") {
		t.Fatalf("tool result = %q", tr.Content)
	}

	mu.Lock()
	got := append([]SubagentEvent(nil), childEvents...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("OnEvent received nothing")
	}
	var childID, childRunID, sawAnswer string
	for _, ev := range got {
		if ev.ParentAgentID != parent.id {
			t.Fatalf("ParentAgentID = %q, want %q", ev.ParentAgentID, parent.id)
		}
		if ev.ParentRunID != run.ID() {
			t.Fatalf("ParentRunID = %q, want %q", ev.ParentRunID, run.ID())
		}
		if ev.ToolCallID != "call-1" {
			t.Fatalf("ToolCallID = %q", ev.ToolCallID)
		}
		if ev.AgentID == "" || ev.AgentID == parent.id {
			t.Fatalf("AgentID = %q", ev.AgentID)
		}
		if ev.RunID == "" {
			t.Fatal("child RunID is empty")
		}
		childID = ev.AgentID
		childRunID = ev.RunID
		if ev.Event.Type == EventAssistant && ev.Event.Text == "child answer" {
			sawAnswer = ev.AgentID
		}
	}
	if sawAnswer != childID {
		t.Fatalf("no child assistant event for %q", childID)
	}
	if childRunID == "" {
		t.Fatal("child run id missing")
	}

	// The listing nests the child: identity came from the meta record.
	infos, err := List(context.Background(), ListOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("agents = %d, want 2", len(infos))
	}
	var child *AgentInfo
	for i := range infos {
		if infos[i].ID == childID {
			child = &infos[i]
		}
	}
	if child == nil {
		t.Fatalf("child %q not listed", childID)
	}
	if child.ParentAgentID != parent.id || child.ParentRunID != run.ID() ||
		child.ParentToolCallID != "call-1" || child.Depth != 1 ||
		child.Kind != "subagent" || child.TaskTitle != "Greeter" {
		t.Fatalf("child identity = %+v", child)
	}

	// Deleting the session takes the subagent's file with it.
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteAgent(context.Background(), parent.id, ListOptions{Local: LocalOptions{CWD: dir}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(context.Background(), childID, ListOptions{Local: LocalOptions{CWD: dir}}); err == nil {
		t.Fatal("child survived DeleteAgent")
	}
	if _, err := Get(context.Background(), parent.id, ListOptions{Local: LocalOptions{CWD: dir}}); err == nil {
		t.Fatal("parent survived DeleteAgent")
	}
}

// TestSubagentDepthLimit checks that the tool is absent, not merely guarded:
// a child at the last depth has no spawn_agent in its registry.
func TestSubagentDepthLimit(t *testing.T) {
	dir := t.TempDir()
	opts := subagentTestOptions(dir)
	opts.Subagents.MaxDepth = 1
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if hasTool(a, "spawn_agent") {
		t.Fatal("depth 0 with MaxDepth 1 has spawn_agent")
	}
	res := a.registry.Execute(context.Background(), "spawn_agent", json.RawMessage(`{}`))
	if !res.IsError || !strings.Contains(res.Content, "unknown tool") {
		t.Fatalf("direct call = %+v", res)
	}

	// A child at depth 1 under MaxDepth 2 is at the last depth as well.
	opts.Subagents.MaxDepth = 2
	child, err := createAgent(context.Background(), opts, subagentIdentity{
		ParentAgentID: "agent-parent",
		Depth:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if hasTool(child, "spawn_agent") {
		t.Fatal("depth 1 with MaxDepth 2 has spawn_agent")
	}
}

func hasTool(a *Agent, name string) bool {
	for _, d := range a.registry.Defs() {
		if d.Name == name {
			return true
		}
	}
	return false
}

// TestSubagentBudget checks the tree-wide counter and its per-run reset.
func TestSubagentBudget(t *testing.T) {
	b := newSubagentBudget(2)
	if err := b.take(); err != nil {
		t.Fatal(err)
	}
	if err := b.take(); err != nil {
		t.Fatal(err)
	}
	err := b.take()
	if err == nil || !strings.Contains(err.Error(), "MaxAgents") {
		t.Fatalf("third take = %v", err)
	}
	b.reset()
	if err := b.take(); err != nil {
		t.Fatalf("after reset: %v", err)
	}

	dir := t.TempDir()
	opts := subagentTestOptions(dir)
	opts.Subagents.MaxAgents = 1
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch lastUserText(req) {
		case "child one", "child two":
			return textResponse("done"), nil
		}
		// The tool results trailing the newest user message are this run's.
		switch len(trailingToolResults(req)) {
		case 0:
			return callResponse("call-1", "spawn_agent", `{"prompt":"child one","description":"One"}`), nil
		case 1:
			return callResponse("call-2", "spawn_agent", `{"prompt":"child two","description":"Two"}`), nil
		default:
			return textResponse("ok"), nil
		}
	})

	run, err := parent.Send(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	var results []Event
	for _, ev := range events {
		if ev.Type == EventToolResult && ev.Name == "spawn_agent" {
			results = append(results, ev)
		}
	}
	if len(results) != 2 {
		t.Fatalf("spawn results = %d, want 2", len(results))
	}
	if results[0].IsError {
		t.Fatalf("first spawn failed: %s", results[0].Content)
	}
	if !results[1].IsError || !strings.Contains(results[1].Content, "MaxAgents") {
		t.Fatalf("second spawn = %+v", results[1])
	}

	// A new root run gets a new tree budget.
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if lastUserText(req) == "child one" {
			return textResponse("done"), nil
		}
		if len(trailingToolResults(req)) == 0 {
			return callResponse("call-3", "spawn_agent", `{"prompt":"child one","description":"One"}`), nil
		}
		return textResponse("ok"), nil
	})
	run2, err := parent.Send(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	events2 := collectEvents(t, run2)
	if _, err := run2.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := findToolResult(events2, "spawn_agent")
	if tr == nil || tr.IsError {
		t.Fatalf("spawn after reset = %+v", tr)
	}
}

// TestSubagentPermissionIdentity checks that a child's gated call is decided by
// the inherited callback and names the child, and that a denial is a tool error
// without ending the parent's run.
func TestSubagentPermissionIdentity(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var requests []PermissionRequest
	var childMu sync.Mutex
	var childID string
	opts := subagentTestOptions(dir)
	opts.Permission = func(_ context.Context, req PermissionRequest) (PermissionDecision, error) {
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		return PermissionDecision{Allow: false, Message: "not today"}, nil
	}
	opts.Subagents.OnEvent = func(ev SubagentEvent) {
		childMu.Lock()
		childID = ev.AgentID
		childMu.Unlock()
	}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch lastUserText(req) {
		case "write x":
			if len(trailingToolResults(req)) == 0 {
				return callResponse("call-w", "write_file", `{"path":"x.txt","content":"hi"}`), nil
			}
			return textResponse("could not write"), nil
		}
		if len(trailingToolResults(req)) == 0 {
			return callResponse("call-1", "spawn_agent", `{"prompt":"write x","description":"Writer","tools":["write"]}`), nil
		}
		return textResponse("parent ok"), nil
	})

	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "parent ok" {
		t.Fatalf("parent run = %+v", res)
	}
	tr := findToolResult(events, "spawn_agent")
	if tr == nil || tr.IsError {
		t.Fatalf("spawn = %+v", tr)
	}
	if !strings.Contains(tr.Content, "could not write") {
		t.Fatalf("spawn content = %q", tr.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("permission requests = %d, want 1", len(requests))
	}
	got := requests[0]
	childMu.Lock()
	wantAgent := childID
	childMu.Unlock()
	if got.ToolName != "write_file" || got.AgentID != wantAgent || got.Depth != 1 {
		t.Fatalf("request identity = %+v, child = %q", got, wantAgent)
	}
	if got.RunID == "" || got.RunID == run.ID() {
		t.Fatalf("RunID = %q, parent run = %q", got.RunID, run.ID())
	}
}

// TestSubagentModelError checks that a failing child becomes an error tool
// result while the parent's run finishes normally.
func TestSubagentModelError(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if lastUserText(req) == "explode" {
			return nil, errors.New("boom")
		}
		if len(trailingToolResults(req)) == 0 {
			return callResponse("call-1", "spawn_agent", `{"prompt":"explode","description":"Boom"}`), nil
		}
		return textResponse("parent recovered"), nil
	})

	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "parent recovered" {
		t.Fatalf("parent run = %+v", res)
	}
	tr := findToolResult(events, "spawn_agent")
	if tr == nil || !tr.IsError || !strings.Contains(tr.Content, "boom") {
		t.Fatalf("spawn result = %+v", tr)
	}
}

// TestSubagentToolSelector checks that a selector naming nothing is an error the
// model can act on, rather than a child silently narrower than asked for.
func TestSubagentToolSelector(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if len(trailingToolResults(req)) == 0 {
			return callResponse("call-1", "spawn_agent", `{"prompt":"look","description":"Look","tools":["nope"]}`), nil
		}
		return textResponse("ok"), nil
	})
	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := findToolResult(events, "spawn_agent")
	if tr == nil || !tr.IsError {
		t.Fatalf("spawn result = %+v", tr)
	}
	if !strings.Contains(tr.Content, "groups: agent") {
		t.Fatalf("error does not list groups: %q", tr.Content)
	}
}

// TestSubagentMaxStepsClamp checks that a child's step ceiling can only narrow
// the parent's, and that the caller is told when it was narrowed.
func TestSubagentMaxStepsClamp(t *testing.T) {
	dir := t.TempDir()
	opts := subagentTestOptions(dir)
	opts.MaxSteps = 3
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if lastUserText(req) == "look" {
			return textResponse("found it"), nil
		}
		if len(trailingToolResults(req)) == 0 {
			return callResponse("call-1", "spawn_agent", `{"prompt":"look","description":"Look","max_steps":99}`), nil
		}
		return textResponse("ok"), nil
	})
	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := findToolResult(events, "spawn_agent")
	if tr == nil || tr.IsError {
		t.Fatalf("spawn result = %+v", tr)
	}
	if !strings.Contains(tr.Content, "clamped to this session's 3") {
		t.Fatalf("no clamp note: %q", tr.Content)
	}
}

// TestSubagentsDisabledByDefault checks the opt-in: an ordinary session neither
// registers the tool nor sees any other change.
func TestSubagentsDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if hasTool(a, "spawn_agent") {
		t.Fatal("spawn_agent registered without Subagents.Enabled")
	}
}
