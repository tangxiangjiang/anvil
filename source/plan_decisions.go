// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

// WorkDecision is an explicit human decision. It is never inferred from todo
// progress, model text, or execution state.
type WorkDecision string

const (
	WorkDecisionApprove WorkDecision = "approve"
	WorkDecisionDeny    WorkDecision = "deny"
)

var (
	ErrPlanRevisionConflict = errors.New("plan revision conflict")
	ErrPlanDecisionConflict = errors.New("plan decision conflict")
	ErrPlanExecutionGate    = errors.New("plan execution gate refused")
)

type PlanItemDecision struct {
	ItemID   string       `json:"item_id"`
	Decision WorkDecision `json:"decision"`
	Reason   string       `json:"reason,omitempty"`
}

type PlanDecisionRequest struct {
	ArtifactID       string             `json:"artifact_id"`
	ExpectedRevision int                `json:"expected_revision"`
	Decisions        []PlanItemDecision `json:"decisions"`
}

type PlanDecisionResult struct {
	Artifact         PlanArtifact      `json:"artifact"`
	AcceptedRevision int               `json:"accepted_revision"`
	Changed          bool              `json:"changed"`
	Audit            []WorkStateRecord `json:"audit"`
}

type PlanExecutionRequest struct {
	ArtifactID       string `json:"artifact_id"`
	ItemID           string `json:"item_id"`
	ExpectedRevision int    `json:"expected_revision"`
	ExecutionID      string `json:"execution_id"`
}

type PlanExecutionResult struct {
	Artifact PlanArtifact `json:"artifact"`
	Started  bool         `json:"started"`
}

type PlanExecutionFinish struct {
	ArtifactID       string          `json:"artifact_id"`
	ItemID           string          `json:"item_id"`
	ExpectedRevision int             `json:"expected_revision"`
	ExecutionID      string          `json:"execution_id"`
	State            WorkItemState   `json:"state"`
	Evidence         json.RawMessage `json:"evidence,omitempty"`
	Reason           string          `json:"reason,omitempty"`
}

type PlanExecutionFinishResult struct {
	Artifact PlanArtifact `json:"artifact"`
	Changed  bool         `json:"changed"`
}

// WorkflowProjection is the compact durable read model used by editor clients.
// States retain audit/evidence rows; artifacts carry the current item snapshot.
type WorkflowProjection struct {
	Artifacts          []PlanArtifact    `json:"artifacts"`
	SelectedArtifactID string            `json:"selected_artifact_id,omitempty"`
	States             []WorkStateRecord `json:"states"`
	LastSeq            uint64            `json:"last_seq,omitempty"`
}

const localPlanActor = "local-user"

// GetStoredWorkflow reads one closed or live session without taking a write
// lock. artifactID narrows the result; empty returns every current artifact.
func GetStoredWorkflow(ctx context.Context, agentID, artifactID string, opts ListOptions) (WorkflowProjection, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowProjection{}, err
	}
	if err := validPlanID("agent_id", agentID); err != nil {
		return WorkflowProjection{}, err
	}
	if artifactID != "" {
		if err := validPlanID("artifact_id", artifactID); err != nil {
			return WorkflowProjection{}, err
		}
	}
	backend, err := openPlanBackend(opts)
	if err != nil {
		return WorkflowProjection{}, err
	}
	defer backend.Close()
	records, err := backend.Load(agentID)
	if err != nil {
		return WorkflowProjection{}, err
	}
	return projectWorkflowRecords(records, artifactID)
}

// Workflow returns this live agent's durable read projection. It performs no
// scheduling, approval, cancellation, provider call, or checkpoint restore.
func (a *Agent) Workflow(ctx context.Context, artifactID string) (WorkflowProjection, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowProjection{}, err
	}
	if a == nil || a.store == nil {
		return WorkflowProjection{}, errors.New("agent has no plan store")
	}
	records, err := a.store.Load(a.id)
	if err != nil {
		return WorkflowProjection{}, err
	}
	return projectWorkflowRecords(records, artifactID)
}

