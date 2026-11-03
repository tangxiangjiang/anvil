// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestPlanPartialApproval(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir, artifact := seedDecisionPlan(t, kind)
			opts := planStoreOptions(dir, kind)
			request := PlanDecisionRequest{
				ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
				Decisions: []PlanItemDecision{{ItemID: "build", Decision: WorkDecisionApprove}},
			}
			result, err := DecideStoredPlan(context.Background(), "agent-decisions", request, opts)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || result.AcceptedRevision != 2 {
				t.Fatalf("result=%+v", result)
			}
			items := decisionItems(result.Artifact)
			if items["prepare"].Approval != WorkApprovalPending ||
				items["build"].Approval != WorkApprovalApproved ||
				items["build"].State != WorkStateBlocked {
				t.Fatalf("items=%+v", items)
			}
			if len(result.Audit) != 1 || result.Audit[0].Decision != WorkDecisionApprove ||
				result.Audit[0].DecisionID == "" || result.Audit[0].Actor != localPlanActor ||
				result.Audit[0].Time.IsZero() {
				t.Fatalf("audit=%+v", result.Audit)
			}
			if _, err := ClaimStoredPlanExecution(context.Background(), "agent-decisions", PlanExecutionRequest{
				ArtifactID: artifact.ArtifactID, ItemID: "build",
				ExpectedRevision: 2, ExecutionID: "run-build",
			}, opts); !errors.Is(err, ErrPlanExecutionGate) {
				t.Fatalf("dependent execution error=%v", err)
			}

			request.ExpectedRevision = 2
			repeated, err := DecideStoredPlan(context.Background(), "agent-decisions", request, opts)
			if err != nil {
				t.Fatal(err)
			}
			if repeated.Changed || repeated.AcceptedRevision != 2 {
				t.Fatalf("idempotent result=%+v", repeated)
			}
			request.Decisions[0].Reason = "different batch"
			if _, err := DecideStoredPlan(
				context.Background(), "agent-decisions", request, opts,
			); !errors.Is(err, ErrPlanDecisionConflict) {
				t.Fatalf("conflicting duplicate error=%v", err)
			}
			states, err := ListWorkStates(context.Background(), "agent-decisions", opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(states) != 1 {
				t.Fatalf("idempotent decision appended %d states", len(states))
			}
		})
	}
}

