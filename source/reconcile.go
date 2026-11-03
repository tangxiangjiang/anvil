// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

// ReconcileOptions selects the workspace store and names the new process
// incarnation that is observing it.
type ReconcileOptions struct {
	Local  LocalOptions
	HostID string
}

// RunRef identifies one run changed by reconciliation.
type RunRef struct {
	AgentID string
	RunID   string
}

// ReconcileHost marks unfinished work from lost process incarnations
// interrupted. It acquires each session lock and skips live owners; it never
// discovers, adopts, or signals an operating-system process.
func ReconcileHost(ctx context.Context, opts ReconcileOptions) error {
	_, err := ReconcileRuns(ctx, opts)
	return err
}

// ReconcileRuns is ReconcileHost with the runs newly classified interrupted.
func ReconcileRuns(ctx context.Context, opts ReconcileOptions) ([]RunRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.HostID) == "" {
		return nil, newError("host id is required", false, nil)
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
	type runKey struct{ agentID, runID string }
	before := make(map[runKey]string)
	ids, err := backend.ListIDs()
	if err != nil {
		return nil, newError("list agent stores", false, err)
	}
	for _, agentID := range ids {
		recs, loadErr := backend.Load(agentID)
		if loadErr != nil {
			return nil, newError("load agent store", false, loadErr)
		}
		runs, foldErr := runsFromRecords(recs)
		if foldErr != nil {
			return nil, foldErr
		}
		for _, run := range runs {
			before[runKey{agentID, run.ID}] = run.State
		}
	}
	if err := store.ReconcileHost(backend, opts.HostID, time.Now().UTC()); err != nil {
		return nil, newError("reconcile interrupted work", false, err)
	}
	var out []RunRef
	for _, agentID := range ids {
		recs, loadErr := backend.Load(agentID)
		if loadErr != nil {
			return nil, newError("load reconciled agent store", false, loadErr)
		}
		runs, foldErr := runsFromRecords(recs)
		if foldErr != nil {
			return nil, foldErr
		}
		for _, run := range runs {
			if run.State == "interrupted" && before[runKey{agentID, run.ID}] != "interrupted" {
				out = append(out, RunRef{AgentID: agentID, RunID: run.ID})
			}
		}
	}
	return out, nil
}