// GetStoredPlan returns the latest durable projection without taking a session
// write lock. Reading a plan never starts or changes work.
func GetStoredPlan(ctx context.Context, agentID, artifactID string, opts ListOptions) (PlanArtifact, error) {
	if err := ctx.Err(); err != nil {
		return PlanArtifact{}, err
	}
	if err := validPlanID("agent_id", agentID); err != nil {
		return PlanArtifact{}, err
	}
	if err := validPlanID("artifact_id", artifactID); err != nil {
		return PlanArtifact{}, err
	}
	backend, err := openPlanBackend(opts)
	if err != nil {
		return PlanArtifact{}, err
	}
	defer backend.Close()
	records, err := backend.Load(agentID)
	if err != nil {
		return PlanArtifact{}, err
	}
	artifact, _, err := projectPlanRecords(records, artifactID)
	return artifact, err
}

// DecideStoredPlan mutates a closed stored session. A live Agent owns the
// session lock and must use Agent.DecidePlan instead.
func DecideStoredPlan(ctx context.Context, agentID string, request PlanDecisionRequest, opts ListOptions) (PlanDecisionResult, error) {
	var zero PlanDecisionResult
	backend, lock, err := lockedPlanBackend(ctx, agentID, opts)
	if err != nil {
		return zero, err
	}
	defer backend.Close()
	defer lock.Release()
	return decidePlan(ctx, backend, agentID, localPlanActor, request)
}

// DecidePlan applies one explicit local-user decision batch to this live
// session. The batch is revision guarded and contains no execution side effect.
func (a *Agent) DecidePlan(ctx context.Context, request PlanDecisionRequest) (PlanDecisionResult, error) {
	if a == nil || a.store == nil {
		return PlanDecisionResult{}, errors.New("agent has no plan store")
	}
	return decidePlan(ctx, a.store, a.id, localPlanActor, request)
}

func decidePlan(ctx context.Context, backend store.Backend, agentID, actor string, request PlanDecisionRequest) (PlanDecisionResult, error) {
	var result PlanDecisionResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateDecisionRequest(request, actor); err != nil {
		return result, err
	}
	decisionID, err := planDecisionID(request)
	if err != nil {
		return result, err
	}
	err = backend.UpdateRecords(agentID, func(records []store.Record) ([]store.Record, error) {
		artifact, latestDecisions, err := projectPlanRecords(records, request.ArtifactID)
		if err != nil {
			return nil, err
		}
		if artifact.Revision != request.ExpectedRevision {
			return nil, fmt.Errorf("%w: artifact %s is revision %d, expected %d",
				ErrPlanRevisionConflict, request.ArtifactID, artifact.Revision, request.ExpectedRevision)
		}
		next, audit, changed, err := applyPlanDecisions(
			artifact, latestDecisions, request, decisionID, actor, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		result = PlanDecisionResult{
			Artifact: next, AcceptedRevision: next.Revision, Changed: changed,
			Audit: append([]WorkStateRecord(nil), audit...),
		}
		if !changed {
			return records, nil
		}
		artifactRecord, err := planArtifactRecord(next)
		if err != nil {
			return nil, err
		}
		records = append(records, artifactRecord)
		for _, state := range audit {
			records = append(records, workStateStoreRecord(state))
		}
		return records, nil
	})
	return result, err
}

func validateDecisionRequest(request PlanDecisionRequest, actor string) error {
	if err := validPlanID("artifact_id", request.ArtifactID); err != nil {
		return err
	}
	if request.ExpectedRevision < 1 {
		return errors.New("expected plan revision must be positive")
	}
	if strings.TrimSpace(actor) == "" || strings.ContainsAny(actor, "\r\n") {
		return errors.New("plan decision actor must be one non-empty line")
	}
	if len(request.Decisions) == 0 {
		return errors.New("plan decision batch must contain at least one item")
	}
	seen := make(map[string]struct{}, len(request.Decisions))
	for _, decision := range request.Decisions {
		if err := validPlanID("item_id", decision.ItemID); err != nil {
			return err
		}
		if _, duplicate := seen[decision.ItemID]; duplicate {
			return fmt.Errorf("duplicate plan decision for item %q", decision.ItemID)
		}
		seen[decision.ItemID] = struct{}{}
		switch decision.Decision {
		case WorkDecisionApprove:
		case WorkDecisionDeny:
			if strings.TrimSpace(decision.Reason) == "" {
				return fmt.Errorf("denying item %q requires a reason", decision.ItemID)
			}
		default:
			return fmt.Errorf("invalid work decision %q", decision.Decision)
		}
		if strings.ContainsAny(decision.Reason, "\r\n") {
			return fmt.Errorf("decision reason for item %q must be one line", decision.ItemID)
		}
	}
	return nil
}

func planDecisionID(request PlanDecisionRequest) (string, error) {
	decisions := append([]PlanItemDecision(nil), request.Decisions...)
	for i := range decisions {
		decisions[i].Reason = strings.TrimSpace(decisions[i].Reason)
	}
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].ItemID < decisions[j].ItemID
	})
	payload, err := json.Marshal(struct {
		ArtifactID string             `json:"artifact_id"`
		Decisions  []PlanItemDecision `json:"decisions"`
	}{ArtifactID: request.ArtifactID, Decisions: decisions})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("decision-%x", sum[:16]), nil
}

