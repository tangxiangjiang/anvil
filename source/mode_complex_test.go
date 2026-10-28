// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

const goodDecision = `{"summary":"change a and b","options":[{"name":"A","rationale":"simple"}],"chosen":"A"}`

func twoItemRoadmap() string {
	return roadmapJSON(
		map[string]any{"id": "a", "goal": "edit a", "plan": []string{"edit"}, "files": []string{"pkg/a.txt"}, "acceptance": []string{"a says new"}, "depends_on": []string{}},
		map[string]any{"id": "b", "goal": "edit b", "plan": []string{"edit"}, "files": []string{"pkg/b.txt"}, "acceptance": []string{"b says new"}, "depends_on": []string{"a"}},
	)
}

func complexOptions(dir string) Options {
	opts := subagentTestOptions(dir)
	opts.Mode = ModeComplex
	opts.Subagents.MaxDepth = 3
	opts.Subagents.MaxAgents = 8
	return opts
}

// stageOf says which stage a child request belongs to, from the role sentence in its prompt.
func stageOf(req llm.Request) string {
	sys := systemOf(req)
	switch {
	case strings.Contains(sys, "You are the decision stage"):
		return "decision"
	case strings.Contains(sys, "You are the analysis stage"):
		return "analyze"
	case strings.Contains(sys, "The brief below is your task"):
		return "worker"
	}
	return "root"
}

// One Send runs decide, analyze and the Workers as a workflow, and the Workers are
// delegate-style children: allowlisted, reporting against acceptance, at depth 1.
func TestComplexSend(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pkg/a.txt": "old", "pkg/b.txt": "old", "README.md": "old"})
	opts := complexOptions(dir)
	opts.Checkpoints = &CheckpointOptions{}
	var mu sync.Mutex
	var events []SubagentEvent
	opts.Subagents.OnEvent = func(ev SubagentEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	var order []string
	var efforts = map[string]llm.Effort{}
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		stage := stageOf(req)
		mu.Lock()
		if len(trailingToolResults(req)) == 0 {
			order = append(order, stage)
			efforts[stage] = req.Effort
		}
		mu.Unlock()
		switch stage {
		case "decision":
			if lastUserText(req) != "make both files new" {
				t.Errorf("the decision stage did not get the user's message: %q", lastUserText(req))
			}
			return textResponse(goodDecision), nil
		case "analyze":
			if !strings.Contains(lastUserText(req), `"chosen":"A"`) {
				t.Errorf("analyze did not receive the decision: %q", lastUserText(req))
			}
			return textResponse(twoItemRoadmap()), nil
		case "worker":
			name := "pkg/a.txt"
			if strings.Contains(systemOf(req), "Goal: edit b") {
				name = "pkg/b.txt"
			}
			if len(trailingToolResults(req)) == 0 {
				return callResponse("w", "edit_file", editArgs(name)), nil
			}
			return textResponse("[met] " + map[string]string{"pkg/a.txt": "a says new", "pkg/b.txt": "b says new"}[name]), nil
		}
		return textResponse("unexpected"), nil
	})

	run, err := parent.Send(context.Background(), "make both files new")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished {
		t.Fatalf("status=%s err=%s result=%s", res.Status, res.Err, res.Result)
	}
	for _, name := range []string{"a", "b"} {
		if got, _ := os.ReadFile(filepath.Join(dir, "pkg", name+".txt")); string(got) != "new" {
			t.Fatalf("pkg/%s.txt = %q", name, got)
		}
	}
	for _, want := range []string{"workflow complex: finished (3 steps)", "decide", "analyze", "work", "[met]", "changed: pkg/a.txt", "changed: pkg/b.txt"} {
		if !strings.Contains(res.Result, want) {
			t.Fatalf("the run's result lacks %q:\n%s", want, res.Result)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	// decision, then analyze, then both workers; b waited for a.
	if len(order) != 4 || order[0] != "decision" || order[1] != "analyze" || order[2] != "worker" || order[3] != "worker" {
		t.Fatalf("stage order %v", order)
	}
	if efforts["decision"] != llm.EffortHigh || efforts["analyze"] != llm.EffortMedium || efforts["worker"] != llm.EffortLow {
		t.Fatalf("stage efforts %v", efforts)
	}
	agentsSeen := map[string]bool{}
	for _, ev := range events {
		if ev.Depth != 1 {
			t.Fatalf("a stage child is at depth %d, want 1", ev.Depth)
		}
		if !strings.HasPrefix(ev.ToolCallID, "complex-") || ev.ParentAgentID != parent.id {
			t.Fatalf("event envelope %+v", ev)
		}
		agentsSeen[ev.AgentID] = true
	}
	if len(agentsSeen) != 4 {
		t.Fatalf("%d child agents emitted events, want 4 (decide, analyze, two workers)", len(agentsSeen))
	}
	if parent.budget.used != 4 {
		t.Fatalf("budget used = %d, want 4", parent.budget.used)
	}
	if !strings.Contains(parent.systemPrompt, "delegate") {
		t.Log("complex is multi plus stages; the main prompt carries the delegate paragraph")
	}
}

// A Complex Worker is held to its item's files exactly like a delegate worker.
func TestComplexWorkerIsConfined(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pkg/a.txt": "old", "README.md": "old"})
	parent, err := Create(context.Background(), complexOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	one := roadmapJSON(map[string]any{"id": "a", "goal": "edit a", "plan": []string{"edit"}, "files": []string{"pkg/a.txt"},
		"acceptance": []string{"a says new"}, "depends_on": []string{}})
	var got string
	var mu sync.Mutex
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch stageOf(req) {
		case "decision":
			return textResponse(goodDecision), nil
		case "analyze":
			return textResponse(one), nil
		case "worker":
			n := len(trailingToolResults(req))
			if n == 0 {
				return callResponse("w", "edit_file", editArgs("README.md")), nil
			}
			mu.Lock()
			got = trailingToolResults(req)[0].Content
			mu.Unlock()
			return textResponse("[not met] a says new"), nil
		}
		return textResponse("?"), nil
	})
	run, err := parent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(got, "outside the delegated brief's files") {
		t.Fatalf("a Complex worker was not confined: %q", got)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(data) != "old" {
		t.Fatalf("README.md changed: %q", data)
	}
}

// An Analyze answer that does not hold ends the run before any Worker, and says why.
func TestComplexBadRoadmapStartsNoWorker(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), complexOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	var mu sync.Mutex
	workers := 0
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch stageOf(req) {
		case "decision":
			return textResponse(goodDecision), nil
		case "analyze":
			return textResponse(roadmapJSON(item("a", "b"), item("b", "a"))), nil // a cycle
		case "worker":
			mu.Lock()
			workers++
			mu.Unlock()
		}
		return textResponse("?"), nil
	})
	run, err := parent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	res, _ := run.Wait(context.Background())
	if res == nil || res.Status != StatusError || !strings.Contains(res.Result, "dependency cycle") {
		t.Fatalf("result = %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if workers != 0 {
		t.Fatalf("%d workers started for a roadmap that failed validation", workers)
	}
	if parent.budget.used != 2 {
		t.Fatalf("budget used = %d, want 2", parent.budget.used)
	}
}

