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
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
)

func decodeForTest(t *testing.T, raw string) workflowSpec {
	t.Helper()
	spec, err := decodeWorkflowSpec(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return spec
}

// TestWorkflowValidationIsPure pins the refusals that must happen without a
// provider. Every message names the thing its author has to change.
func TestWorkflowValidationIsPure(t *testing.T) {
	valid := `{"name":"ok","steps":[{"id":"a","prompt":"p"}]}`
	if err := validateWorkflow(decodeForTest(t, valid)); err != nil {
		t.Fatalf("valid workflow refused: %v", err)
	}

	cases := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "cycle",
			spec: `{"name":"x","steps":[
				{"id":"a","prompt":"p","depends_on":["b"]},
				{"id":"b","prompt":"p","depends_on":["a"]}]}`,
			want: "dependency cycle",
		},
		{
			name: "duplicate id",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"p"},{"id":"a","prompt":"q"}]}`,
			want: `step "a" is defined twice`,
		},
		{
			name: "unknown dependency",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"p","depends_on":["ghost"]}]}`,
			want: `depends on unknown step "ghost"`,
		},
		{
			name: "later dependency",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"p","depends_on":["b"]},{"id":"b","prompt":"q"}]}`,
			want: "which is listed later",
		},
		{
			name: "unknown token",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"use {{nope}}"}]}`,
			want: "unknown token {{nope}}",
		},
		{
			name: "item outside fan-out",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"use {{item}}"}]}`,
			want: "only available in a step with for_each",
		},
		{
			name: "reads its own answer",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"use {{a.result}}"}]}`,
			want: "own answer",
		},
		{
			name: "no steps",
			spec: `{"name":"x","steps":[]}`,
			want: "at least one step",
		},
		{
			name: "empty prompt",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"   "}]}`,
			want: "prompt is required",
		},
		{
			name: "missing name",
			spec: `{"steps":[{"id":"a","prompt":"p"}]}`,
			want: "name is required",
		},
		{
			name: "parallel bound",
			spec: `{"name":"x","max_parallel":9,"steps":[{"id":"a","prompt":"p"}]}`,
			want: "between 1 and 8",
		},
		{
			name: "fan-out in fan-out",
			spec: `{"name":"x","steps":[
				{"id":"a","prompt":"p"},
				{"id":"b","for_each":{"from":"a","path":"items"},"prompt":"p"},
				{"id":"c","for_each":{"from":"b","path":"items"},"prompt":"p"}]}`,
			want: "fan-out is one level only",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWorkflow(decodeForTest(t, tc.spec))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestWorkflowDecodeRefusesUnknownFields pins the misspelled-field rule: it is
// a decode error, not a silently ignored bound.
func TestWorkflowDecodeRefusesUnknownFields(t *testing.T) {
	_, err := decodeWorkflowSpec(json.RawMessage(
		`{"name":"x","steps":[{"id":"a","prompt":"p","toolz":["read"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("decode = %v, want an unknown-field error", err)
	}
}

// TestWorkflowRefusedBeforeStartingAnAgent drives the tool through a run and
// asserts the provider saw no child prompt: the refusal happens before any
// agent exists, not after the first step.
func TestWorkflowRefusedBeforeStartingAnAgent(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "cycle",
			spec: `{"name":"x","steps":[
				{"id":"a","prompt":"p","depends_on":["b"]},
				{"id":"b","prompt":"p","depends_on":["a"]}]}`,
			want: "dependency cycle",
		},
		{
			name: "duplicate id",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"p"},{"id":"a","prompt":"q"}]}`,
			want: "defined twice",
		},
		{
			name: "later dependency",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"p","depends_on":["b"]},{"id":"b","prompt":"q"}]}`,
			want: "listed later",
		},
		{
			name: "unknown field",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"p","toolz":["read"]}]}`,
			want: "unknown field",
		},
		{
			name: "unknown token",
			spec: `{"name":"x","steps":[{"id":"a","prompt":"use {{nope}}"}]}`,
			want: "unknown token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var mu sync.Mutex
			var prompts []string
			parent, err := Create(context.Background(), subagentTestOptions(dir))
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
				text := lastUserText(req)
				mu.Lock()
				prompts = append(prompts, text)
				mu.Unlock()
				if text == "parent task" && len(trailingToolResults(req)) == 0 {
					return callResponse("call-1", "run_workflow", tc.spec), nil
				}
				return textResponse("parent done"), nil
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
				t.Fatalf("parent run = %+v", res)
			}
			tr := findToolResult(events, "run_workflow")
			if tr == nil || !tr.IsError {
				t.Fatalf("tool result = %+v, want an error", tr)
			}
			if !strings.Contains(tr.Content, tc.want) {
				t.Fatalf("result = %q, want it to contain %q", tr.Content, tc.want)
			}
			if !strings.Contains(tr.Content, "no agent was started") {
				t.Fatalf("refusal does not say nothing ran: %q", tr.Content)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, prompt := range prompts {
				if prompt != "parent task" {
					t.Fatalf("a child was started with %q", prompt)
				}
			}
		})
	}
}

// TestWorkflowLinearSubstitution checks the main path: three steps in order,
// and {{scan.result}} reaching the next child as the literal answer.
func TestWorkflowLinearSubstitution(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"linear","steps":[
		{"id":"scan","prompt":"scan the tree"},
		{"id":"fix","depends_on":["scan"],"prompt":"Fix {{scan.result}}"},
		{"id":"check","depends_on":["fix"],"prompt":"Check the fix"}]}`
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch lastUserText(req) {
		case "scan the tree":
			return textResponse("found 2 files"), nil
		case "Fix found 2 files":
			return textResponse("fixed both"), nil
		case "Check the fix":
			return textResponse("all good"), nil
		case "parent task":
			if len(trailingToolResults(req)) == 0 {
				return callResponse("call-1", "run_workflow", spec), nil
			}
			return textResponse("parent done"), nil
		}
		return textResponse("unexpected prompt"), nil
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
		t.Fatalf("parent run = %+v", res)
	}
	tr := findToolResult(events, "run_workflow")
	if tr == nil || tr.IsError {
		t.Fatalf("tool result = %+v", tr)
	}
	for _, want := range []string{
		"workflow linear: finished (3 steps)",
		"found 2 files",
		"fixed both",
		"all good",
	} {
		if !strings.Contains(tr.Content, want) {
			t.Fatalf("result = %q, missing %q", tr.Content, want)
		}
	}
	// Rows keep the document's order, which is how the author refers to them.
	lines := strings.Split(tr.Content, "\n")
	for i, id := range []string{"scan", "fix", "check"} {
		if !strings.HasPrefix(lines[1+i], id+" ") {
			t.Fatalf("row %d = %q, want it to start with %q", i, lines[1+i], id)
		}
	}

	// Every step's child carries the workflow call as its origin.
	infos, err := List(context.Background(), ListOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	children := 0
	for _, info := range infos {
		if info.ParentAgentID != parent.id {
			continue
		}
		children++
		if info.ParentToolCallID != "call-1" || info.Depth != 1 || info.Kind != "subagent" {
			t.Fatalf("child identity = %+v", info)
		}
	}
	if children != 3 {
		t.Fatalf("children = %d, want 3", children)
	}
}

