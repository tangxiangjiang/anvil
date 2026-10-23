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

func TestMilestoneExecuteWorkerBind(t *testing.T) {
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
		Worker:    WorkerRef{ID: "w_exec", Label: "exec", Fleet: "local", Node: "n1", ProjectRel: "exec"},
		Title:     "Execute eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor

	d, err := s.CreateDesign(CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "Execute design",
	})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "Phase A"})
	if err != nil {
		t.Fatal(err)
	}
	gate := "Gate A"
	arch := "arch"
	class := "class"
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

	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true}); err == nil {
		t.Fatal("execute without worker should fail")
	}

	wid := "w_checkout"
	m, err := s.PatchMilestone(msID, PatchMilestoneInput{ExecuteWorkerID: &wid})
	if err != nil {
		t.Fatal(err)
	}
	if m.ExecuteWorkerID != wid {
		t.Fatalf("want execute_worker_id %q, got %q", wid, m.ExecuteWorkerID)
	}

	if err := s.SetMilestoneLifecycle(eng.ID, maj, min, LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}

	m, err = s.PatchMilestone(msID, PatchMilestoneInput{Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != LifeInDevelopment {
		t.Fatalf("canvas status want %s, got %s", LifeInDevelopment, m.Status)
	}
	if m.ExecuteWorkerID != wid {
		t.Fatalf("worker should persist, got %q", m.ExecuteWorkerID)
	}

	eng2, err := s.GetEngReq(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range eng2.Versions {
		if v.Major == maj && v.Minor == min {
			found = true
			if v.Status != LifeInDevelopment {
				t.Fatalf("version lifecycle want %s, got %s", LifeInDevelopment, v.Status)
			}
		}
	}
	if !found {
		t.Fatal("expected versions under milestone")
	}

	// Reload via getMilestone (simulates snapshot field).
	m2, err := s.getMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if m2.ExecuteWorkerID != wid {
		t.Fatalf("reload lost worker: %q", m2.ExecuteWorkerID)
	}

	for _, status := range []string{LifeAwaitingDevelopment, LifeInDevelopment} {
		t.Run("allows_"+status, func(t *testing.T) {
			if err := s.RestoreMilestoneRun(msID, status, RunIdle); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true}); err != nil {
				t.Fatalf("execute from %s: %v", status, err)
			}
		})
	}
	for _, status := range []string{
		LifeAwaitingDesign, LifeAwaitingRoadmap, LifeTesting, LifeComplete,
	} {
		t.Run("rejects_"+status, func(t *testing.T) {
			if err := s.RestoreMilestoneRun(msID, status, RunIdle); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true}); err == nil {
				t.Fatalf("execute from %s should fail", status)
			}
			got, err := s.GetMilestone(msID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != status || got.RunState != RunIdle {
				t.Fatalf("rejected execute mutated lifecycle: %s / %s", got.Status, got.RunState)
			}
		})
	}
}

func TestParseMilestoneBind(t *testing.T) {
	b := parseMilestoneBind("ms_eng_abc_1_33")
	if b == nil || b.EngID != "eng_abc" || b.Major != 1 || b.Minor != 33 {
		t.Fatalf("got %+v", b)
	}
	if parseMilestoneBind("ms_ui") != nil {
		t.Fatal("demo ids without major.minor should not bind")
	}
}
