// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"fmt"
	"time"
)

const (
	LostHostCause         = "host ownership was lost; process state is unknown"
	LegacyUnfinishedCause = "legacy unfinished run"
)

var ErrTerminalState = errors.New("state is already terminal")

func TerminalRunState(state string) bool {
	switch state {
	case "finished", "error", "cancelled", "interrupted":
		return true
	}
	return false
}

func TerminalTaskState(state string) bool {
	switch state {
	case "finished", "failed", "cancelled", "interrupted":
		return true
	}
	return false
}

// AppendRunState records ownership without permitting a completed run to be
// revived. LeaseUntil is evidence only; it is never a process handle.
func AppendRunState(b Backend, agentID string, rec Record) error {
	if b == nil {
		return errors.New("nil store")
	}
	if rec.RunID == "" || rec.HostID == "" || rec.RunState == "" {
		return errors.New("run state requires run id, host id, and state")
	}
	recs, err := b.Load(agentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Type == TypeRunState && recs[i].RunID == rec.RunID {
			if TerminalRunState(recs[i].RunState) {
				if recs[i].RunState == rec.RunState {
					return nil
				}
				return fmt.Errorf("%w: run %s is %s", ErrTerminalState, rec.RunID, recs[i].RunState)
			}
			break
		}
	}
	rec.Type = TypeRunState
	return b.Append(agentID, rec)
}

// AppendTaskState records only the safe task snapshot. Callers cannot pass
// process-control details because Record has no fields for them.
func AppendTaskState(b Backend, agentID string, rec Record) error {
	if b == nil {
		return errors.New("nil store")
	}
	if rec.TaskID == "" || rec.TaskSeq == 0 || rec.HostID == "" || rec.TaskState == "" {
		return errors.New("task state requires task id, positive sequence, host id, and state")
	}
	recs, err := b.Load(agentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	found := false
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Type == TypeTaskState && recs[i].TaskID == rec.TaskID {
			found = true
			if TerminalTaskState(recs[i].TaskState) {
				if recs[i].TaskState == rec.TaskState && recs[i].TaskSeq == rec.TaskSeq {
					return nil
				}
				return fmt.Errorf("%w: task %s is %s", ErrTerminalState, rec.TaskID, recs[i].TaskState)
			}
			if rec.TaskSeq != recs[i].TaskSeq+1 {
				return fmt.Errorf("task %s got sequence %d, want %d", rec.TaskID, rec.TaskSeq, recs[i].TaskSeq+1)
			}
			break
		}
	}
	if !found && rec.TaskSeq != 1 {
		return fmt.Errorf("task %s got sequence %d, want 1", rec.TaskID, rec.TaskSeq)
	}
	rec.Type = TypeTaskState
	return b.Append(agentID, rec)
}

// ReconcileLostHost monotonically closes non-terminal work whose owner is no
// longer this host (or whose lease expired). It never inspects or controls an
// operating-system process.
func ReconcileLostHost(b Backend, agentID, hostID string, now time.Time) error {
	recs, err := b.Load(agentID)
	if err != nil {
		return err
	}
	runs := map[string]Record{}
	tasks := map[string]Record{}
	started := map[string]Record{}
	ended := map[string]bool{}
	for _, rec := range recs {
		switch rec.Type {
		case TypeRunStart:
			started[rec.RunID] = rec
		case TypeRunEnd:
			ended[rec.RunID] = true
		case TypeRunState:
			runs[rec.RunID] = rec
		case TypeTaskState:
			tasks[rec.TaskID] = rec
		}
	}
	for runID := range started {
		if _, hasState := runs[runID]; hasState || ended[runID] || runID == "" {
			continue
		}
		if err := AppendRunState(b, agentID, Record{
			RunID: runID, RunState: "interrupted", HostID: hostID,
			Cause: LegacyUnfinishedCause,
		}); err != nil {
			return err
		}
	}
	for _, rec := range runs {
		if TerminalRunState(rec.RunState) || (rec.HostID == hostID && rec.LeaseUntil.After(now)) {
			continue
		}
		rec.RunState, rec.Cause, rec.LeaseUntil = "interrupted", LostHostCause, time.Time{}
		rec.HostID, rec.Time = hostID, time.Time{}
		if err := AppendRunState(b, agentID, rec); err != nil {
			return err
		}
	}
	for _, rec := range tasks {
		if TerminalTaskState(rec.TaskState) || (rec.HostID == hostID && rec.LeaseUntil.After(now)) {
			continue
		}
		rec.TaskState, rec.Cause, rec.LeaseUntil = "interrupted", LostHostCause, time.Time{}
		rec.TaskSeq++
		rec.HostID, rec.Time, rec.EndedAt = hostID, time.Time{}, now.UTC()
		if err := AppendTaskState(b, agentID, rec); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileHost scans one selected backend and closes work whose former owner
// can no longer prove liveness. A busy session is skipped: its live holder,
// not this host, retains authority.
func ReconcileHost(b Backend, hostID string, now time.Time) error {
	if b == nil {
		return errors.New("nil store")
	}
	ids, err := b.ListIDs()
	if err != nil {
		return err
	}
	for _, agentID := range ids {
		lock, err := b.TryLock(agentID)
		if errors.Is(err, ErrSessionBusy) {
			continue
		}
		if err != nil {
			return err
		}
		reconcileErr := ReconcileLostHost(b, agentID, hostID, now)
		releaseErr := lock.Release()
		if reconcileErr != nil && !errors.Is(reconcileErr, ErrNotFound) {
			return reconcileErr
		}
		if releaseErr != nil {
			return releaseErr
		}
	}
	return nil
}