// TestWorkflowConcurrencyBound checks that independent steps overlap up to the
// bound and no further, and that the result still lists them in document order.
func TestWorkflowConcurrencyBound(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	var mu sync.Mutex
	inflight, peak := 0, 0
	spec := `{"name":"wide","max_parallel":2,"steps":[
		{"id":"one","prompt":"do 1"},
		{"id":"two","prompt":"do 2"},
		{"id":"three","prompt":"do 3"},
		{"id":"four","prompt":"do 4"}]}`
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		text := lastUserText(req)
		if strings.HasPrefix(text, "do ") {
			mu.Lock()
			inflight++
			if inflight > peak {
				peak = inflight
			}
			mu.Unlock()
			time.Sleep(60 * time.Millisecond)
			mu.Lock()
			inflight--
			mu.Unlock()
			return textResponse("finished " + strings.TrimPrefix(text, "do ")), nil
		}
		if len(trailingToolResults(req)) == 0 {
			return callResponse("call-1", "run_workflow", spec), nil
		}
		return textResponse("parent done"), nil
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
	if tr == nil || tr.IsError {
		t.Fatalf("tool result = %+v", tr)
	}
	mu.Lock()
	got := peak
	mu.Unlock()
	if got != 2 {
		t.Fatalf("peak in flight = %d, want exactly 2", got)
	}
	lines := strings.Split(tr.Content, "\n")
	for i, id := range []string{"one", "two", "three", "four"} {
		if !strings.HasPrefix(lines[1+i], id+" ") {
			t.Fatalf("row %d = %q, want it to start with %q", i, lines[1+i], id)
		}
	}
}

