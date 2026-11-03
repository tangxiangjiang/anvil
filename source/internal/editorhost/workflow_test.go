// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"context"
	"testing"

	"github.com/anvil-dev/anvil"
)

func TestWorkflowPlanModePersistsReadOnlyProjection(t *testing.T) {
	llm := newStubLLM(t, textTurn("# Release plan\n\nCheck inputs, then build."))
	h, _ := initHost(t, llm.baseURL())
	session := resultOf(t, h.callOK(MethodSessionNew, SessionParams{PermissionMode: ModePlan}))
	sessionID, _ := session["session_id"].(string)
	agentID, _ := session["agent_id"].(string)
	promptRun(t, h, sessionID, "plan the release", autoApprove)

	first := resultOf(t, h.callOK(MethodWorkflowGet, WorkflowGetParams{AgentID: agentID}))
	artifacts, _ := first["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("workflow.get=%v", first)
	}
	artifact := artifacts[0].(map[string]any)
	if artifact["kind"] != "plan" || artifact["body"] != "# Release plan\n\nCheck inputs, then build." {
		t.Fatalf("artifact=%v", artifact)
	}
	if items, ok := artifact["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("prose plan items=%v", artifact["items"])
	}

	// Closing the live session forces the next read through the durable backend.
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})
	reopened := resultOf(t, h.callOK(MethodWorkflowGet, WorkflowGetParams{AgentID: agentID}))
	if reopened["last_seq"] != first["last_seq"] {
		t.Fatalf("reopened=%v first=%v", reopened, first)
	}
}

func TestWorkflowDecisionIsExplicitRevisionGuardedAndReloaded(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())
	session := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := session["session_id"].(string)
	agentID, _ := session["agent_id"].(string)
	sess, ok := h.srv.sessionForAgent(agentID)
	if !ok || sess.sdk() == nil {
		t.Fatal("live SDK session unavailable")
	}
	artifact := anvil.PlanArtifact{
		Schema: anvil.PlanSchemaVersion, ArtifactID: "artifact-control", RunID: "run-control",
		Kind: anvil.PlanKindComplex, Revision: 1, Title: "Controlled plan", Items: []anvil.WorkItem{
			{
				ID: "prepare", Title: "Prepare", Goal: "Inputs are ready",
				Plan: []string{"prepare"}, Files: []string{}, Acceptance: []string{"ready"},
				DependsOn: []string{}, Approval: anvil.WorkApprovalPending,
				State: anvil.WorkStateProposed, Source: anvil.WorkSourceComplex, ExecutionIDs: []string{},
			},
			{
				ID: "build", Title: "Build", Goal: "Artifact exists",
				Plan: []string{"build"}, Files: []string{}, Acceptance: []string{"built"},
				DependsOn: []string{"prepare"}, Approval: anvil.WorkApprovalPending,
				State: anvil.WorkStateProposed, Source: anvil.WorkSourceComplex, ExecutionIDs: []string{},
			},
		},
	}
	if err := sess.sdk().SavePlanArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}

	// Reads and navigation are represented by workflow.get only; no decision is
	// inferred from opening the projection.
	before := resultOf(t, h.callOK(MethodWorkflowGet, WorkflowGetParams{AgentID: agentID}))
	current := before["artifacts"].([]any)[0].(map[string]any)
	if current["revision"] != float64(1) {
		t.Fatalf("before=%v", before)
	}

	decided := resultOf(t, h.callOK(MethodWorkflowDecide, WorkflowDecideParams{
		AgentID: agentID, ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
		Decisions: []WorkflowDecision{{ItemID: "build", Decision: "approve"}},
	}))
	if decided["accepted_revision"] != float64(2) {
		t.Fatalf("decided=%v", decided)
	}
	stale := h.call(MethodWorkflowDecide, WorkflowDecideParams{
		AgentID: agentID, ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
		Decisions: []WorkflowDecision{{ItemID: "prepare", Decision: "approve"}},
	})
	rpcErr, ok := rpcErrorOf(stale)
	if !ok || rpcErr.Code != CodeWorkflowConflict {
		t.Fatalf("stale decision=%v", stale)
	}
	authoritative := resultOf(t, h.callOK(MethodWorkflowGet, WorkflowGetParams{AgentID: agentID}))
	current = authoritative["artifacts"].([]any)[0].(map[string]any)
	if current["revision"] != float64(2) {
		t.Fatalf("authoritative=%v", authoritative)
	}

	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})
	reopened := resultOf(t, h.callOK(MethodWorkflowGet, WorkflowGetParams{AgentID: agentID}))
	current = reopened["artifacts"].([]any)[0].(map[string]any)
	if current["revision"] != float64(2) {
		t.Fatalf("reopened=%v", reopened)
	}
}
