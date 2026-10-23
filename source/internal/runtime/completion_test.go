// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import (
	"encoding/json"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestCompletionRequirementRecognizesCommitRequest(t *testing.T) {
	message := "<context>\n<diagnostics count=\"0\">\n</diagnostics>\n</context>\n\ngit 先把非Streaming的提交下"
	if got := completionRequirementFor(message); got != requireCommit {
		t.Fatalf("requirement=%v", got)
	}
}

func TestCompletionRequirementDoesNotTurnQuestionsIntoMutations(t *testing.T) {
	for _, message := range []string{
		"为什么这次修改没有生效？",
		"这个架构图怎么调整一下比较好？",
		"这个文件需要修改吗？",
		"解释一下 git commit 的工作原理",
		"不要提交，只看一下改动",
		"I will update this myself; explain the API.",
	} {
		if got := completionRequirementFor(message); got != requireNoSideEffect {
			t.Fatalf("message=%q requirement=%v", message, got)
		}
	}
}

func TestCompletionRequirementRecognizesConversationalChineseChangeRequests(t *testing.T) {
	for _, message := range []string{
		"好，调一下，定定案的TextureStreaming架构图",
		"我们的这个需要改掉，做成后面这种的",
		"把状态动画补上",
		"可以帮我调整一下吗？",
	} {
		if got := completionRequirementFor(message); got != requireMutation {
			t.Fatalf("message=%q requirement=%v", message, got)
		}
	}
}

func TestShellCompletionEvidenceDistinguishesInspectionFromCommit(t *testing.T) {
	if shellPerformsCommit("git diff --name-only --cached && git diff --shortstat") {
		t.Fatal("read-only diff counted as a commit")
	}
	if !shellPerformsCommit(`git -c user.name=Anvil -c user.email=a@example.test commit -m "snapshot"`) {
		t.Fatal("git commit with options was not recognized")
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