// TestWorkflowFanOut checks that one step runs one child per array element with
// {{item}} and {{index}} substituted.
func TestWorkflowFanOut(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"fan","steps":[
		{"id":"scan","prompt":"list files"},
		{"id":"check","depends_on":["scan"],"for_each":{"from":"scan","path":"files"},
		 "prompt":"Check {{item}} ({{index}})","max_parallel":2}]}`
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		text := lastUserText(req)
		switch {
		case text == "list files":
			return textResponse(`{"files":["a.go","b.go"]}`), nil
		case text == "Check a.go (0)":
			return textResponse("a ok"), nil
		case text == "Check b.go (1)":
			return textResponse("b ok"), nil
		case text == "parent task":
			if len(trailingToolResults(req)) == 0 {
				return callResponse("call-1", "run_workflow", spec), nil
			}
			return textResponse("parent done"), nil
		}
		return textResponse("unexpected prompt: " + text), nil
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
	if tr == nil || tr.IsError {
		t.Fatalf("tool result = %+v", tr)
	}
	for _, want := range []string{
		"workflow fan: finished (2 steps)",
		"check[0]:",
		"a ok",
		"check[1]:",
		"b ok",
	} {
		if !strings.Contains(tr.Content, want) {
			t.Fatalf("result = %q, missing %q", tr.Content, want)
		}
	}
}

// TestWorkflowFanOutRequiresAnArray checks the failure that is better than a
// silent zero children: a source that is not an array fails the step, naming the
// path.
func TestWorkflowFanOutRequiresAnArray(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"badfan","steps":[
		{"id":"scan","prompt":"list files"},
		{"id":"check","depends_on":["scan"],"for_each":{"from":"scan","path":"files"},"prompt":"Check {{item}}"}]}`
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch lastUserText(req) {
		case "list files":
			return textResponse(`{"files":"not a list"}`), nil
		case "parent task":
			if len(trailingToolResults(req)) == 0 {
				return callResponse("call-1", "run_workflow", spec), nil
			}
			return textResponse("parent done"), nil
		}
		return textResponse("unexpected prompt"), nil
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
	if res.Status != StatusFinished {
		t.Fatalf("parent run = %+v", res)
	}
	tr := findToolResult(events, "run_workflow")
	if tr == nil || !tr.IsError {
		t.Fatalf("tool result = %+v, want a failed workflow", tr)
	}
	for _, want := range []string{
		"workflow badfan: failed",
		"for_each over scan",
		`"files" is not an array`,
		"check",
	} {
		if !strings.Contains(tr.Content, want) {
			t.Fatalf("result = %q, missing %q", tr.Content, want)
		}
	}
}

