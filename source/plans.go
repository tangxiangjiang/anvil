// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

const PlanSchemaVersion = 1

type PlanKind string

const (
	PlanKindPlan    PlanKind = "plan"
	PlanKindComplex PlanKind = "complex"
)

type WorkApproval string

const (
	WorkApprovalPending  WorkApproval = "pending"
	WorkApprovalApproved WorkApproval = "approved"
	WorkApprovalDenied   WorkApproval = "denied"
)

type WorkItemState string

const (
	WorkStateProposed  WorkItemState = "proposed"
	WorkStateReady     WorkItemState = "ready"
	WorkStateRunning   WorkItemState = "running"
	WorkStateBlocked   WorkItemState = "blocked"
	WorkStateCompleted WorkItemState = "completed"
	WorkStateFailed    WorkItemState = "failed"
	WorkStateCancelled WorkItemState = "cancelled"
)

type WorkItemSource string

const (
	WorkSourcePlan    WorkItemSource = "plan"
	WorkSourceTodo    WorkItemSource = "todo"
	WorkSourceComplex WorkItemSource = "complex"
)

// WorkItem is one explicitly identified unit in a versioned plan artifact.
type WorkItem struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Goal            string         `json:"goal"`
	Plan            []string       `json:"plan"`
	Files           []string       `json:"files"`
	Acceptance      []string       `json:"acceptance"`
	DependsOn       []string       `json:"depends_on"`
	Approval        WorkApproval   `json:"approval"`
	State           WorkItemState  `json:"state"`
	Source          WorkItemSource `json:"source"`
	ExecutionIDs    []string       `json:"execution_ids"`
	CheckpointRunID string         `json:"checkpoint_run_id,omitempty"`
}

// PlanArtifact is the canonical persisted and wire representation. Schema must
// be exactly PlanSchemaVersion; unknown versions are never coerced.
type PlanArtifact struct {
	Schema     int        `json:"schema"`
	ArtifactID string     `json:"artifact_id"`
	RunID      string     `json:"run_id"`
	Kind       PlanKind   `json:"kind"`
	Revision   int        `json:"revision"`
	Title      string     `json:"title,omitempty"`
	Body       string     `json:"body,omitempty"`
	Items      []WorkItem `json:"items"`
}