// With the tree allowed three agents, decide and analyze leave one: a two-item roadmap is
// refused by the validator, and no Worker starts.
func TestComplexBudget(t *testing.T) {
	dir := t.TempDir()
	opts := complexOptions(dir)
	opts.Subagents.MaxAgents = 3
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	var mu sync.Mutex
	workers := 0
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch stageOf(req) {
		case "decision":
			return textResponse(goodDecision), nil
		case "analyze":
			return textResponse(twoItemRoadmap()), nil
		case "worker":
			mu.Lock()
			workers++
			mu.Unlock()
		}
		return textResponse("?"), nil
	})
	run, err := parent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	res, _ := run.Wait(context.Background())
	if res == nil || res.Status != StatusError || !strings.Contains(res.Result, "only 1 more agents") {
		t.Fatalf("result = %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if workers != 0 {
		t.Fatalf("%d workers started past the budget", workers)
	}
}

// Plan mode removes the write tools; a Worker stage edits by definition, so Send refuses
// before anything is created.
func TestComplexPlanModeRefused(t *testing.T) {
	dir := t.TempDir()
	opts := complexOptions(dir)
	opts.DisallowedTools = []string{"write", "edit", "shell", "verify"}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	called := false
	parent.complete = completerFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		called = true
		return textResponse("x"), nil
	})
	if _, err := parent.Send(context.Background(), "go"); err == nil || !strings.Contains(err.Error(), "plan mode") {
		t.Fatalf("Send in plan mode: %v", err)
	}
	if called || parent.budget.used != 0 {
		t.Fatalf("an agent was created for a refused run (called=%v used=%d)", called, parent.budget.used)
	}
}

func TestComplexOptionsValidate(t *testing.T) {
	dir := t.TempDir()
	noSub := complexOptions(dir)
	noSub.Subagents.Enabled = false
	if a, err := Create(context.Background(), noSub); err == nil {
		a.Close()
		t.Fatal("complex without subagents was accepted")
	}
	bad := complexOptions(dir)
	bad.Complex.Worker.Effort = "extreme"
	if a, err := Create(context.Background(), bad); err == nil {
		a.Close()
		t.Fatal("a bad stage effort was accepted")
	}
	// The SDK fills defaults and reads no file: an empty ComplexOptions is a working config.
	a, err := Create(context.Background(), complexOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
}