// TestWorkflowFailedStepSkipsDependents checks the failure rule: dependents are
// skipped with a reason, an independent step that is already running finishes,
// and the workflow is failed.
func TestWorkflowFailedStepSkipsDependents(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"fail","max_parallel":2,"steps":[
		{"id":"scan","prompt":"explode"},
		{"id":"other","prompt":"slow"},
		{"id":"fix","depends_on":["scan"],"prompt":"Fix it"}]}`
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch lastUserText(req) {
		case "explode":
			return nil, errors.New("boom")
		case "slow":
			time.Sleep(80 * time.Millisecond)
			return textResponse("unaffected"), nil
		case "parent task":
			if len(trailingToolResults(req)) == 0 {
				return callResponse("call-1", "run_workflow", spec), nil
			}
			return textResponse("parent done"), nil
		}
		return textResponse("unexpected prompt"), nil
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
		t.Fatalf("parent run = %+v", res)
	}
	tr := findToolResult(events, "run_workflow")
	if tr == nil || !tr.IsError {
		t.Fatalf("tool result = %+v, want a failed workflow", tr)
	}
	for _, want := range []string{
		"workflow fail: failed (3 steps, 1 failed, 1 skipped)",
		"boom",
		"depends on scan, which failed",
		"unaffected",
	} {
		if !strings.Contains(tr.Content, want) {
			t.Fatalf("result = %q, missing %q", tr.Content, want)
		}
	}
}

// TestWorkflowCancellation cancels the workflow's context and checks that the
// running child is cancelled rather than left behind.
func TestWorkflowCancellation(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parent.complete = blockingCompleter{}

	spec := decodeForTest(t, `{"name":"cancel","steps":[{"id":"slow","prompt":"wait"}]}`)
	if err := validateWorkflow(spec); err != nil {
		t.Fatal(err)
	}
	plan, err := newWorkflowPlan(parent.registry, spec)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	block, status := executeWorkflow(ctx, parent, plan, "", "call-1")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("workflow took %s to notice the cancel", elapsed)
	}
	if status != workflowCancelled {
		t.Fatalf("status = %s, block = %q", status, block)
	}
	if !strings.Contains(block, "workflow cancel: cancelled") || !strings.Contains(block, "cancelled") {
		t.Fatalf("block = %q", block)
	}
}

// TestWorkflowBackground checks that the whole workflow is one task, titled with
// the workflow's name, and that its output is the result block.
func TestWorkflowBackground(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	spec := `{"name":"nightly","background":true,"steps":[{"id":"scan","prompt":"look around"}]}`
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch lastUserText(req) {
		case "look around":
			return textResponse("saw it"), nil
		case "parent task":
			if len(trailingToolResults(req)) == 0 {
				return callResponse("call-1", "run_workflow", spec), nil
			}
			return textResponse("parent done"), nil
		}
		return textResponse("unexpected prompt"), nil
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
		t.Fatalf("parent run = %+v", res)
	}
	tr := findToolResult(events, "run_workflow")
	if tr == nil || tr.IsError {
		t.Fatalf("tool result = %+v", tr)
	}
	if !strings.Contains(tr.Content, "started in the background: workflow nightly") {
		t.Fatalf("tool result = %q", tr.Content)
	}
	id := taskIDFromResult(tr.Content)
	if id == "" {
		t.Fatalf("no task id in %q", tr.Content)
	}

	final := waitTask(t, parent, id)
	if final.Kind != TaskKindAgent || final.Status != TaskStatusFinished {
		t.Fatalf("final task = %+v", final)
	}
	if final.Title != "nightly" {
		t.Fatalf("task title = %q, want the workflow's name", final.Title)
	}
	out, err := parent.TaskOutput(id, 0, 0)
	if err != nil {
		t.Fatalf("TaskOutput: %v", err)
	}
	for _, want := range []string{"workflow nightly: finished (1 step)", "saw it"} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("task output = %q, missing %q", out.Text, want)
		}
	}
}

// TestWorkflowSharesTheDepthLimit pins that a step's child is at the same depth
// as a spawn_agent child, so the tool is absent at the last depth.
func TestWorkflowSharesTheDepthLimit(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if !hasTool(parent, "run_workflow") || !hasTool(parent, "spawn_agent") {
		t.Fatalf("tools at depth 0 = %v", parent.registry.Names())
	}

	opts := subagentTestOptions(t.TempDir())
	opts.Subagents.MaxDepth = 2
	child, err := createAgent(context.Background(), opts, subagentIdentity{
		ParentAgentID: "agent-parent",
		Depth:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if hasTool(child, "run_workflow") {
		t.Fatal("a child at the last depth has run_workflow")
	}
}
