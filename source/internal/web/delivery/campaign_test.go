// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestTestCampaignLoop(t *testing.T) {
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

	cases, err := s.CreateTestCases(CreateTestCasesInput{
		EngReqID: engID, Major: maj, Minor: min,
		Cases: []TestCaseDraft{
			{Title: "Case A", Body: "pass"},
			{Title: "Case B", Body: "will fail mock"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("want 2 cases, got %d", len(cases))
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.TestCases) < 2 {
		t.Fatal("snapshot should include test_cases")
	}

	run, err := s.RunTestCases(engID, maj, min)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Bugs) < 1 {
		t.Fatal("expected bug from failed case")
	}
	failed := 0
	for _, c := range run.Cases {
		if c.Status == "failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("want 1 failed case, got %d", failed)
	}

	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true}); err == nil {
		t.Fatal("pass with open bugs should fail")
	}

	bugID := run.Bugs[0].ID
	fixed, err := s.FixBuglistItem(bugID)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Status != "verified" {
		t.Fatalf("fix want verified, got %s", fixed.Status)
	}

	reg, err := s.RegressBuglist(engID, maj, min)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	for _, b := range reg.Bugs {
		if b.ID == bugID && b.Status == "closed" {
			closed = true
		}
	}
	if !closed {
		t.Fatal("regress should close verified bug")
	}
	for _, c := range reg.Cases {
		if c.Status != "passed" {
			t.Fatalf("after clean regress case %q want passed, got %s", c.Title, c.Status)
		}
	}

	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true}); err != nil {
		t.Fatal(err)
	}
	m, err := s.getMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != LifeComplete {
		t.Fatalf("want complete, got %s", m.Status)
	}
}
