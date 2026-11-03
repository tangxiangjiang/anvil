// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"testing"
	"time"
)

func completedDurableRun(t *testing.T) (*harness, string, string) {
	t.Helper()
	llm := newStubLLM(t, textTurn("durable answer"))
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)
	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "persist this"})
	done := paramsOf(t, h.collectRunOK(15*time.Second).done)
	runID, _ := done["run_id"].(string)
	if agentID == "" || runID == "" {
		t.Fatalf("session=%v done=%v", sess, done)
	}
	return h, agentID, runID
}

func TestHostRehydrate(t *testing.T) {
	h, agentID, runID := completedDurableRun(t)
	listed := resultOf(t, h.callOK(MethodRunList, RunListParams{AgentID: agentID}))
	runs, _ := listed["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs=%v", listed)
	}
	run := runs[0].(map[string]any)
	if run["id"] != runID || run["state"] != "finished" {
		t.Fatalf("run=%v", run)
	}
	got := resultOf(t, h.callOK(MethodRunGet, RunGetParams{AgentID: agentID, RunID: runID}))
	if got["run"].(map[string]any)["last_seq"].(float64) < 1 {
		t.Fatalf("run.get=%v", got)
	}
}

func TestHostWatermark(t *testing.T) {
	h, agentID, runID := completedDurableRun(t)
	first := resultOf(t, h.callOK(MethodRunEvents, RunEventsParams{
		AgentID: agentID, RunID: runID, Limit: 1,
	}))
	events := first["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("first page=%v", first)
	}
	seq := uint64(events[0].(map[string]any)["seq"].(float64))
	next := uint64(first["next_seq"].(float64))
	if seq == 0 || next != seq {
		t.Fatalf("first page=%v", first)
	}
	second := resultOf(t, h.callOK(MethodRunEvents, RunEventsParams{
		AgentID: agentID, RunID: runID, AfterSeq: next,
	}))
	for _, raw := range second["events"].([]any) {
		if got := uint64(raw.(map[string]any)["seq"].(float64)); got <= next {
			t.Fatalf("event sequence %d was not after watermark %d", got, next)
		}
	}
	if uint64(second["next_seq"].(float64)) < next {
		t.Fatalf("watermark moved backwards: first=%v second=%v", first, second)
	}
	wrong := h.call(MethodRunGet, RunGetParams{AgentID: agentID, RunID: "run-not-owned"})
	rpcErr, ok := rpcErrorOf(wrong)
	if !ok || rpcErr.Code != CodeNoRun {
		t.Fatalf("run mismatch response=%v", wrong)
	}
}
