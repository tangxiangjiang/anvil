// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"os"
	"os/exec"
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

func TestMergeMilestoneWithLocalGit(t *testing.T) {
	s, msID, _, _, _ := setupFailAcceptFixture(t)
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{PassTest: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchMilestone(msID, PatchMilestoneInput{AcceptMilestone: true}); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	repo := filepath.Join(root, "fa")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Anvil Test")
	runGit(t, repo, "config", "user.email", "anvil@example.invalid")
	runGit(t, repo, "checkout", "-B", "main")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "base.txt")
	runGit(t, repo, "commit", "-m", "base")

	branch := MilestoneBranchName(msID)
	runGit(t, repo, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-m", "feature")

	m, err := s.MergeMilestone(msID, MergeMilestoneInput{MergeRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if m.MergeStatus != MergeMerged || m.MergedAt == "" {
		t.Fatalf("want real merge recorded, got %+v", m)
	}
	if got := strings.TrimSpace(runGit(t, repo, "branch", "--show-current")); got != "main" {
		t.Fatalf("current branch = %q, want main", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("merged feature missing: %v", err)
	}
	parents := strings.Fields(runGit(t, repo, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents) != 3 {
		t.Fatalf("merge commit has %d parents, want 2: %v", len(parents)-1, parents)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
