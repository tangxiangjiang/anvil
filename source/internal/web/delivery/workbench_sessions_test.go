// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"strings"
	"testing"
)

func TestWorkbenchResumeOwnershipAndClaim(t *testing.T) {
	s := openTestStore(t)
	if err := s.BeginWorkbenchSession(WorkbenchSessionRecord{
		ID: "wb-1", JobID: "job-1", OwnerID: 7, WorkerID: "worker-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishWorkbenchSession("wb-1", "agent-1", "finished"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimWorkbenchResume("wb-1", 8, "worker-a"); err == nil ||
		!strings.Contains(err.Error(), "scope") {
		t.Fatalf("cross-owner claim error = %v", err)
	}
	if _, err := s.ClaimWorkbenchResume("wb-1", 7, "worker-b"); err == nil {
		t.Fatal("cross-worker resume was accepted")
	}
	agentID, err := s.ClaimWorkbenchResume("wb-1", 7, "worker-a")
	if err != nil || agentID != "agent-1" {
		t.Fatalf("claim = %q, %v", agentID, err)
	}
	if _, err := s.ClaimWorkbenchResume("wb-1", 7, "worker-a"); err == nil {
		t.Fatal("concurrent resume was accepted")
	}
	if err := s.ReleaseWorkbenchResume("wb-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimWorkbenchResume("wb-1", 7, "worker-a"); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
}

func TestFailInterruptedJobsClosesResumeAndHistory(t *testing.T) {
	s := openTestStore(t)
	if err := s.BeginJob(JobRecord{JobID: "job-running", Kind: "workbench"}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginWorkbenchSession(WorkbenchSessionRecord{
		ID: "wb-running", JobID: "job-running", OwnerID: 1, WorkerID: "w",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FailInterruptedJobs(); err != nil {
		t.Fatal(err)
	}
	rec, err := s.WorkbenchSession("wb-running")
	if err != nil || rec.State != "error" {
		t.Fatalf("session after restart = %+v, %v", rec, err)
	}
	jobs, err := s.ListJobs(JobFilter{Status: "error"})
	if err != nil || len(jobs) != 1 || jobs[0].Error != "interrupted by web process restart" {
		t.Fatalf("jobs after restart = %+v, %v", jobs, err)
	}
	if _, err := s.ClaimWorkbenchResume("wb-running", 1, "w"); err == nil {
		t.Fatal("interrupted session without an agent id was resumable")
	}
}
