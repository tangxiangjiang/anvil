// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

// RunEventsResult is one ordered page from a run's durable event journal.
type RunEventsResult struct {
	Events    []Event
	NextSeq   uint64
	Terminal  bool
	Truncated bool
}

// GetRunEvents reads events strictly after afterSeq. Limit <= 0 uses the
// transcript page default. Reads never mutate or reconcile the store.
func GetRunEvents(ctx context.Context, agentID, runID string, afterSeq uint64, limit int, opts ListOptions) (RunEventsResult, error) {
	if err := ctx.Err(); err != nil {
		return RunEventsResult{}, err
	}
	if agentID == "" || runID == "" {
		return RunEventsResult{}, newError("agent id and run id are required", false, nil)
	}
	run, err := GetRun(ctx, agentID, runID, opts)
	if err != nil {
		return RunEventsResult{}, err
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return RunEventsResult{}, err
	}
	backend, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return RunEventsResult{}, newError("open store", false, err)
	}
	defer backend.Close()
	journal, err := backend.LoadRunEvents(agentID, runID)
	if err != nil {
		return RunEventsResult{}, newError("load run events", false, err)
	}
	if limit <= 0 {
		limit = DefaultTranscriptLimit
	}
	out := RunEventsResult{
		Events:   make([]Event, 0, min(limit, len(journal))),
		Terminal: store.TerminalRunState(run.State),
		NextSeq:  afterSeq,
	}
	firstAvailable := uint64(0)
	if len(journal) > 0 {
		firstAvailable = journal[0].Seq
	}
	if firstAvailable > afterSeq+1 {
		out.Truncated = true
	}
	for _, item := range journal {
		if item.Seq <= afterSeq {
			continue
		}
		if len(out.Events) >= limit {
			break
		}
		var event Event
		if err := json.Unmarshal(item.Event, &event); err != nil {
			return RunEventsResult{}, newError("decode run event", false, err)
		}
		event.Seq = item.Seq
		out.Events = append(out.Events, event)
		out.NextSeq = item.Seq
	}
	return out, nil
}

// DurableTaskInfo is a read-only task journal projection. It deliberately has
// no process handle, PID, cancellation function, environment, or output ring.
type DurableTaskInfo struct {
	ID                   string
	Seq                  uint64
	Kind                 string
	Title                string
	State                string
	AgentID              string
	ChildAgentID         string
	ParentRunID          string
	ParentToolCallID     string
	HostID               string
	LeaseUntil           time.Time
	Cause                string
	StartedAt            time.Time
	EndedAt              time.Time
	ExitCode             *int
	OutputFirstOffset    int64
	OutputNextOffset     int64
	WorkerID             string
	WorktreeLabel        string
	WorkspaceFingerprint string
	BaseCommit           string
}

// TaskHistory folds durable task records for one owning agent. It never
// populates the live task manager and therefore grants no control capability.
func TaskHistory(ctx context.Context, agentID, runID string, opts ListOptions) ([]DurableTaskInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return nil, err
	}
	backend, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return nil, newError("open store", false, err)
	}
	defer backend.Close()
	recs, err := backend.Load(agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, newError("agent not found: "+agentID, false, nil)
		}
		return nil, newError("load agent store", false, err)
	}
	latest := make(map[string]store.Record)
	order := make(map[string]time.Time)
	for _, rec := range recs {
		if rec.Type != store.TypeTaskState || rec.TaskID == "" {
			continue
		}
		if runID != "" && rec.ParentRunID != runID {
			continue
		}
		if _, ok := order[rec.TaskID]; !ok {
			order[rec.TaskID] = rec.StartedAt
		}
		if prior, ok := latest[rec.TaskID]; !ok || rec.TaskSeq > prior.TaskSeq {
			latest[rec.TaskID] = rec
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		return order[ids[i]].Before(order[ids[j]])
	})
	out := make([]DurableTaskInfo, 0, len(ids))
	for _, id := range ids {
		rec := latest[id]
		out = append(out, DurableTaskInfo{
			ID: id, Seq: rec.TaskSeq, Kind: rec.TaskKind, Title: rec.TaskTitle,
			State: rec.TaskState, AgentID: rec.AgentID, ChildAgentID: rec.ChildAgentID,
			ParentRunID: rec.ParentRunID, ParentToolCallID: rec.ParentToolCallID,
			HostID: rec.HostID, LeaseUntil: rec.LeaseUntil, Cause: rec.Cause,
			StartedAt: rec.StartedAt, EndedAt: rec.EndedAt, ExitCode: rec.ExitCode,
			OutputFirstOffset: rec.OutputFirstOffset, OutputNextOffset: rec.OutputNextOffset,
			WorkerID: rec.WorkerID, WorktreeLabel: rec.WorktreeLabel,
			WorkspaceFingerprint: rec.WorkspaceFingerprint, BaseCommit: rec.BaseCommit,
		})
	}
	return out, nil
}