func TestPlanDependencies(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir, artifact := seedDecisionPlan(t, kind)
			opts := planStoreOptions(dir, kind)
			approved, err := DecideStoredPlan(context.Background(), "agent-decisions", PlanDecisionRequest{
				ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
				Decisions: []PlanItemDecision{
					{ItemID: "build", Decision: WorkDecisionApprove},
					{ItemID: "prepare", Decision: WorkDecisionApprove},
				},
			}, opts)
			if err != nil {
				t.Fatal(err)
			}
			items := decisionItems(approved.Artifact)
			if items["prepare"].State != WorkStateReady || items["build"].State != WorkStateBlocked {
				t.Fatalf("approved items=%+v", items)
			}
			claim := PlanExecutionRequest{
				ArtifactID: artifact.ArtifactID, ItemID: "prepare",
				ExpectedRevision: 2, ExecutionID: "run-prepare",
			}
			started, err := ClaimStoredPlanExecution(context.Background(), "agent-decisions", claim, opts)
			if err != nil || !started.Started {
				t.Fatalf("claim=%+v err=%v", started, err)
			}
			replayed, err := ClaimStoredPlanExecution(context.Background(), "agent-decisions", claim, opts)
			if err != nil || replayed.Started {
				t.Fatalf("replayed claim=%+v err=%v", replayed, err)
			}
			finishRequest := PlanExecutionFinish{
				ArtifactID: artifact.ArtifactID, ItemID: "prepare", ExpectedRevision: 2,
				ExecutionID: "run-prepare", State: WorkStateCompleted,
				Evidence: json.RawMessage(`{"kind":"test","passed":true}`),
			}
			finished, err := FinishStoredPlanExecution(context.Background(), "agent-decisions", finishRequest, opts)
			if err != nil {
				t.Fatal(err)
			}
			items = decisionItems(finished.Artifact)
			if items["prepare"].State != WorkStateCompleted || items["build"].State != WorkStateReady {
				t.Fatalf("finished items=%+v", items)
			}
			repeatedFinish, err := FinishStoredPlanExecution(
				context.Background(), "agent-decisions", finishRequest, opts)
			if err != nil || repeatedFinish.Changed {
				t.Fatalf("repeated finish=%+v err=%v", repeatedFinish, err)
			}
			finishRequest.Evidence = json.RawMessage(`{"kind":"test","passed":false}`)
			if _, err := FinishStoredPlanExecution(
				context.Background(), "agent-decisions", finishRequest, opts); !errors.Is(err, ErrPlanExecutionGate) {
				t.Fatalf("conflicting terminal evidence error=%v", err)
			}
			build, err := ClaimStoredPlanExecution(context.Background(), "agent-decisions", PlanExecutionRequest{
				ArtifactID: artifact.ArtifactID, ItemID: "build",
				ExpectedRevision: 2, ExecutionID: "run-build",
			}, opts)
			if err != nil || !build.Started {
				t.Fatalf("build claim=%+v err=%v", build, err)
			}
		})

		t.Run(kind+"-denial", func(t *testing.T) {
			dir, artifact := seedDecisionPlan(t, kind)
			opts := planStoreOptions(dir, kind)
			if _, err := DecideStoredPlan(context.Background(), "agent-decisions", PlanDecisionRequest{
				ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
				Decisions: []PlanItemDecision{{ItemID: "build", Decision: WorkDecisionApprove}},
			}, opts); err != nil {
				t.Fatal(err)
			}
			denied, err := DecideStoredPlan(context.Background(), "agent-decisions", PlanDecisionRequest{
				ArtifactID: artifact.ArtifactID, ExpectedRevision: 2,
				Decisions: []PlanItemDecision{{
					ItemID: "prepare", Decision: WorkDecisionDeny, Reason: "unsafe boundary",
				}},
			}, opts)
			if err != nil {
				t.Fatal(err)
			}
			items := decisionItems(denied.Artifact)
			if items["prepare"].Approval != WorkApprovalDenied ||
				items["build"].State != WorkStateBlocked {
				t.Fatalf("denied items=%+v", items)
			}
			foundStableReason := false
			for _, state := range denied.Audit {
				if state.ItemID == "build" && state.Reason == `dependency "prepare" was denied` {
					foundStableReason = true
				}
			}
			if !foundStableReason {
				t.Fatalf("denial audit=%+v", denied.Audit)
			}
		})
	}
}

func TestPlanRevision(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir, artifact := seedDecisionPlan(t, kind)
			backend, err := store.Open(dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			requests := []PlanDecisionRequest{
				{
					ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
					Decisions: []PlanItemDecision{{ItemID: "prepare", Decision: WorkDecisionApprove}},
				},
				{
					ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
					Decisions: []PlanItemDecision{{
						ItemID: "prepare", Decision: WorkDecisionDeny, Reason: "not authorized",
					}},
				},
			}
			var wg sync.WaitGroup
			errs := make([]error, len(requests))
			for i := range requests {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, errs[i] = decidePlan(context.Background(), backend, "agent-decisions",
						localPlanActor, requests[i])
				}(i)
			}
			wg.Wait()
			accepted, conflicts := 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					accepted++
				case errors.Is(err, ErrPlanRevisionConflict):
					conflicts++
				default:
					t.Fatalf("unexpected concurrent error: %v", err)
				}
			}
			if accepted != 1 || conflicts != 1 {
				t.Fatalf("accepted=%d conflicts=%d errors=%v", accepted, conflicts, errs)
			}
		})
	}
}

