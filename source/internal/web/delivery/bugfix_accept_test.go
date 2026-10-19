// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func setupFailAcceptFixture(t *testing.T) (*Store, string, string, int, int) {
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
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker:    WorkerRef{ID: "w_fa", Label: "fa", Fleet: "local", Node: "n1", ProjectRel: "fa"},
		Title:     "FailAccept eng",
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
	wid := "w_fa"
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMilestoneLifecycle(eng.ID, maj, min, LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{SubmitTest: true}); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTestCases(CreateTestCasesInput{
		EngReqID: eng.ID, Major: maj, Minor: min,
		Cases: []TestCaseDraft{{Title: "T1", Body: "ok"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunTestCasesAllPass(eng.ID, maj, min); err != nil {
		t.Fatal(err)
	}
	return s, msID, eng.ID, maj, min
}

func TestFailSeedsBugfixPhasesAndReady(t *testing.T) {
	s, msID, engID, maj, min := setupFailAcceptFixture(t)
	before, err := s.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	nBefore := len(before.Phases)

	m, err := s.PatchMilestone(msID, PatchMilestoneInput{
		FailTest:  true,
		FailTitle: "Crash on save",
		FailBody:  "nil pointer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != LifeAwaitingRoadmap {
		t.Fatalf("want awaiting_roadmap, got %s", m.Status)
	}
	if len(m.Phases) <= nBefore {
		t.Fatalf("expected bugfix phase added, before=%d after=%d", nBefore, len(m.Phases))
	}
	foundBF := false
	for _, p := range m.Phases {
		if strings.HasPrefix(p.ID, msID+"_bf_") && strings.HasPrefix(p.Title, "Bugfix:") {
			foundBF = true
			if p.Status == "finished" {
				t.Fatal("bugfix phase should be unfinished")
			}
		}
	}
	if !foundBF {
		t.Fatal("expected bugfix phase")
	}

	// Idempotent re-seed via SeedBugfixPhases
	added, err := s.SeedBugfixPhases(msID, engID, maj, min)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("re-seed should add 0, got %d", len(added))
	}

	m2, err := s.PatchMilestone(msID, PatchMilestoneInput{ReadyDev: true})
	if err != nil {
		t.Fatal(err)
	}
	if m2.Status != LifeAwaitingDevelopment {
		t.Fatalf("want awaiting_development, got %s", m2.Status)
	}
}

func TestAcceptMilestone(t *testing.T) {
	s, msID, _, _, _ := setupFailAcceptFixture(t)
	acc := "- [ ] Signed off by owner\n- [x] Docs updated\n"
	_, err := s.db.Exec(`UPDATE delivery_milestones SET acceptance=? WHERE id=?`, acc, msID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != LifeComplete || m.AcceptStatus != AcceptPending {
		t.Fatalf("after pass: status=%s accept=%s", m.Status, m.AcceptStatus)
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{AcceptMilestone: true}); err == nil {
		t.Fatal("accept with open checklist should fail")
	}
	if _, err := s.ToggleMilestoneAcceptanceCheck(msID, 0, true); err != nil {
		t.Fatal(err)
	}
	m2, err := s.PatchMilestone(msID, PatchMilestoneInput{AcceptMilestone: true})
	if err != nil {
		t.Fatal(err)
	}
	if m2.AcceptStatus != AcceptAccepted || m2.AcceptedAt == "" {
		t.Fatalf("want accepted, got %+v", m2)
	}
}
