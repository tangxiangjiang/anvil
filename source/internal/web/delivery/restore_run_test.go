// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestRestoreMilestoneRun(t *testing.T) {
	dir := t.TempDir()
	as, err := auth.Open(filepath.Join(dir, "web.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer as.Close()
	s, err := Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker:    WorkerRef{ID: "w_rr", Label: "rr", Fleet: "local", Node: "n1", ProjectRel: "rr"},
		Title:     "Restore",
		ReqsRel:   "docs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	d, err := s.CreateDesign(CreateDesignInput{EngReqID: eng.ID, Major: maj, Minor: min, Title: "D"})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	gate, arch, class := "g", "a", "c"
	_, err = s.PatchDesignVersion(d.ID, v1.ID, PatchDesignVersionInput{
		Gate: &gate, SubArchitecture: &arch, ClassDesign: &class,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveDesign(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapDesignRoadmap(d.ID); err != nil {
		t.Fatal(err)
	}
	msID := fmt.Sprintf("ms_%s_%d_%d", eng.ID, maj, min)
	wid := "w_rr"
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMilestoneLifecycle(eng.ID, maj, min, LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	prev, err := s.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true}); err != nil {
		t.Fatal(err)
	}
	mid, err := s.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if mid.Status != LifeInDevelopment || mid.RunState != RunRunning {
		t.Fatalf("after execute: %+v", mid)
	}
	phaseID := mid.Phases[0].ID
	if err := s.SetPhaseJob(phaseID, "job_new", JobQueued, "queued"); err != nil {
		t.Fatal(err)
	}
	restored, err := s.RestoreExecuteStart(msID, phaseID, "job_old", ExecutePreviousState{
		Status: prev.Status, RunState: prev.RunState,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored {
		t.Fatal("stale job generation restored lifecycle")
	}
	if err := s.SetPhaseJob(phaseID, "job_old", JobError, "scheduler unavailable"); err != nil {
		t.Fatal(err)
	}
	restored, err = s.RestoreExecuteStart(msID, phaseID, "job_old", ExecutePreviousState{
		Status: prev.Status, RunState: prev.RunState,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("current job generation did not restore lifecycle")
	}
	got, err := s.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != prev.Status || got.RunState != prev.RunState {
		t.Fatalf("restore want status=%s run=%s, got %s / %s", prev.Status, prev.RunState, got.Status, got.RunState)
	}
}
