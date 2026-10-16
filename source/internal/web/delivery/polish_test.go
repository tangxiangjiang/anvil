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

func TestMilestoneRunStateAndPolish(t *testing.T) {
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
		Worker:    WorkerRef{ID: "w_pol", Label: "pol", Fleet: "local", Node: "n1", ProjectRel: "pol"},
		Title:     "Polish eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	d, err := s.CreateDesign(CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "Polish design",
	})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "Phase A"})
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

	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true}); err == nil {
		t.Fatal("execute without worker should fail")
	}

	wid := "w_checkout"
	paused := RunPaused
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMilestoneLifecycle(eng.ID, maj, min, LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	m, err := s.PatchMilestone(msID, PatchMilestoneInput{Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.RunState != RunRunning {
		t.Fatalf("want running, got %s", m.RunState)
	}
	m, err = s.PatchMilestone(msID, PatchMilestoneInput{RunState: &paused})
	if err != nil {
		t.Fatal(err)
	}
	if m.RunState != RunPaused {
		t.Fatalf("want paused, got %s", m.RunState)
	}

	if err := s.SetMilestoneLifecycle(eng.ID, maj, min, LifeComplete); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePolish(CreatePolishInput{
		EngReqID: eng.ID, Major: maj, Minor: min,
		Title: "Tighten gates", Body: "Add exit criteria for MVP chat.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "ready" {
		t.Fatalf("status %s", p.Status)
	}
	eng2, err := s.GetEngReq(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range eng2.Versions {
		if v.Major == maj && v.Minor == min && strings.Contains(v.Success, "Tighten gates") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("polish title should appear in Version success")
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Polishes) < 1 {
		t.Fatal("snapshot should include polishes")
	}
	p2, err := s.RunPolish(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Status != "done" {
		t.Fatalf("want done, got %s", p2.Status)
	}
}