// WorkStateRecord is one append-only execution/state observation for an item.
type WorkStateRecord struct {
	ArtifactID  string          `json:"artifact_id"`
	ItemID      string          `json:"item_id"`
	Revision    int             `json:"revision"`
	ExecutionID string          `json:"execution_id,omitempty"`
	State       WorkItemState   `json:"state"`
	Actor       string          `json:"actor,omitempty"`
	Decision    WorkDecision    `json:"decision,omitempty"`
	DecisionID  string          `json:"decision_batch_id,omitempty"`
	Time        time.Time       `json:"timestamp,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Evidence    json.RawMessage `json:"evidence,omitempty"`
}

var planIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// DecodePlanArtifact strictly decodes and validates one canonical artifact.
func DecodePlanArtifact(data []byte) (PlanArtifact, error) {
	var artifact PlanArtifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("decode plan artifact: %w", err)
	}
	if err := requirePlanJSONEOF(decoder); err != nil {
		return PlanArtifact{}, err
	}
	if err := artifact.Validate(); err != nil {
		return PlanArtifact{}, err
	}
	return artifact, nil
}

// EncodePlanArtifact validates before producing the canonical wire/store JSON.
func EncodePlanArtifact(artifact PlanArtifact) ([]byte, error) {
	if err := artifact.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(artifact)
}

func requirePlanJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode plan artifact: trailing JSON value")
		}
		return fmt.Errorf("decode plan artifact: %w", err)
	}
	return nil
}

// Validate checks identity, enums, dependency references and cycles without
// mutating or scheduling any work.
func (artifact PlanArtifact) Validate() error {
	if artifact.Schema != PlanSchemaVersion {
		return fmt.Errorf("unsupported plan schema version %d", artifact.Schema)
	}
	if err := validPlanID("artifact_id", artifact.ArtifactID); err != nil {
		return err
	}
	if err := validPlanID("run_id", artifact.RunID); err != nil {
		return err
	}
	if artifact.Kind != PlanKindPlan && artifact.Kind != PlanKindComplex {
		return fmt.Errorf("invalid plan kind %q", artifact.Kind)
	}
	if artifact.Revision < 1 {
		return errors.New("plan revision must be positive")
	}
	if strings.ContainsAny(artifact.Title, "\r\n") {
		return errors.New("plan title must be one line")
	}
	if artifact.Items == nil {
		return errors.New("plan items must be present (use an empty array for prose-only plans)")
	}
	byID := make(map[string]WorkItem, len(artifact.Items))
	for i, item := range artifact.Items {
		if err := validateWorkItem(item); err != nil {
			return fmt.Errorf("plan item %d: %w", i+1, err)
		}
		if _, exists := byID[item.ID]; exists {
			return fmt.Errorf("duplicate plan item id %q", item.ID)
		}
		byID[item.ID] = item
	}
	for _, item := range artifact.Items {
		for _, dependency := range item.DependsOn {
			if _, exists := byID[dependency]; !exists {
				return fmt.Errorf("plan item %q depends on missing item %q", item.ID, dependency)
			}
		}
	}
	if cycle := dependencyCycle(artifact.Items); len(cycle) > 0 {
		return fmt.Errorf("plan dependency cycle: %s", strings.Join(cycle, " -> "))
	}
	return nil
}

func validateWorkItem(item WorkItem) error {
	if err := validPlanID("id", item.ID); err != nil {
		return err
	}
	if strings.TrimSpace(item.Title) == "" || strings.ContainsAny(item.Title, "\r\n") {
		return errors.New("title must be one non-empty line")
	}
	if strings.TrimSpace(item.Goal) == "" {
		return errors.New("goal is required")
	}
	if err := nonEmptyStrings("plan", item.Plan); err != nil {
		return err
	}
	if len(item.Plan) == 0 {
		return errors.New("plan must contain at least one step")
	}
	if err := nonEmptyStrings("files", item.Files); err != nil {
		return err
	}
	if err := nonEmptyStrings("acceptance", item.Acceptance); err != nil {
		return err
	}
	if len(item.Acceptance) == 0 {
		return errors.New("acceptance must contain at least one check")
	}
	if item.DependsOn == nil {
		return errors.New("depends_on must be present")
	}
	dependencies := make(map[string]struct{}, len(item.DependsOn))
	for _, dependency := range item.DependsOn {
		if err := validPlanID("dependency", dependency); err != nil {
			return err
		}
		if dependency == item.ID {
			return fmt.Errorf("item %q cannot depend on itself", item.ID)
		}
		if _, duplicate := dependencies[dependency]; duplicate {
			return fmt.Errorf("duplicate dependency %q", dependency)
		}
		dependencies[dependency] = struct{}{}
	}
	switch item.Approval {
	case WorkApprovalPending, WorkApprovalApproved, WorkApprovalDenied:
	default:
		return fmt.Errorf("invalid approval %q", item.Approval)
	}
	switch item.State {
	case WorkStateProposed, WorkStateReady, WorkStateRunning, WorkStateBlocked,
		WorkStateCompleted, WorkStateFailed, WorkStateCancelled:
	default:
		return fmt.Errorf("invalid work state %q", item.State)
	}
	if item.Approval == WorkApprovalPending && item.State != WorkStateProposed {
		return errors.New("pending item must remain proposed")
	}
	if item.Approval == WorkApprovalDenied && item.State != WorkStateBlocked {
		return errors.New("denied item must be blocked")
	}
	if item.Approval != WorkApprovalApproved &&
		(item.State == WorkStateReady || item.State == WorkStateRunning ||
			item.State == WorkStateCompleted || item.State == WorkStateFailed ||
			item.State == WorkStateCancelled) {
		return fmt.Errorf("%s item cannot be %s", item.Approval, item.State)
	}
	switch item.Source {
	case WorkSourcePlan, WorkSourceTodo, WorkSourceComplex:
	default:
		return fmt.Errorf("invalid work item source %q", item.Source)
	}
	if item.ExecutionIDs == nil {
		return errors.New("execution_ids must be present")
	}
	executions := make(map[string]struct{}, len(item.ExecutionIDs))
	for _, executionID := range item.ExecutionIDs {
		if err := validPlanID("execution_id", executionID); err != nil {
			return err
		}
		if _, duplicate := executions[executionID]; duplicate {
			return fmt.Errorf("duplicate execution id %q", executionID)
		}
		executions[executionID] = struct{}{}
	}
	if item.CheckpointRunID != "" {
		if err := validPlanID("checkpoint_run_id", item.CheckpointRunID); err != nil {
			return err
		}
	}
	return nil
}

func validPlanID(field, value string) error {
	if !planIDPattern.MatchString(value) {
		return fmt.Errorf("%s %q is not a stable identifier", field, value)
	}
	return nil
}

func nonEmptyStrings(field string, values []string) error {
	if values == nil {
		return fmt.Errorf("%s must be present", field)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s contains an empty value", field)
		}
	}
	return nil
}

func dependencyCycle(items []WorkItem) []string {
	byID := make(map[string]WorkItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	const (
		unseen = iota
		visiting
		visited
	)
	state := make(map[string]int, len(items))
	var stack []string
	var visit func(string) []string
	visit = func(id string) []string {
		state[id] = visiting
		stack = append(stack, id)
		for _, dependency := range byID[id].DependsOn {
			if state[dependency] == visiting {
				for i, node := range stack {
					if node == dependency {
						return append(append([]string(nil), stack[i:]...), dependency)
					}
				}
			}
			if state[dependency] == unseen {
				if cycle := visit(dependency); len(cycle) > 0 {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = visited
		return nil
	}
	for _, item := range items {
		if state[item.ID] == unseen {
			if cycle := visit(item.ID); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

// SavePlanArtifact appends a validated artifact to the selected local backend.
func SavePlanArtifact(ctx context.Context, agentID string, artifact PlanArtifact, opts ListOptions) error {
	if err := artifact.Validate(); err != nil {
		return err
	}
	backend, lock, err := lockedPlanBackend(ctx, agentID, opts)
	if err != nil {
		return err
	}
	defer backend.Close()
	defer lock.Release()
	return savePlanArtifact(backend, agentID, artifact)
}

// SavePlanArtifact persists an artifact through this live session's existing
// ownership claim.
func (a *Agent) SavePlanArtifact(ctx context.Context, artifact PlanArtifact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.store == nil {
		return errors.New("agent has no plan store")
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	return savePlanArtifact(a.store, a.id, artifact)
}

func savePlanArtifact(backend store.Backend, agentID string, artifact PlanArtifact) error {
	record, err := planArtifactRecord(artifact)
	if err != nil {
		return err
	}
	return backend.UpdateRecords(agentID, func(records []store.Record) ([]store.Record, error) {
		latest := 0
		for _, rec := range records {
			if rec.Type != store.TypePlanArtifact || rec.ArtifactID != artifact.ArtifactID {
				continue
			}
			stored, err := planArtifactFromRecord(rec)
			if err != nil {
				return nil, err
			}
			if stored.Revision <= latest {
				return nil, fmt.Errorf("plan artifact %s has non-monotonic revision %d",
					artifact.ArtifactID, stored.Revision)
			}
			latest = stored.Revision
		}
		if artifact.Revision != latest+1 {
			return nil, fmt.Errorf("%w: artifact %s got revision %d, want %d",
				ErrPlanRevisionConflict, artifact.ArtifactID, artifact.Revision, latest+1)
		}
		return append(records, record), nil
	})
}

// ListPlanArtifacts returns validated artifacts in append order.
func ListPlanArtifacts(ctx context.Context, agentID string, opts ListOptions) ([]PlanArtifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend, err := openPlanBackend(opts)
	if err != nil {
		return nil, err
	}
	defer backend.Close()
	records, err := backend.Load(agentID)
	if err != nil {
		return nil, newError("load plan artifacts", false, err)
	}
	var out []PlanArtifact
	for _, rec := range records {
		if rec.Type != store.TypePlanArtifact {
			continue
		}
		artifact, err := planArtifactFromRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, nil
}

// SaveWorkState appends one validated descriptive state record. It does not
// start, approve, deny, cancel, or retry execution.
func SaveWorkState(ctx context.Context, agentID string, state WorkStateRecord, opts ListOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := state.Validate(); err != nil {
		return err
	}
	backend, err := openPlanBackend(opts)
	if err != nil {
		return err
	}
	defer backend.Close()
	if !backend.Exists(agentID) {
		return newError("save work state", false, store.ErrNotFound)
	}
	records, err := backend.Load(agentID)
	if err != nil {
		return newError("load plan artifact for work state", false, err)
	}
	found := false
	for _, rec := range records {
		if rec.Type != store.TypePlanArtifact ||
			rec.ArtifactID != state.ArtifactID || rec.Revision != state.Revision {
			continue
		}
		artifact, err := planArtifactFromRecord(rec)
		if err != nil {
			return err
		}
		for _, item := range artifact.Items {
			if item.ID == state.ItemID {
				found = true
				break
			}
		}
	}
	if !found {
		return fmt.Errorf("work state references unknown artifact item %s/%s at revision %d",
			state.ArtifactID, state.ItemID, state.Revision)
	}
	return backend.Append(agentID, store.Record{
		Type: store.TypeWorkState, ArtifactID: state.ArtifactID, ItemID: state.ItemID,
		Revision: state.Revision, ExecutionID: state.ExecutionID, State: string(state.State),
		Actor: state.Actor, Decision: string(state.Decision), DecisionID: state.DecisionID, Time: state.Time,
		Reason: state.Reason, Evidence: append(json.RawMessage(nil), state.Evidence...),
	})
}

// ListWorkStates returns validated descriptive records in append order.
func ListWorkStates(ctx context.Context, agentID string, opts ListOptions) ([]WorkStateRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend, err := openPlanBackend(opts)
	if err != nil {
		return nil, err
	}
	defer backend.Close()
	records, err := backend.Load(agentID)
	if err != nil {
		return nil, newError("load work states", false, err)
	}
	var out []WorkStateRecord
	for _, rec := range records {
		if rec.Type != store.TypeWorkState {
			continue
		}
		state := WorkStateRecord{
			ArtifactID: rec.ArtifactID, ItemID: rec.ItemID, Revision: rec.Revision,
			ExecutionID: rec.ExecutionID, State: WorkItemState(rec.State),
			Actor: rec.Actor, Decision: WorkDecision(rec.Decision), DecisionID: rec.DecisionID,
			Time: rec.Time, Reason: rec.Reason,
			Evidence: append(json.RawMessage(nil), rec.Evidence...),
		}
		if err := state.Validate(); err != nil {
			return nil, newError("validate work state", false, err)
		}
		out = append(out, state)
	}
	return out, nil
}

func (state WorkStateRecord) Validate() error {
	if err := validPlanID("artifact_id", state.ArtifactID); err != nil {
		return err
	}
	if err := validPlanID("item_id", state.ItemID); err != nil {
		return err
	}
	if state.Revision < 1 {
		return errors.New("work-state revision must be positive")
	}
	if state.ExecutionID != "" {
		if err := validPlanID("execution_id", state.ExecutionID); err != nil {
			return err
		}
	}
	switch state.State {
	case WorkStateProposed, WorkStateReady, WorkStateRunning, WorkStateBlocked,
		WorkStateCompleted, WorkStateFailed, WorkStateCancelled:
	default:
		return fmt.Errorf("invalid work state %q", state.State)
	}
	switch state.Decision {
	case "", WorkDecisionApprove, WorkDecisionDeny:
	default:
		return fmt.Errorf("invalid work decision %q", state.Decision)
	}
	if state.Decision != "" && strings.TrimSpace(state.Actor) == "" {
		return errors.New("work decision actor is required")
	}
	if state.Decision != "" {
		if err := validPlanID("decision_batch_id", state.DecisionID); err != nil {
			return err
		}
	} else if state.DecisionID != "" {
		return errors.New("decision batch id requires a work decision")
	}
	if state.Decision != "" && state.Time.IsZero() {
		return errors.New("work decision timestamp is required")
	}
	if state.Decision == WorkDecisionDeny && strings.TrimSpace(state.Reason) == "" {
		return errors.New("denied work decision reason is required")
	}
	if len(state.Evidence) > 0 && !json.Valid(state.Evidence) {
		return errors.New("work-state evidence must be valid JSON")
	}
	return nil
}

func openPlanBackend(opts ListOptions) (store.Backend, error) {
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return nil, err
	}
	backend, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return nil, newError("open store", false, err)
	}
	return backend, nil
}

func planArtifactFromRecord(rec store.Record) (PlanArtifact, error) {
	artifact := PlanArtifact{
		Schema: rec.Schema, ArtifactID: rec.ArtifactID, RunID: rec.RunID,
		Kind: PlanKind(rec.Kind), Revision: rec.Revision, Title: rec.Title, Body: rec.Body,
	}
	if err := strictDecode(rec.Items, &artifact.Items); err != nil {
		return PlanArtifact{}, newError("decode plan items", false, err)
	}
	if err := artifact.Validate(); err != nil {
		return PlanArtifact{}, newError("validate plan artifact", false, err)
	}
	return artifact, nil
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requirePlanJSONEOF(decoder)
}
