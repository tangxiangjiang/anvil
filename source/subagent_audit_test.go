// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

// audit #22: spawn_agent takes a budget slot before the background task starts,
// and does not return the slot when StartAgent refuses. A refused start therefore
// uses up the budget with no child ever running.
func TestAuditRefusedBackgroundSpawnReturnsItsBudget(t *testing.T) {
	dir := t.TempDir()
	opts := subagentTestOptions(dir)
	opts.Subagents.MaxAgents = 1
	opts.Subagents.MaxBackground = 1
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	spawn := `{"prompt":"child work","description":"child","background":true}`
	var results []string
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		for _, m := range trailingToolResults(req) {
			if len(results) < len(trailingToolResults(req)) {
				results = append(results, m.Content)
			}
		}
		switch len(trailingToolResults(req)) {
		case 0:
			// Fill the task table with a long background shell.
			return callResponse("c1", "shell", `{"command":"sleep 30","run_in_background":true}`), nil
		case 1:
			// Refused: the task table is full. The budget slot is taken before this.
			return callResponse("c2", "spawn_agent", spawn), nil
		case 2:
			// Free the task table for real: cancel the background shell, then try again.
			if id := taskIDFromResult(results[0]); id != "" {
				parent.CancelTask(id)
			}
			return callResponse("c3", "spawn_agent", spawn), nil
		default:
			return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
		}
	})
	run, err := parent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	var spawns []string
	for _, r := range results {
		if strings.Contains(r, "agent") {
			spawns = append(spawns, r)
		}
	}
	if len(spawns) < 2 {
		t.Fatalf("expected two spawn results, got %d: %v", len(spawns), results)
	}
	if strings.Contains(spawns[len(spawns)-1], "already created") {
		t.Fatalf("a refused background spawn kept its budget slot; the next spawn was refused with: %s", spawns[len(spawns)-1])
	}
}

// audit #24: Close sets a.taskMgr to nil, so Tasks, Task, TaskOutput and CancelTask
// report nothing after the session closed, even though the task list is meant to
// show the background work as stopped.
func TestAuditTasksAfterCloseShowStopped(t *testing.T) {
	dir := t.TempDir()
	opts := subagentTestOptions(dir)
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if len(trailingToolResults(req)) == 0 {
			return callResponse("c1", "shell", `{"command":"sleep 30","run_in_background":true}`), nil
		}
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
	})
	run, err := parent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(parent.Tasks()) == 0 {
		t.Fatal("no background task recorded before Close")
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if got := parent.Tasks(); len(got) != 1 {
		t.Fatalf("after Close the task list has %d row(s), want the stopped task; got %+v", len(got), got)
	}
}

// audit #25: childOptions does not copy AllowHookCommands, so a child is built with
// command hooks switched off and a command deny in the workspace hooks file is
// skipped for the child's calls. The design says a deny refuses a child's call
// exactly as it refuses the parent's.
func TestAuditChildInheritsHookCommandPermission(t *testing.T) {
	parent := &Agent{opts: Options{Subagents: SubagentOptions{Enabled: true}, AllowHookCommands: true}}
	child := parent.childOptions(spawnParams{Prompt: "p", Description: "d"}, nil, 1)
	if !child.AllowHookCommands {
		t.Fatal("the child is built without AllowHookCommands, so a command deny in the workspace hooks file is skipped for it")
	}
}

// audit #26: executeRun forwards every runtime event to OnEvent, including the
// child's "done". Design 42 says a child's done is not forwarded as a stream event.
func TestAuditChildDoneIsNotForwarded(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var types []string
	opts := subagentTestOptions(dir)
	opts.Subagents.OnEvent = func(ev SubagentEvent) {
		mu.Lock()
		defer mu.Unlock()
		types = append(types, string(ev.Event.Type))
	}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch len(trailingToolResults(req)) {
		case 0:
			return callResponse("c1", "spawn_agent", `{"prompt":"child work","description":"child"}`), nil
		case 1:
			return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "child text"}}, nil
		default:
			return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
		}
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
	for _, typ := range types {
		if typ == "done" {
			t.Fatalf("a child's done was forwarded to OnEvent; events seen: %v", types)
		}
	}
}
