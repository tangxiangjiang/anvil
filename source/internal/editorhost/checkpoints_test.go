// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A run that changed files leaves a checkpoint behind, and restoring it puts
// the workspace back: the file that existed comes back with its pre-run bytes,
// and the file the run created is gone. This is the whole feature, through the
// wire — the SDK-side arithmetic is pinned in source/checkpoints_test.go.
func TestCheckpointRestorePutsBackTheRun(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"after"}`),
		toolTurn("call_2", "write_file", `{"path":"created.txt","content":"new"}`),
		textTurn("done"),
	)
	h, root := initHost(t, llm.baseURL())
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := res["session_id"].(string)
	agentID, _ := res["agent_id"].(string)
	if sessionID == "" || agentID == "" {
		t.Fatalf("session.new returned no identity: %v", res)
	}
	out := promptRun(t, h, sessionID, "change things", autoApprove)
	if status := paramsOf(t, out.done)["status"]; status != "finished" {
		t.Fatalf("run did not finish: %v", paramsOf(t, out.done))
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "after" {
		t.Fatalf("a.txt = %q, want the run's write", got)
	}

	listed := resultOf(t, h.callOK(MethodCheckpointList, CheckpointListParams{AgentID: agentID}))
	runs, _ := listed["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("want one checkpointed run, got %v", listed)
	}
	run, _ := runs[0].(map[string]any)
	runID, _ := run["run_id"].(string)
	if runID == "" {
		t.Fatalf("the checkpoint names no run: %v", run)
	}
	files, _ := run["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("want both paths captured, got %v", files)
	}

	result := resultOf(t, h.callOK(MethodCheckpointRestore, CheckpointRestoreParams{AgentID: agentID, RunID: runID}))
	if got, _ := result["run_id"]; got != runID {
		t.Fatalf("restore answered for %v, want %s", got, runID)
	}
	restored, _ := result["files"].([]any)
	if len(restored) != 2 {
		t.Fatalf("want two files restored, got %v", result)
	}

	if got, err := os.ReadFile(filepath.Join(root, "a.txt")); err != nil || string(got) != "before" {
		t.Fatalf("a.txt = %q, %v; want the pre-run bytes", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("a file the run created must be deleted, stat err = %v", err)
	}
}

func TestCheckpointListIsEmptyBeforeAnyRun(t *testing.T) {
	llm := newStubLLM(t, textTurn("hello"))
	h, _ := initHost(t, llm.baseURL())
	res := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	agentID, _ := res["agent_id"].(string)

	listed := resultOf(t, h.callOK(MethodCheckpointList, CheckpointListParams{AgentID: agentID}))
	runs, ok := listed["runs"].([]any)
	if !ok || len(runs) != 0 {
		t.Fatalf("want an empty list, got %v", listed)
	}
}

func TestCheckpointRestoreRejectsAnUnknownRun(t *testing.T) {
	llm := newStubLLM(t, textTurn("hello"))
	h, _ := initHost(t, llm.baseURL())
	res := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	agentID, _ := res["agent_id"].(string)

	m := h.call(MethodCheckpointRestore, CheckpointRestoreParams{AgentID: agentID, RunID: "run-missing"})
	if _, isErr := rpcErrorOf(m); !isErr {
		t.Fatalf("restoring a run with no checkpoint must fail, got %v", m)
	}
}

func TestCheckpointMethodsRequireAnAgent(t *testing.T) {
	llm := newStubLLM(t, textTurn("hello"))
	h, _ := initHost(t, llm.baseURL())

	if _, isErr := rpcErrorOf(h.call(MethodCheckpointList, CheckpointListParams{})); !isErr {
		t.Fatal("checkpoint.list without an agent id must fail")
	}
	if _, isErr := rpcErrorOf(h.call(MethodCheckpointRestore, CheckpointRestoreParams{RunID: "run-1"})); !isErr {
		t.Fatal("checkpoint.restore without an agent id must fail")
	}
}