func applyPlanDecisions(
	artifact PlanArtifact,
	latest map[string]WorkStateRecord,
	request PlanDecisionRequest,
	decisionID string,
	actor string,
	now time.Time,
) (PlanArtifact, []WorkStateRecord, bool, error) {
	next := clonePlanArtifact(artifact)
	index := planItemIndex(next.Items)
	changedItems := make(map[string]PlanItemDecision, len(request.Decisions))
	for _, decision := range request.Decisions {
		i, ok := index[decision.ItemID]
		if !ok {
			return PlanArtifact{}, nil, false, fmt.Errorf("plan item %q does not exist", decision.ItemID)
		}
		item := &next.Items[i]
		want := WorkApprovalApproved
		if decision.Decision == WorkDecisionDeny {
			want = WorkApprovalDenied
		}
		switch {
		case item.Approval == WorkApprovalPending:
			item.Approval = want
			changedItems[item.ID] = decision
		case item.Approval != want:
			return PlanArtifact{}, nil, false, fmt.Errorf("%w: item %s is already %s",
				ErrPlanDecisionConflict, item.ID, item.Approval)
		default:
			last, ok := latest[item.ID]
			if !ok || last.Decision != decision.Decision ||
				last.DecisionID != decisionID || last.Revision != artifact.Revision {
				return PlanArtifact{}, nil, false, fmt.Errorf("%w: item %s already has a different recorded decision",
					ErrPlanDecisionConflict, item.ID)
			}
		}
	}
	if len(changedItems) == 0 {
		return next, nil, false, nil
	}

	before := make(map[string]WorkItemState, len(next.Items))
	for _, item := range next.Items {
		before[item.ID] = item.State
	}
	recomputePlanReadiness(&next)
	next.Revision++
	audit := make([]WorkStateRecord, 0, len(changedItems)+len(next.Items))
	for _, decision := range request.Decisions {
		if _, changed := changedItems[decision.ItemID]; !changed {
			continue
		}
		item := next.Items[index[decision.ItemID]]
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" && item.State == WorkStateBlocked {
			reason = planBlockReason(next, item.ID)
		}
		audit = append(audit, WorkStateRecord{
			ArtifactID: next.ArtifactID, ItemID: item.ID, Revision: next.Revision,
			State: item.State, Actor: actor, Decision: decision.Decision,
			DecisionID: decisionID, Time: now, Reason: reason,
		})
	}
	for _, item := range next.Items {
		if _, direct := changedItems[item.ID]; direct {
			continue
		}
		reason := planBlockReason(next, item.ID)
		denialChanged := false
		for _, decision := range changedItems {
			if decision.Decision == WorkDecisionDeny {
				denialChanged = true
				break
			}
		}
		if before[item.ID] == item.State &&
			!(denialChanged && item.State == WorkStateBlocked && strings.Contains(reason, "was denied")) {
			continue
		}
		audit = append(audit, WorkStateRecord{
			ArtifactID: next.ArtifactID, ItemID: item.ID, Revision: next.Revision,
			State: item.State, Actor: "system:dependencies", Time: now,
			Reason: reason,
		})
	}
	if err := next.Validate(); err != nil {
		return PlanArtifact{}, nil, false, err
	}
	for _, state := range audit {
		if err := state.Validate(); err != nil {
			return PlanArtifact{}, nil, false, err
		}
	}
	return next, audit, true, nil
}

