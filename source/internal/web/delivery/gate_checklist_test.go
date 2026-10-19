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

func TestParseAndSetGateChecklist(t *testing.T) {
	gate := "Entry notes\n- [ ] lint clean\n- [x] unit tests\n* [ ] human review\n"
	items := ParseGateChecklist(gate)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	if items[0].Done || items[0].Text != "lint clean" {
		t.Fatalf("item0: %+v", items[0])
	}
	if !items[1].Done || items[1].Text != "unit tests" {
		t.Fatalf("item1: %+v", items[1])
	}
	next, err := SetGateChecklistItem(gate, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	open := OpenGateItems(next)
	if len(open) != 1 || open[0].Text != "human review" {
		t.Fatalf("open after toggle: %+v", open)
	}
}

func TestAssertPhaseGateReadyBlocks(t *testing.T) {
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
		Worker:    WorkerRef{ID: "w_g", Label: "g", Fleet: "local", Node: "n1", ProjectRel: "g"},
		Title:     "Gate eng",
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
	gate := "- [ ] must pass before start\n- [x] already ok"
	arch, class := "a", "c"
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
	ms, err := s.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms.Phases) == 0 {
		t.Fatal("expected phase")
	}
	phID := ms.Phases[0].ID
	err = s.AssertPhaseGateReady(msID, phID)
	if err == nil || !strings.Contains(err.Error(), "gate checklist incomplete") {
		t.Fatalf("want gate block, got %v", err)
	}
	if _, err := s.ToggleDesignVersionGateCheck(d.ID, v1.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AssertPhaseGateReady(msID, phID); err != nil {
		t.Fatal(err)
	}
	if err := s.AssertCampaignGatesReady(eng.ID, maj, min); err != nil {
		t.Fatal(err)
	}
}
