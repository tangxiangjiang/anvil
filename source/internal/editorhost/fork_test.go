// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"strings"
	"testing"
)

// Forking over the wire: the host answers with a new agent id, that agent is a
// second row in `session.list`, and its transcript is the prefix the fork was
// asked for. The source stays exactly as it was, which is the whole point of a
// branch rather than an undo.
func TestSessionForkBranchesAtTheRun(t *testing.T) {
	llm := newStubLLM(t, textTurn("noted."))
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)
	out := promptRun(t, h, sessionID, "read the note", autoApprove)
	runID, _ := paramsOf(t, out.done)["run_id"].(string)
	if runID == "" {
		t.Fatalf("done carries no run id: %v", out.done)
	}
	before := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	beforeEntries, _ := before["entries"].([]any)

	forked := resultOf(t, h.callOK(MethodSessionFork, SessionForkParams{AgentID: agentID, RunID: runID}))
	newID, _ := forked["agent_id"].(string)
	if newID == "" || newID == agentID || !strings.HasPrefix(newID, "agent-") {
		t.Fatalf("fork returned %v, want a fresh agent id", forked)
	}

	// The fork resumes — the client's next step — and replays the same prefix.
	resumed := resultOf(t, h.callOK(MethodSessionResume, SessionParams{AgentID: newID}))
	if resumed["agent_id"] != newID {
		t.Fatalf("resume returned %v", resumed)
	}
	after := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: newID}))
	afterEntries, _ := after["entries"].([]any)
	if len(afterEntries) != len(beforeEntries) {
		t.Fatalf("fork transcript has %d entries, want the source's %d: %v", len(afterEntries), len(beforeEntries), after)
	}

	source := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	sourceEntries, _ := source["entries"].([]any)
	if len(sourceEntries) != len(beforeEntries) {
		t.Fatalf("the fork changed the source's history: %v", source)
	}

	listing := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	sessions, _ := listing["sessions"].([]any)
	var titles = map[string]string{}
	for _, entry := range sessions {
		row, _ := entry.(map[string]any)
		id, _ := row["agent_id"].(string)
		title, _ := row["title"].(string)
		titles[id] = title
	}
	if titles[agentID] != "read the note" {
		t.Fatalf("source title = %q", titles[agentID])
	}
	if titles[newID] != "read the note (fork)" {
		t.Fatalf("fork title = %q, want the branch to be distinguishable", titles[newID])
	}
}

func TestSessionForkRefusesAnUnknownRunOrMissingParams(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	agentID, _ := sess["agent_id"].(string)
	promptRun(t, h, sess["session_id"].(string), "hello", autoApprove)

	missingRun := h.call(MethodSessionFork, SessionForkParams{AgentID: agentID, RunID: "run-nope"})
	if rpcErr, ok := rpcErrorOf(missingRun); !ok || rpcErr.Code != CodeHostError {
		t.Fatalf("want a host error for an unknown run, got %v", missingRun)
	}

	empty := h.call(MethodSessionFork, SessionForkParams{})
	if rpcErr, ok := rpcErrorOf(empty); !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("want invalid params for empty ids, got %v", empty)
	}
}