// ClaimStoredPlanExecution is the durable at-most-once worker-dispatch gate for
// a closed session. Callers may start work only when Started is true.
func ClaimStoredPlanExecution(ctx context.Context, agentID string, request PlanExecutionRequest, opts ListOptions) (PlanExecutionResult, error) {
	var zero PlanExecutionResult
	backend, lock, err := lockedPlanBackend(ctx, agentID, opts)
	if err != nil {
		return zero, err
	}
	defer backend.Close()
	defer lock.Release()
	return claimPlanExecution(ctx, backend, agentID, request)
}

func (a *Agent) ClaimPlanExecution(ctx context.Context, request PlanExecutionRequest) (PlanExecutionResult, error) {
	if a == nil || a.store == nil {
		return PlanExecutionResult{}, errors.New("agent has no plan store")
	}
	return claimPlanExecution(ctx, a.store, a.id, request)
}

func claimPlanExecution(ctx context.Context, backend store.Backend, agentID string, request PlanExecutionRequest) (PlanExecutionResult, error) {
	var result PlanExecutionResult
	if err := validateExecutionRequest(request); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	err := backend.UpdateRecords(agentID, func(records []store.Record) ([]store.Record, error) {
		artifact, _, err := projectPlanRecords(records, request.ArtifactID)
		if err != nil {
			return nil, err
		}
		if artifact.Revision != request.ExpectedRevision {
			return nil, fmt.Errorf("%w: artifact %s is revision %d, expected %d",
				ErrPlanRevisionConflict, request.ArtifactID, artifact.Revision, request.ExpectedRevision)
		}
		index := planItemIndex(artifact.Items)
		i, ok := index[request.ItemID]
		if !ok {
			return nil, fmt.Errorf("plan item %q does not exist", request.ItemID)
		}
		item := &artifact.Items[i]
		if planContainsString(item.ExecutionIDs, request.ExecutionID) &&
			(item.State == WorkStateRunning || item.State == WorkStateCompleted ||
				item.State == WorkStateFailed || item.State == WorkStateCancelled) {
			result = PlanExecutionResult{Artifact: artifact, Started: false}
			return records, nil
		}
		if item.Approval != WorkApprovalApproved || item.State != WorkStateReady {
			return nil, fmt.Errorf("%w: item %s is %s/%s",
				ErrPlanExecutionGate, item.ID, item.Approval, item.State)
		}
		for _, dependency := range item.DependsOn {
			dep := artifact.Items[index[dependency]]
			if dep.State != WorkStateCompleted {
				return nil, fmt.Errorf("%w: item %s waits for dependency %s",
					ErrPlanExecutionGate, item.ID, dependency)
			}
		}
		item.State = WorkStateRunning
		item.ExecutionIDs = append(item.ExecutionIDs, request.ExecutionID)
		state := WorkStateRecord{
			ArtifactID: artifact.ArtifactID, ItemID: item.ID, Revision: artifact.Revision,
			ExecutionID: request.ExecutionID, State: WorkStateRunning, Actor: "system:execution",
		}
		records = append(records, workStateStoreRecord(state))
		result = PlanExecutionResult{Artifact: artifact, Started: true}
		return records, nil
	})
	return result, err
}

// FinishStoredPlanExecution records terminal evidence and recomputes dependent
// readiness. It never retries, reviews, or creates a replacement plan.
func FinishStoredPlanExecution(ctx context.Context, agentID string, request PlanExecutionFinish, opts ListOptions) (PlanExecutionFinishResult, error) {
	var zero PlanExecutionFinishResult
	backend, lock, err := lockedPlanBackend(ctx, agentID, opts)
	if err != nil {
		return zero, err
	}
	defer backend.Close()
	defer lock.Release()
	return finishPlanExecution(ctx, backend, agentID, request)
}

func (a *Agent) FinishPlanExecution(ctx context.Context, request PlanExecutionFinish) (PlanExecutionFinishResult, error) {
	if a == nil || a.store == nil {
		return PlanExecutionFinishResult{}, errors.New("agent has no plan store")
	}
	return finishPlanExecution(ctx, a.store, a.id, request)
}

