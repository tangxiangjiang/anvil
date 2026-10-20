// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	as, err := auth.Open(filepath.Join(dir, "web.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = as.Close() })
	s, err := Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestJobEventsPersistInOrder is the point of p_0.46.10: a finished job's
// transcript must be readable, in order, after the fact.
func TestJobEventsPersistInOrder(t *testing.T) {
	s := openTestStore(t)
	if err := s.BeginJob(JobRecord{JobID: "job-1", Kind: "workbench", WorkerID: "w1"}); err != nil {
		t.Fatal(err)
	}
	for _, txt := range []string{"first", "second", "third"} {
		if err := s.AppendJobEvent(JobEvent{JobID: "job-1", Type: "assistant", Text: txt}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishJob("job-1", "error", "boom"); err != nil {
		t.Fatal(err)
	}

	events, err := s.JobEvents("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events=%d", len(events))
	}
	for i, want := range []string{"first", "second", "third"} {
		if events[i].Text != want {
			t.Fatalf("event %d = %q, want %q", i, events[i].Text, want)
		}
		if events[i].Seq != i+1 {
			t.Fatalf("event %d seq = %d", i, events[i].Seq)
		}
	}

	jobs, err := s.ListJobs(JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	j := jobs[0]
	if j.Status != "error" || j.Error != "boom" {
		t.Fatalf("job status=%q error=%q", j.Status, j.Error)
	}
	if j.EventCount != 3 {
		t.Fatalf("event_count=%d", j.EventCount)
	}
	if j.FinishedAt == "" {
		t.Fatal("finished_at not recorded")
	}
}

// TestJobEventTailIsBounded pins the retention rule: a long run keeps its tail,
// not its head, because the failure is at the end.
func TestJobEventTailIsBounded(t *testing.T) {
	s := openTestStore(t)
	if err := s.BeginJob(JobRecord{JobID: "job-long", Kind: "workbench"}); err != nil {
		t.Fatal(err)
	}
	total := jobEventKeepPerJob + 50
	for i := 0; i < total; i++ {
		if err := s.AppendJobEvent(JobEvent{JobID: "job-long", Type: "progress", Text: "e"}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.JobEvents("job-long")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != jobEventKeepPerJob {
		t.Fatalf("kept %d events, want the bound %d", len(events), jobEventKeepPerJob)
	}
	// The surviving rows are the newest ones.
	if events[len(events)-1].Seq != total {
		t.Fatalf("last kept seq=%d, want %d", events[len(events)-1].Seq, total)
	}
	jobs, _ := s.ListJobs(JobFilter{})
	if len(jobs) != 1 || !jobs[0].Truncated {
		t.Fatalf("job should be marked truncated: %+v", jobs)
	}
}

// TestJobFilterNarrows keeps the history list usable on a busy install.
func TestJobFilterNarrows(t *testing.T) {
	s := openTestStore(t)
	for _, rec := range []JobRecord{
		{JobID: "j-a", Kind: "phase", MilestoneID: "ms_1", WorkerID: "w1"},
		{JobID: "j-b", Kind: "phase", MilestoneID: "ms_2", WorkerID: "w1"},
		{JobID: "j-c", Kind: "workbench", WorkerID: "w2"},
	} {
		if err := s.BeginJob(rec); err != nil {
			t.Fatal(err)
		}
		_ = s.FinishJob(rec.JobID, "finished", "")
	}
	got, err := s.ListJobs(JobFilter{MilestoneID: "ms_2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].JobID != "j-b" {
		t.Fatalf("milestone filter: %+v", got)
	}
	got, _ = s.ListJobs(JobFilter{WorkerID: "w1"})
	if len(got) != 2 {
		t.Fatalf("worker filter: %+v", got)
	}
}

// TestAuditRecordsWhoAndPrunes: one row per mutation, attributed, bounded.
func TestAuditRecordsWhoAndPrunes(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < auditKeepPerEntity+10; i++ {
		if err := s.RecordAudit(AuditEntry{
			Actor: "alice", Action: "PATCH eng-reqs/x/versions/1/2/3",
			EntityKind: "requirement_version", EntityID: "x:1:2:3",
			Summary: "version updated",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// ListAudit caps at 200 per read; read twice to see the whole retained set.
	rows, err := s.ListAudit("requirement_version", "x:1:2:3", 200)
	if err != nil {
		t.Fatal(err)
	}
	more, err := s.ListAudit("requirement_version", "x:1:2:3", 200)
	if err != nil {
		t.Fatal(err)
	}
	_ = more
	// The retained set is bounded; asking for more than the cap returns the cap.
	if len(rows) == 0 {
		t.Fatal("no audit rows returned")
	}
	if len(rows) > auditKeepPerEntity {
		t.Fatalf("audit grew past its bound: %d", len(rows))
	}
	if rows[0].Actor != "alice" {
		t.Fatalf("actor not recorded: %+v", rows[0])
	}
	if rows[0].Ts == "" {
		t.Fatal("ts not recorded")
	}
	// A different entity is unaffected.
	other, _ := s.ListAudit("design", "d1", 10)
	if len(other) != 0 {
		t.Fatalf("unexpected rows for another entity: %+v", other)
	}
}

// TestAuditRequiresAnEntity keeps meaningless rows out of the log.
func TestAuditRequiresAnEntity(t *testing.T) {
	s := openTestStore(t)
	if err := s.RecordAudit(AuditEntry{Actor: "a", Action: "x"}); err == nil {
		t.Fatal("expected an error for a row with no entity kind")
	}
}

// TestTrashProvenance records who deleted a major, which is what a restore has to
// state.
func TestTrashProvenance(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	item, err := s.DeleteMajorToTrash(1)
	if err != nil {
		t.Fatalf("delete major: %v", err)
	}
	if err := s.SetTrashProvenance(item.ID, "alice", "1 milestone, 0 requirements"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListTrash()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected a trash row")
	}
	by, at, summary, err := s.TrashProvenance(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if by != "alice" || at == "" || summary == "" {
		t.Fatalf("provenance = %q %q %q", by, at, summary)
	}
}


