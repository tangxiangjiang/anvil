// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
)

// runSpec sends one parent turn that calls run_workflow with spec and returns the tool
// result the parent saw. script answers every child.
func runSpec(t *testing.T, parent *Agent, spec string, script func(req llm.Request) *llm.Response) *Event {
	t.Helper()
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if lastUserText(req) == "parent task" {
			if len(trailingToolResults(req)) == 0 {
				return callResponse("call-1", "run_workflow", spec), nil
			}
			return textResponse("parent done"), nil
		}
		return script(req), nil
	})
	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := findToolResult(events, "run_workflow")
	if tr == nil {
		t.Fatal("no run_workflow result")
	}
	return tr
}

func TestWorkflowStepFields(t *testing.T) {
	// The new fields are validated like every other.
	cases := map[string]string{
		"unknown role":   `{"name":"w","steps":[{"id":"a","prompt":"p","role":"reviewer"}]}`,
		"unknown effort": `{"name":"w","steps":[{"id":"a","prompt":"p","effort":"extreme"}]}`,
		"unknown schema": `{"name":"w","steps":[{"id":"a","prompt":"p","result_schema":"plan"}]}`,
		"brief_from_item without for_each": `{"name":"w","steps":[{"id":"a","prompt":"p","brief_from_item":true}]}`,
	}
	for name, raw := range cases {
		if err := validateWorkflow(decodeForTest(t, raw)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	ok := `{"name":"w","steps":[{"id":"a","prompt":"p","role":"decision","effort":"high","result_schema":"decision"}]}`
	if err := validateWorkflow(decodeForTest(t, ok)); err != nil {
		t.Fatalf("valid fields refused: %v", err)
	}

	// They reach the child: its role is in its prompt, its effort in its request.
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	var mu sync.Mutex
	var system string
	var effort llm.Effort
	runSpec(t, parent, `{"name":"w","steps":[{"id":"a","prompt":"think","role":"decision","effort":"high"}]}`,
		func(req llm.Request) *llm.Response {
			mu.Lock()
			system, effort = systemOf(req), req.Effort
			mu.Unlock()
			return textResponse("done")
		})
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(system, "You are the decision stage") {
		t.Fatalf("the step's role did not reach the child's prompt:\n%s", system)
	}
	if effort != llm.EffortHigh {
		t.Fatalf("the step's effort did not reach the child's request: %q", effort)
	}
}

// A message that fails its schema fails the step, names the problems, and starts no
// dependant: the budget shows only the two agents that ran.
func TestWorkflowSchemaFailureStopsDependants(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"w","steps":[
		{"id":"decide","prompt":"decide","result_schema":"decision"},
		{"id":"analyze","depends_on":["decide"],"prompt":"analyze {{decide.result}}","result_schema":"roadmap"},
		{"id":"work","depends_on":["analyze"],"prompt":"work {{analyze.result}}"}]}`
	var mu sync.Mutex
	var started []string
	tr := runSpec(t, parent, spec, func(req llm.Request) *llm.Response {
		mu.Lock()
		defer mu.Unlock()
		started = append(started, lastUserText(req))
		if strings.HasPrefix(lastUserText(req), "analyze") {
			return textResponse(`{"items":[]}`) // fails the roadmap schema
		}
		if lastUserText(req) == "decide" {
			return textResponse(`{"summary":"s","options":[{"name":"A","rationale":"r"}],"chosen":"A"}`)
		}
		return textResponse("work ran")
	})
	if !tr.IsError || !strings.Contains(tr.Content, "analyze") || !strings.Contains(tr.Content, "1 to 16") {
		t.Fatalf("the failure is not reported with its problems: %+v", tr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(started) != 2 {
		t.Fatalf("started %v, want exactly decide and analyze", started)
	}
	if !strings.Contains(tr.Content, "work") || !strings.Contains(tr.Content, "skipped") {
		t.Fatalf("the dependant is not shown as skipped: %s", tr.Content)
	}
	if parent.budget.used != 2 {
		t.Fatalf("budget used = %d, want 2", parent.budget.used)
	}
}

// Items name their own order: B waits for A, an independent C does not, and a fan-out
// with no depends_on runs exactly as before.
func TestWorkflowItemDeps(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"w","max_parallel":4,"steps":[
		{"id":"plan","prompt":"plan"},
		{"id":"work","depends_on":["plan"],"for_each":{"from":"plan","path":"items"},"prompt":"do {{item}}"}]}`
	var mu sync.Mutex
	var order []string
	aDone := make(chan struct{})
	tr := runSpec(t, parent, spec, func(req llm.Request) *llm.Response {
		text := lastUserText(req)
		switch {
		case text == "plan":
			return textResponse(`{"items":[{"id":"A"},{"id":"B","depends_on":["A"]},{"id":"C"}]}`)
		case strings.HasPrefix(text, "do ") && strings.Contains(text, `"id":"A"`):
			time.Sleep(150 * time.Millisecond)
			mu.Lock()
			order = append(order, "A-end")
			mu.Unlock()
			close(aDone)
			return textResponse("a")
		case strings.HasPrefix(text, "do ") && strings.Contains(text, `"id":"B"`):
			mu.Lock()
			order = append(order, "B-start")
			mu.Unlock()
			select {
			case <-aDone:
			default:
				t.Error("B started before A ended")
			}
			return textResponse("b")
		case strings.HasPrefix(text, "do "):
			mu.Lock()
			order = append(order, "C-start")
			mu.Unlock()
			return textResponse("c")
		}
		return textResponse("?")
	})
	if tr.IsError {
		t.Fatalf("workflow failed: %s", tr.Content)
	}
	mu.Lock()
	defer mu.Unlock()
	idx := func(s string) int {
		for i, o := range order {
			if o == s {
				return i
			}
		}
		return -1
	}
	if idx("C-start") < 0 || idx("C-start") > idx("A-end") {
		t.Fatalf("an independent item waited for A: %v", order)
	}
	if idx("B-start") < idx("A-end") {
		t.Fatalf("B started before A ended: %v", order)
	}
}

// A fan-out with no item dependencies keeps today's behaviour: items run concurrently up
// to the bound and the result lists them in order.
func TestWorkflowFanOutWithoutDepsIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"fan","steps":[
		{"id":"scan","prompt":"list"},
		{"id":"check","depends_on":["scan"],"for_each":{"from":"scan","path":"files"},"prompt":"Check {{item}}"}]}`
	var mu sync.Mutex
	inflight, peak := 0, 0
	tr := runSpec(t, parent, spec, func(req llm.Request) *llm.Response {
		if lastUserText(req) == "list" {
			return textResponse(`{"files":["a","b","c"]}`)
		}
		mu.Lock()
		inflight++
		peak = max(peak, inflight)
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		inflight--
		mu.Unlock()
		return textResponse("ok " + lastUserText(req))
	})
	if tr.IsError {
		t.Fatalf("workflow failed: %s", tr.Content)
	}
	if peak < 2 {
		t.Fatalf("a dependency-free fan-out did not run concurrently (peak %d)", peak)
	}
	for _, want := range []string{"check[0]:", "check[1]:", "check[2]:"} {
		if !strings.Contains(tr.Content, want) {
			t.Fatalf("missing %q in %s", want, tr.Content)
		}
	}
}

// A fan-out whose items depend on each other in a cycle fails the step at once rather
// than waiting on itself: the scheduler checks the order it is handed, whatever the
// schema did or did not.
func TestWorkflowItemCycleFailsFast(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"w","steps":[
		{"id":"plan","prompt":"plan"},
		{"id":"work","depends_on":["plan"],"for_each":{"from":"plan","path":"items"},"prompt":"do {{item}}"}]}`
	started := make(chan struct{})
	go func() {
		defer close(started)
		tr := runSpec(t, parent, spec, func(req llm.Request) *llm.Response {
			if lastUserText(req) == "plan" {
				return textResponse(`{"items":[{"id":"A","depends_on":["B"]},{"id":"B","depends_on":["A"]}]}`)
			}
			t.Errorf("an item of a cyclic fan-out started: %q", lastUserText(req))
			return textResponse("x")
		})
		if !tr.IsError || !strings.Contains(tr.Content, "dependency cycle") {
			t.Errorf("the cycle was not reported: %+v", tr)
		}
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("a cyclic fan-out hung instead of failing")
	}
}