func finishPlanExecution(ctx context.Context, backend store.Backend, agentID string, request PlanExecutionFinish) (PlanExecutionFinishResult, error) {
	var result PlanExecutionFinishResult
	if err := validateExecutionFinish(request); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	err := backend.UpdateRecords(agentID, func(records []store.Record) ([]store.Record, error) {
		artifact, _, err := projectPlanRecords(records, request.ArtifactID)
		if err != nil {
			return nil, err
		}
		if artifact.Revision != request.ExpectedRevision {
			return nil, fmt.Errorf("%w: artifact %s is revision %d, expected %d",
				ErrPlanRevisionConflict, request.ArtifactID, artifact.Revision, request.ExpectedRevision)
		}
		index := planItemIndex(artifact.Items)
		i, ok := index[request.ItemID]
		if !ok {
			return nil, fmt.Errorf("plan item %q does not exist", request.ItemID)
		}
		item := &artifact.Items[i]
		if item.State == request.State && planContainsString(item.ExecutionIDs, request.ExecutionID) {
			last, ok := latestPlanExecutionState(records, request)
			if ok && last.State == request.State &&
				bytes.Equal(last.Evidence, request.Evidence) &&
				last.Reason == strings.TrimSpace(request.Reason) {
				result = PlanExecutionFinishResult{Artifact: artifact, Changed: false}
				return records, nil
			}
			return nil, fmt.Errorf("%w: execution %s already has a different terminal record",
				ErrPlanExecutionGate, request.ExecutionID)
		}
		if item.State != WorkStateRunning || !planContainsString(item.ExecutionIDs, request.ExecutionID) {
			return nil, fmt.Errorf("%w: execution %s is not running item %s",
				ErrPlanExecutionGate, request.ExecutionID, request.ItemID)
		}
		item.State = request.State
		recomputePlanReadiness(&artifact)
		state := WorkStateRecord{
			ArtifactID: artifact.ArtifactID, ItemID: item.ID, Revision: artifact.Revision,
			ExecutionID: request.ExecutionID, State: request.State, Actor: "system:execution",
			Reason: strings.TrimSpace(request.Reason), Evidence: append(json.RawMessage(nil), request.Evidence...),
		}
		records = append(records, workStateStoreRecord(state))
		result = PlanExecutionFinishResult{Artifact: artifact, Changed: true}
		return records, nil
	})
	return result, err
}

func validateExecutionRequest(request PlanExecutionRequest) error {
	if err := validPlanID("artifact_id", request.ArtifactID); err != nil {
		return err
	}
	if err := validPlanID("item_id", request.ItemID); err != nil {
		return err
	}
	if err := validPlanID("execution_id", request.ExecutionID); err != nil {
		return err
	}
	if request.ExpectedRevision < 1 {
		return errors.New("expected plan revision must be positive")
	}
	return nil
}

func validateExecutionFinish(request PlanExecutionFinish) error {
	if err := validateExecutionRequest(PlanExecutionRequest{
		ArtifactID: request.ArtifactID, ItemID: request.ItemID,
		ExpectedRevision: request.ExpectedRevision, ExecutionID: request.ExecutionID,
	}); err != nil {
		return err
	}
	switch request.State {
	case WorkStateCompleted:
		if len(request.Evidence) == 0 || !json.Valid(request.Evidence) {
			return errors.New("completed plan execution requires valid evidence")
		}
	case WorkStateFailed, WorkStateCancelled:
		if len(request.Evidence) > 0 && !json.Valid(request.Evidence) {
			return errors.New("plan execution evidence must be valid JSON")
		}
	default:
		return fmt.Errorf("invalid terminal plan state %q", request.State)
	}
	if strings.ContainsAny(request.Reason, "\r\n") {
		return errors.New("plan execution reason must be one line")
	}
	return nil
}

