// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestParseAndApplyCampaignResult(t *testing.T) {
	raw := "some log\nANVIL_CAMPAIGN_RESULT:\n" +
		`{"cases":[{"id":"tc_a","status":"passed"},{"id":"tc_b","status":"failed"}]}` +
		"\ntrailing"
	trail, ok := ParseCampaignResult(raw)
	if !ok || len(trail.Cases) != 2 {
		t.Fatalf("parse: ok=%v %+v", ok, trail)
	}

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
		Worker: WorkerRef{ID: "w_cr", Label: "cr", Fleet: "local", Node: "n1", ProjectRel: "cr"},
		Title:  "CR", ReqsRel: "docs", AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	list, err := s.CreateTestCases(CreateTestCasesInput{
		EngReqID: eng.ID, Major: maj, Minor: min,
		Cases: []TestCaseDraft{{Title: "A", Body: "a"}, {Title: "B", Body: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 {
		t.Fatal("need 2 cases")
	}
	trail.Cases[0].ID = list[0].ID
	trail.Cases[1].ID = list[1].ID
	res, err := s.ApplyCampaignRunResult(eng.ID, maj, min, trail)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Bugs) != 1 {
		t.Fatalf("want 1 bug, got %d", len(res.Bugs))
	}
	got, err := s.listTestCasesForBind(eng.ID, maj, min)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, c := range got {
		byID[c.ID] = c.Status
	}
	if byID[list[0].ID] != "passed" || byID[list[1].ID] != "failed" {
		t.Fatalf("statuses %+v", byID)
	}
}

func TestMergeMilestoneStub(t *testing.T) {
	s, msID, _, _, _ := setupFailAcceptFixture(t)
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeMilestone(msID, MergeMilestoneInput{ForceStub: true}); err == nil {
		t.Fatal("merge before accept should fail")
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{AcceptMilestone: true}); err != nil {
		t.Fatal(err)
	}
	m, err := s.MergeMilestone(msID, MergeMilestoneInput{ForceStub: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.MergeStatus != MergeMerged || m.MergedAt == "" {
		t.Fatalf("want merged, got %+v", m)
	}
	if MilestoneBranchName(msID) != "milestone/"+msID {
		t.Fatal(MilestoneBranchName(msID))
	}
	if !strings.Contains(m.MergeNote, "stub merge") {
		t.Fatalf("merge note: %s", m.MergeNote)
	}
}
