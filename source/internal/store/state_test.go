package store_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

func stateBackends(t *testing.T) []struct {
	name string
	open func() store.Backend
} {
	t.Helper()
	return []struct {
		name string
		open func() store.Backend
	}{
		{name: "jsonl", open: func() store.Backend { return store.New(t.TempDir()) }},
		{name: "sqlite", open: func() store.Backend {
			backend, err := store.Open(t.TempDir(), store.KindSQLite)
			if err != nil {
				t.Fatal(err)
			}
			return backend
		}},
	}
}

func TestTaskPersistenceBoundary(t *testing.T) {
	rec := store.Record{
		Type: store.TypeTaskState, TaskID: "task-1", TaskSeq: 1, TaskKind: "shell",
		TaskState: "running", AgentID: "agent-1", HostID: "host-1",
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"pid", "process_handle", "cancel_func", "environment", "command", "output_ring"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("durable task record contains %q: %s", forbidden, raw)
		}
	}
}

func TestRunLease(t *testing.T) {
	st := store.New(t.TempDir())
	now := time.Now().UTC()
	rec := store.Record{RunID: "run-1", RunState: "running", HostID: "host-a", LeaseUntil: now.Add(time.Minute)}
	if err := store.AppendRunState(st, "agent-1", rec); err != nil {
		t.Fatal(err)
	}
	if err := store.ReconcileLostHost(st, "agent-1", "host-a", now); err != nil {
		t.Fatal(err)
	}
	recs, _ := st.Load("agent-1")
	if len(recs) != 1 || recs[0].RunState != "running" {
		t.Fatalf("fresh owner was reconciled: %+v", recs)
	}
}

func TestLostHost(t *testing.T) {
	st := store.New(t.TempDir())
	agentID := "agent-1"
	if err := store.AppendRunState(st, agentID, store.Record{
		RunID: "run-1", RunState: "running", HostID: "host-old", LeaseUntil: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskState(st, agentID, store.Record{
		TaskID: "task-1", TaskSeq: 1, TaskState: "running", HostID: "host-old", LeaseUntil: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReconcileLostHost(st, agentID, "host-new", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recs, _ := st.Load(agentID)
	var interrupted int
	for _, rec := range recs {
		if (rec.Type == store.TypeRunState && rec.RunState == "interrupted") ||
			(rec.Type == store.TypeTaskState && rec.TaskState == "interrupted") {
			interrupted++
			if rec.Cause != store.LostHostCause {
				t.Fatalf("cause=%q", rec.Cause)
			}
		}
	}
	if interrupted != 2 {
		t.Fatalf("interrupted=%d records=%+v", interrupted, recs)
	}
	if err := store.ReconcileLostHost(st, agentID, "host-new", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	again, _ := st.Load(agentID)
	if len(again) != len(recs) {
		t.Fatalf("reconciliation is not idempotent: %d -> %d", len(recs), len(again))
	}
}

func TestRunStateTerminalMonotonicity(t *testing.T) {
	for _, tc := range stateBackends(t) {
		t.Run(tc.name, func(t *testing.T) {
			backend := tc.open()
			defer backend.Close()
			agentID := "agent-1"
			if err := store.AppendRunState(backend, agentID, store.Record{
				RunID: "run-1", RunState: "finished", HostID: "host-1",
			}); err != nil {
				t.Fatal(err)
			}
			err := store.AppendRunState(backend, agentID, store.Record{
				RunID: "run-1", RunState: "running", HostID: "host-1",
			})
			if !errors.Is(err, store.ErrTerminalState) {
				t.Fatalf("terminal transition error=%v", err)
			}
		})
	}
}

func TestTaskStateSequenceAndTerminalMonotonicity(t *testing.T) {
	for _, tc := range stateBackends(t) {
		t.Run(tc.name, func(t *testing.T) {
			backend := tc.open()
			defer backend.Close()
			agentID := "agent-1"
			first := store.Record{TaskID: "task-1", TaskSeq: 1, TaskState: "running", HostID: "host-1"}
			if err := store.AppendTaskState(backend, agentID, first); err != nil {
				t.Fatal(err)
			}
			gap := first
			gap.TaskSeq = 3
			if err := store.AppendTaskState(backend, agentID, gap); err == nil {
				t.Fatal("task sequence gap was accepted")
			}
			terminal := first
			terminal.TaskSeq, terminal.TaskState = 2, "finished"
			if err := store.AppendTaskState(backend, agentID, terminal); err != nil {
				t.Fatal(err)
			}
			revive := terminal
			revive.TaskSeq, revive.TaskState = 3, "running"
			if err := store.AppendTaskState(backend, agentID, revive); !errors.Is(err, store.ErrTerminalState) {
				t.Fatalf("terminal transition error=%v", err)
			}
		})
	}
}

func TestLostHostSQLite(t *testing.T) {
	backend, err := store.Open(t.TempDir(), store.KindSQLite)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	agentID := "agent-sqlite"
	if err := store.AppendTaskState(backend, agentID, store.Record{
		TaskID: "task-1", TaskSeq: 1, TaskState: "running", HostID: "host-old",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReconcileLostHost(backend, agentID, "host-new", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recs, err := backend.Load(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if got := recs[len(recs)-1]; got.TaskState != "interrupted" || got.TaskSeq != 2 {
		t.Fatalf("last task state=%+v", got)
	}
}

func TestLostHostLegacyUnfinishedRun(t *testing.T) {
	backend := store.New(t.TempDir())
	agentID := "agent-legacy"
	if err := backend.Append(agentID, store.Record{Type: store.TypeRunStart, RunID: "run-old"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReconcileLostHost(backend, agentID, "host-new", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recs, err := backend.Load(agentID)
	if err != nil {
		t.Fatal(err)
	}
	last := recs[len(recs)-1]
	if last.RunState != "interrupted" || last.Cause != store.LegacyUnfinishedCause {
		t.Fatalf("legacy reconciliation=%+v", last)
	}
}