func projectPlanRecords(records []store.Record, artifactID string) (PlanArtifact, map[string]WorkStateRecord, error) {
	var artifact PlanArtifact
	found := false
	for _, rec := range records {
		if rec.Type != store.TypePlanArtifact || rec.ArtifactID != artifactID {
			continue
		}
		next, err := planArtifactFromRecord(rec)
		if err != nil {
			return PlanArtifact{}, nil, err
		}
		if found && next.Revision <= artifact.Revision {
			return PlanArtifact{}, nil, fmt.Errorf("plan artifact %s has non-monotonic revision %d",
				artifactID, next.Revision)
		}
		artifact, found = next, true
	}
	if !found {
		return PlanArtifact{}, nil, fmt.Errorf("plan artifact %q was not found", artifactID)
	}
	latestDecisions := make(map[string]WorkStateRecord)
	index := planItemIndex(artifact.Items)
	for _, rec := range records {
		if rec.Type != store.TypeWorkState || rec.ArtifactID != artifactID {
			continue
		}
		state := workStateFromStoreRecord(rec)
		if err := state.Validate(); err != nil {
			return PlanArtifact{}, nil, err
		}
		if state.Decision != "" {
			latestDecisions[state.ItemID] = state
		}
		if rec.Revision != artifact.Revision {
			continue
		}
		i, ok := index[state.ItemID]
		if !ok {
			return PlanArtifact{}, nil, fmt.Errorf("work state references unknown plan item %q", state.ItemID)
		}
		item := &artifact.Items[i]
		if state.Decision == WorkDecisionApprove {
			item.Approval = WorkApprovalApproved
		} else if state.Decision == WorkDecisionDeny {
			item.Approval = WorkApprovalDenied
		}
		item.State = state.State
		if state.ExecutionID != "" && !planContainsString(item.ExecutionIDs, state.ExecutionID) {
			item.ExecutionIDs = append(item.ExecutionIDs, state.ExecutionID)
		}
	}
	recomputePlanReadiness(&artifact)
	if err := artifact.Validate(); err != nil {
		return PlanArtifact{}, nil, err
	}
	return artifact, latestDecisions, nil
}

func projectWorkflowRecords(records []store.Record, selected string) (WorkflowProjection, error) {
	out := WorkflowProjection{Artifacts: []PlanArtifact{}, States: []WorkStateRecord{}, LastSeq: uint64(len(records))}
	var ids []string
	seen := make(map[string]bool)
	for _, rec := range records {
		if rec.Type != store.TypePlanArtifact || (selected != "" && rec.ArtifactID != selected) {
			continue
		}
		if !seen[rec.ArtifactID] {
			seen[rec.ArtifactID] = true
			ids = append(ids, rec.ArtifactID)
		}
	}
	for _, artifactID := range ids {
		artifact, _, err := projectPlanRecords(records, artifactID)
		if err != nil {
			return WorkflowProjection{}, err
		}
		out.Artifacts = append(out.Artifacts, artifact)
		out.SelectedArtifactID = artifact.ArtifactID
	}
	if selected != "" && len(out.Artifacts) == 0 {
		return WorkflowProjection{}, fmt.Errorf("plan artifact %q was not found", selected)
	}
	for _, rec := range records {
		if rec.Type != store.TypeWorkState || !seen[rec.ArtifactID] {
			continue
		}
		state := workStateFromStoreRecord(rec)
		state.Time = rec.Time
		if err := state.Validate(); err != nil {
			return WorkflowProjection{}, err
		}
		out.States = append(out.States, state)
	}
	return out, nil
}

func recomputePlanReadiness(artifact *PlanArtifact) {
	index := planItemIndex(artifact.Items)
	for i := range artifact.Items {
		item := &artifact.Items[i]
		switch {
		case item.Approval == WorkApprovalDenied:
			item.State = WorkStateBlocked
		case item.Approval == WorkApprovalPending:
			item.State = WorkStateProposed
		case item.State == WorkStateRunning || item.State == WorkStateCompleted ||
			item.State == WorkStateFailed || item.State == WorkStateCancelled:
			continue
		case deniedDependencyRoot(artifact.Items, index, item.ID, map[string]bool{}) != "":
			item.State = WorkStateBlocked
		default:
			item.State = WorkStateReady
			for _, dependency := range item.DependsOn {
				if artifact.Items[index[dependency]].State != WorkStateCompleted {
					item.State = WorkStateBlocked
					break
				}
			}
		}
	}
}

func planBlockReason(artifact PlanArtifact, itemID string) string {
	index := planItemIndex(artifact.Items)
	item, ok := index[itemID]
	if !ok {
		return ""
	}
	if root := deniedDependencyRoot(artifact.Items, index, itemID, map[string]bool{}); root != "" {
		return fmt.Sprintf("dependency %q was denied", root)
	}
	for _, dependency := range artifact.Items[item].DependsOn {
		if artifact.Items[index[dependency]].State != WorkStateCompleted {
			return fmt.Sprintf("waiting for dependency %q", dependency)
		}
	}
	return ""
}

