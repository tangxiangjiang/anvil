package anvil

import (
	"context"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestHostReconcile(t *testing.T) {
	dir := t.TempDir()
	backend := store.New(dir)
	agentID := "agent-lost"
	if err := store.AppendRunState(backend, agentID, store.Record{
		RunID: "run-1", RunState: "running", HostID: "host-old",
		LeaseUntil: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskState(backend, agentID, store.Record{
		TaskID: "task-1", TaskSeq: 1, TaskState: "running", HostID: "host-old",
		LeaseUntil: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	refs, err := ReconcileRuns(context.Background(), ReconcileOptions{
		Local: LocalOptions{CWD: dir}, HostID: "host-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].AgentID != agentID || refs[0].RunID != "run-1" {
		t.Fatalf("interrupted refs=%+v", refs)
	}
	recs, err := backend.Load(agentID)
	if err != nil {
		t.Fatal(err)
	}
	var runInterrupted, taskInterrupted bool
	for _, rec := range recs {
		runInterrupted = runInterrupted || rec.Type == store.TypeRunState && rec.RunState == "interrupted"
		taskInterrupted = taskInterrupted || rec.Type == store.TypeTaskState && rec.TaskState == "interrupted"
	}
	if !runInterrupted || !taskInterrupted {
		t.Fatalf("run interrupted=%v task interrupted=%v", runInterrupted, taskInterrupted)
	}
}

func TestRunLeaseReconcileAPISkipsLiveLock(t *testing.T) {
	dir := t.TempDir()
	backend := store.New(dir)
	agentID := "agent-live"
	if err := store.AppendRunState(backend, agentID, store.Record{
		RunID: "run-1", RunState: "running", HostID: "host-live",
		LeaseUntil: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := backend.TryLock(agentID)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := ReconcileHost(context.Background(), ReconcileOptions{
		Local: LocalOptions{CWD: dir}, HostID: "host-new",
	}); err != nil {
		t.Fatal(err)
	}
	recs, err := backend.Load(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].RunState != "running" {
		t.Fatalf("live locked session was reconciled: %+v", recs)
	}
}
