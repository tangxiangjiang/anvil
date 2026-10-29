// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import (
	"encoding/json"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestFinishTaskDefinitionDeclaresTheTerminalContract(t *testing.T) {
	def := finishTaskDef()
	if def.Name != finishTaskToolName || !json.Valid(def.Parameters) {
		t.Fatalf("definition = %+v", def)
	}
	if def.Description == "" {
		t.Fatal("finish_task has no model-facing instruction")
	}
}

func TestFinishTaskSummaryAcceptsOnlyOneValidControlCall(t *testing.T) {
	summary, present, problem := finishTaskSummary([]llm.ToolCall{{
		ID: "finish", Name: finishTaskToolName, Args: json.RawMessage(`{"summary":"  work complete  "}`),
	}})
	if summary != "work complete" || !present || problem != "" {
		t.Fatalf("summary=%q present=%v problem=%q", summary, present, problem)
	}

	for _, calls := range [][]llm.ToolCall{
		{{Name: finishTaskToolName, Args: json.RawMessage(`{"summary":" "}`)}},
		{{Name: finishTaskToolName, Args: json.RawMessage(`{`)}},
		{{Name: finishTaskToolName, Args: json.RawMessage(`{"summary":"建议验证："}`)}},
		{{Name: finishTaskToolName, Args: json.RawMessage("{\"summary\":\"Verification:\\n```powershell\\nnpm test\"}")}},
		{
			{Name: finishTaskToolName, Args: json.RawMessage(`{"summary":"done"}`)},
			{Name: "read_file", Args: json.RawMessage(`{"path":"a"}`)},
		},
	} {
		if _, present, problem := finishTaskSummary(calls); !present || problem == "" {
			t.Fatalf("calls=%+v present=%v problem=%q", calls, present, problem)
		}
	}
}

func TestBackgroundCompositionRequiresJoinAndParentCheckpoint(t *testing.T) {
	var state backgroundComposition
	state.observe(
		[]llm.ToolCall{{
			Name: "spawn_agent",
			Args: json.RawMessage(`{"prompt":"inspect","description":"research","background":true}`),
		}},
		[]tools.Result{{Content: "task-123 started in the background: subagent research"}},
	)
	if !state.pending() || state.needsCheckpoint() {
		t.Fatalf("spawn state = %+v", state)
	}

	state.observe(
		[]llm.ToolCall{{Name: "task_output", Args: json.RawMessage(`{"task_id":"task-123"}`)}},
		[]tools.Result{{Content: "task-123 running\nnext offset 0"}},
	)
	if !state.pending() {
		t.Fatal("running output incorrectly joined the child")
	}

	state.observe(
		[]llm.ToolCall{{Name: "task_output", Args: json.RawMessage(`{"task_id":"task-123"}`)}},
		[]tools.Result{{Content: "task-123 finished\nchild result"}},
	)
	if state.pending() || !state.needsCheckpoint() {
		t.Fatalf("terminal state = %+v", state)
	}
	state.checkpointed = true
	if state.needsCheckpoint() {
		t.Fatal("parent composition checkpoint did not settle")
	}
}

func TestForegroundAndUnownedTasksDoNotCreateBackgroundContract(t *testing.T) {
	var state backgroundComposition
	state.observe(
		[]llm.ToolCall{
			{Name: "spawn_agent", Args: json.RawMessage(`{"prompt":"inspect","description":"research"}`)},
			{Name: "task_output", Args: json.RawMessage(`{"task_id":"old-task"}`)},
		},
		[]tools.Result{
			{Content: "agent-child finished"},
			{Content: "old-task finished\nold result"},
		},
	)
	if state.pending() || state.needsCheckpoint() || len(state.tasks) != 0 {
		t.Fatalf("unrelated calls created contract: %+v", state)
	}
}