func TestPlanRestart(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir, artifact := seedDecisionPlan(t, kind)
			opts := planStoreOptions(dir, kind)
			if _, err := DecideStoredPlan(context.Background(), "agent-decisions", PlanDecisionRequest{
				ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
				Decisions: []PlanItemDecision{
					{ItemID: "prepare", Decision: WorkDecisionApprove},
					{ItemID: "build", Decision: WorkDecisionApprove},
				},
			}, opts); err != nil {
				t.Fatal(err)
			}
			if _, err := ClaimStoredPlanExecution(context.Background(), "agent-decisions", PlanExecutionRequest{
				ArtifactID: artifact.ArtifactID, ItemID: "prepare",
				ExpectedRevision: 2, ExecutionID: "run-prepare",
			}, opts); err != nil {
				t.Fatal(err)
			}

			// Every stored operation above closed its backend and released the
			// lock. This read/finish pair is a fresh process-equivalent reopen.
			reopened, err := GetStoredPlan(context.Background(), "agent-decisions", artifact.ArtifactID, opts)
			if err != nil {
				t.Fatal(err)
			}
			if decisionItems(reopened)["prepare"].State != WorkStateRunning {
				t.Fatalf("reopened=%+v", reopened)
			}
			if _, err := FinishStoredPlanExecution(context.Background(), "agent-decisions", PlanExecutionFinish{
				ArtifactID: artifact.ArtifactID, ItemID: "prepare", ExpectedRevision: 2,
				ExecutionID: "run-prepare", State: WorkStateCompleted,
				Evidence: json.RawMessage(`{"kind":"run","status":"finished"}`),
			}, opts); err != nil {
				t.Fatal(err)
			}
			reopened, err = GetStoredPlan(context.Background(), "agent-decisions", artifact.ArtifactID, opts)
			if err != nil {
				t.Fatal(err)
			}
			items := decisionItems(reopened)
			if items["prepare"].State != WorkStateCompleted || items["build"].State != WorkStateReady {
				t.Fatalf("restart projection=%+v", items)
			}
		})
	}
}

func TestPlanFailureDoesNotReplan(t *testing.T) {
	dir, artifact := seedDecisionPlan(t, store.KindJSONL)
	opts := planStoreOptions(dir, store.KindJSONL)
	if _, err := DecideStoredPlan(context.Background(), "agent-decisions", PlanDecisionRequest{
		ArtifactID: artifact.ArtifactID, ExpectedRevision: 1,
		Decisions: []PlanItemDecision{
			{ItemID: "prepare", Decision: WorkDecisionApprove},
			{ItemID: "build", Decision: WorkDecisionApprove},
		},
	}, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimStoredPlanExecution(context.Background(), "agent-decisions", PlanExecutionRequest{
		ArtifactID: artifact.ArtifactID, ItemID: "prepare",
		ExpectedRevision: 2, ExecutionID: "run-prepare",
	}, opts); err != nil {
		t.Fatal(err)
	}
	failed, err := FinishStoredPlanExecution(context.Background(), "agent-decisions", PlanExecutionFinish{
		ArtifactID: artifact.ArtifactID, ItemID: "prepare", ExpectedRevision: 2,
		ExecutionID: "run-prepare", State: WorkStateFailed, Reason: "tests failed",
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	items := decisionItems(failed.Artifact)
	if items["prepare"].State != WorkStateFailed || items["build"].State != WorkStateBlocked {
		t.Fatalf("failure projection=%+v", items)
	}
	artifacts, err := ListPlanArtifacts(context.Background(), "agent-decisions", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || artifacts[len(artifacts)-1].Revision != 2 {
		t.Fatalf("failure created or rewrote a plan: %+v", artifacts)
	}
	if _, err := ClaimStoredPlanExecution(context.Background(), "agent-decisions", PlanExecutionRequest{
		ArtifactID: artifact.ArtifactID, ItemID: "build",
		ExpectedRevision: 2, ExecutionID: "run-build",
	}, opts); !errors.Is(err, ErrPlanExecutionGate) {
		t.Fatalf("failed dependency allowed execution: %v", err)
	}
}

func seedDecisionPlan(t *testing.T, kind string) (string, PlanArtifact) {
	t.Helper()
	dir := t.TempDir()
	_, artifact := planFixture(t)
	artifact = clonePlanArtifact(artifact)
	for i := range artifact.Items {
		artifact.Items[i].Approval = WorkApprovalPending
		artifact.Items[i].State = WorkStateProposed
		artifact.Items[i].ExecutionIDs = []string{}
		artifact.Items[i].CheckpointRunID = ""
	}
	backend, err := store.Open(dir, kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Append("agent-decisions", store.Record{
		Type: store.TypeMeta, AgentID: "agent-decisions",
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := planArtifactRecord(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Append("agent-decisions", rec); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, artifact
}

func planStoreOptions(dir, kind string) ListOptions {
	return ListOptions{Local: LocalOptions{CWD: dir, Store: kind}}
}

func decisionItems(artifact PlanArtifact) map[string]WorkItem {
	items := make(map[string]WorkItem, len(artifact.Items))
	for _, item := range artifact.Items {
		items[item.ID] = item
	}
	return items
}
