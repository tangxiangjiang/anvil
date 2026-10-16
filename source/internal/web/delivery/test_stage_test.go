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

func bootstrapExecMilestone(t *testing.T, s *Store) (engID, msID string, maj, min int) {
	t.Helper()
	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker:    WorkerRef{ID: "w_test", Label: "test", Fleet: "local", Node: "n1", ProjectRel: "test"},
		Title:     "Test eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min = eng.Versions[0].Major, eng.Versions[0].Minor
	d, err := s.CreateDesign(CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "Test design",
	})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "Phase A"})
	if err != nil {
		t.Fatal(err)
	}
	gate, arch, class := "Gate", "arch", "class"
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
	msID = fmt.Sprintf("ms_%s_%d_%d", eng.ID, maj, min)
	wid := "w_checkout"
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMilestoneLifecycle(eng.ID, maj, min, LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true}); err != nil {
		t.Fatal(err)
	}
	return eng.ID, msID, maj, min
}

func TestSubmitPassFailTest(t *testing.T) {
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

	engID, msID, maj, min := bootstrapExecMilestone(t, s)

	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{SubmitTest: true}); err != nil {
		t.Fatal(err)
	}
	m, err := s.getMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != LifeTesting {
		t.Fatalf("after submit want %s, got %s", LifeTesting, m.Status)
	}

	// Pass blocked without clean campaign.
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true}); err == nil {
		t.Fatal("pass without cases should fail")
	}
	if _, err := s.CreateTestCases(CreateTestCasesInput{
		EngReqID: engID, Major: maj, Minor: min,
		Cases: []TestCaseDraft{{Title: "Smoke", Body: "happy path"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunTestCases(engID, maj, min); err != nil {
		t.Fatal(err)
	}

	// Pass from testing after clean single-case run.
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true}); err != nil {
		t.Fatal(err)
	}
	m, err = s.getMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != LifeComplete {
		t.Fatalf("after pass want %s, got %s", LifeComplete, m.Status)
	}

	// Second milestone for fail path.
	eng2, err := s.CreateEngReq(CreateEngReqInput{
		Worker:    WorkerRef{ID: "w_test2", Label: "test2", Fleet: "local", Node: "n1", ProjectRel: "test2"},
		Title:     "Test eng 2",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj2, min2 := eng2.Versions[0].Major, eng2.Versions[0].Minor
	d2, err := s.CreateDesign(CreateDesignInput{
		EngReqID: eng2.ID, Major: maj2, Minor: min2, Title: "Test design 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.AddDesignVersion(d2.ID, AddDesignVersionInput{Title: "Phase B"})
	if err != nil {
		t.Fatal(err)
	}
	gate, arch, class := "G", "a", "c"
	_, err = s.PatchDesignVersion(d2.ID, v2.ID, PatchDesignVersionInput{
		Gate: &gate, SubArchitecture: &arch, ClassDesign: &class,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveDesign(d2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapDesignRoadmap(d2.ID); err != nil {
		t.Fatal(err)
	}
	ms2 := fmt.Sprintf("ms_%s_%d_%d", eng2.ID, maj2, min2)
	wid := "w2"
	if _, err := s.PatchMilestone(ms2, PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMilestoneLifecycle(eng2.ID, maj2, min2, LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchMilestone(ms2, PatchMilestoneInput{Execute: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchMilestone(ms2, PatchMilestoneInput{SubmitTest: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PatchMilestone(ms2, PatchMilestoneInput{FailTest: true}); err == nil {
		t.Fatal("fail without note should error")
	}
	m, err = s.PatchMilestone(ms2, PatchMilestoneInput{
		FailTest:  true,
		FailTitle: "Regress login",
		FailBody:  "Button does nothing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != LifeAwaitingRoadmap {
		t.Fatalf("after fail want %s, got %s", LifeAwaitingRoadmap, m.Status)
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.BuglistItems == nil {
		t.Fatal("snapshot should include buglist_items")
	}
	found := false
	for _, b := range snap.BuglistItems {
		if b.EngReqID == eng2.ID && b.Major == maj2 && b.Minor == min2 && b.Title == "Regress login" {
			found = true
			if b.Status != "open" {
				t.Fatalf("bug status want open, got %s", b.Status)
			}
		}
	}
	if !found {
		t.Fatal("expected buglist item in snapshot")
	}

	// Submit only from in_development.
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{SubmitTest: true}); err == nil {
		t.Fatal("submit from complete should fail")
	}

	_ = engID
	_ = maj
	_ = min
}

func TestSubmitTestRejectsWrongStatus(t *testing.T) {
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
	_, msID, _, _ := bootstrapExecMilestone(t, s)
	note := "smoke ok"
	m, err := s.PatchMilestone(msID, PatchMilestoneInput{TestNote: &note})
	if err != nil {
		t.Fatal(err)
	}
	if m.TestNote != note {
		t.Fatalf("test_note want %q got %q", note, m.TestNote)
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true}); err == nil {
		t.Fatal("pass from in_development should fail")
	}
}