func deniedDependencyRoot(items []WorkItem, index map[string]int, itemID string, visiting map[string]bool) string {
	if visiting[itemID] {
		return ""
	}
	visiting[itemID] = true
	defer delete(visiting, itemID)
	for _, dependency := range items[index[itemID]].DependsOn {
		dep := items[index[dependency]]
		if dep.Approval == WorkApprovalDenied {
			return dep.ID
		}
		if root := deniedDependencyRoot(items, index, dep.ID, visiting); root != "" {
			return root
		}
	}
	return ""
}

func planItemIndex(items []WorkItem) map[string]int {
	index := make(map[string]int, len(items))
	for i := range items {
		index[items[i].ID] = i
	}
	return index
}

func clonePlanArtifact(artifact PlanArtifact) PlanArtifact {
	out := artifact
	out.Items = append([]WorkItem(nil), artifact.Items...)
	for i := range out.Items {
		out.Items[i].Plan = clonePlanStrings(artifact.Items[i].Plan)
		out.Items[i].Files = clonePlanStrings(artifact.Items[i].Files)
		out.Items[i].Acceptance = clonePlanStrings(artifact.Items[i].Acceptance)
		out.Items[i].DependsOn = clonePlanStrings(artifact.Items[i].DependsOn)
		out.Items[i].ExecutionIDs = clonePlanStrings(artifact.Items[i].ExecutionIDs)
	}
	return out
}

func clonePlanStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func planArtifactRecord(artifact PlanArtifact) (store.Record, error) {
	if err := artifact.Validate(); err != nil {
		return store.Record{}, err
	}
	items, err := json.Marshal(artifact.Items)
	if err != nil {
		return store.Record{}, err
	}
	return store.Record{
		Type: store.TypePlanArtifact, Schema: artifact.Schema, ArtifactID: artifact.ArtifactID,
		RunID: artifact.RunID, Kind: string(artifact.Kind), Revision: artifact.Revision,
		Title: artifact.Title, Body: artifact.Body, Items: items,
	}, nil
}

func workStateStoreRecord(state WorkStateRecord) store.Record {
	return store.Record{
		Type: store.TypeWorkState, Time: state.Time, ArtifactID: state.ArtifactID,
		ItemID: state.ItemID, Revision: state.Revision, ExecutionID: state.ExecutionID,
		State: string(state.State), Actor: state.Actor, Decision: string(state.Decision),
		DecisionID: state.DecisionID, Reason: state.Reason,
		Evidence: append(json.RawMessage(nil), state.Evidence...),
	}
}

func workStateFromStoreRecord(rec store.Record) WorkStateRecord {
	state := WorkStateRecord{
		ArtifactID: rec.ArtifactID, ItemID: rec.ItemID, Revision: rec.Revision,
		ExecutionID: rec.ExecutionID, State: WorkItemState(rec.State), Actor: rec.Actor,
		Decision: WorkDecision(rec.Decision), DecisionID: rec.DecisionID, Reason: rec.Reason,
		Evidence: append(json.RawMessage(nil), rec.Evidence...),
	}
	if state.Decision != "" {
		state.Time = rec.Time
	}
	return state
}

func latestPlanExecutionState(records []store.Record, request PlanExecutionFinish) (WorkStateRecord, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.Type == store.TypeWorkState && rec.ArtifactID == request.ArtifactID &&
			rec.ItemID == request.ItemID && rec.Revision == request.ExpectedRevision &&
			rec.ExecutionID == request.ExecutionID {
			return workStateFromStoreRecord(rec), true
		}
	}
	return WorkStateRecord{}, false
}

func planContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func lockedPlanBackend(ctx context.Context, agentID string, opts ListOptions) (store.Backend, *store.SessionLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := validPlanID("agent_id", agentID); err != nil {
		return nil, nil, err
	}
	backend, err := openPlanBackend(opts)
	if err != nil {
		return nil, nil, err
	}
	if !backend.Exists(agentID) {
		backend.Close()
		return nil, nil, newError("open plan session", false, store.ErrNotFound)
	}
	lock, err := backend.TryLock(agentID)
	if err != nil {
		backend.Close()
		return nil, nil, newError("lock plan session", false, err)
	}
	return backend, lock, nil
}
