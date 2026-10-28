// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"errors"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

// RunInfo is operator-facing metadata for one persisted run (0.10+).
type RunInfo struct {
	ID        string
	Status    string
	Steps     int
	StartedAt time.Time
	EndedAt   time.Time
	Usage     *Usage
}

// ListRuns returns runs for agentID under opts.Local.CWD (from run_start/run_end records).
func ListRuns(ctx context.Context, agentID string, opts ListOptions) ([]RunInfo, error) {
	_ = ctx
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return nil, newError("open store", false, err)
	}
	defer st.Close()
	recs, err := st.Load(agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, newError("agent not found: "+agentID, false, nil)
		}
		return nil, newError("load agent store", false, err)
	}
	return runsFromRecords(recs), nil
}

// GetRun returns one run by id for agentID.
func GetRun(ctx context.Context, agentID, runID string, opts ListOptions) (*RunInfo, error) {
	runs, err := ListRuns(ctx, agentID, opts)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if runs[i].ID == runID {
			r := runs[i]
			return &r, nil
		}
	}
	return nil, newError("run not found: "+runID, false, nil)
}

// ListRuns returns this agent's runs from the local store.
func (a *Agent) ListRuns(ctx context.Context) ([]RunInfo, error) {
	if a == nil {
		return nil, newError("nil agent", false, nil)
	}
	return ListRuns(ctx, a.id, ListOptions{Local: a.opts.Local})
}

func runsFromRecords(recs []store.Record) []RunInfo {
	byID := map[string]*RunInfo{}
	order := []string{}
	for _, rec := range recs {
		switch rec.Type {
		case store.TypeRunStart:
			if rec.RunID == "" {
				continue
			}
			if _, ok := byID[rec.RunID]; !ok {
				order = append(order, rec.RunID)
				byID[rec.RunID] = &RunInfo{ID: rec.RunID, StartedAt: rec.Time}
			} else if byID[rec.RunID].StartedAt.IsZero() {
				byID[rec.RunID].StartedAt = rec.Time
			}
		case store.TypeRunEnd:
			if rec.RunID == "" {
				continue
			}
			info, ok := byID[rec.RunID]
			if !ok {
				order = append(order, rec.RunID)
				info = &RunInfo{ID: rec.RunID}
				byID[rec.RunID] = info
			}
			info.Status = rec.Status
			info.Steps = rec.Steps
			info.EndedAt = rec.Time
			if rec.Usage != nil {
				info.Usage = &Usage{
					InputTokens:      rec.Usage.InputTokens,
					OutputTokens:     rec.Usage.OutputTokens,
					TotalTokens:      rec.Usage.TotalTokens,
					CachedTokens:     rec.Usage.CachedTokens,
					CacheWriteTokens: rec.Usage.CacheWriteTokens,
				}
			}
		}
	}
	out := make([]RunInfo, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}
