// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/delivery"
)

func setupCampaignFixture(t *testing.T) (*Server, *delivery.Store, string, string, int, int) {
	t.Helper()
	dir := t.TempDir()
	as, err := auth.Open(filepath.Join(dir, "web.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = as.Close() })
	ds, err := delivery.Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, err := ds.CreateEngReq(delivery.CreateEngReqInput{
		Worker:    delivery.WorkerRef{ID: "w_camp", Label: "camp", Fleet: "local", Node: "n1", ProjectRel: "camp"},
		Title:     "Camp eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	d, err := ds.CreateDesign(delivery.CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "Camp design",
	})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := ds.AddDesignVersion(d.ID, delivery.AddDesignVersionInput{Title: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	gate, arch, class := "G", "a", "c"
	_, err = ds.PatchDesignVersion(d.ID, v1.ID, delivery.PatchDesignVersionInput{
		Gate: &gate, SubArchitecture: &arch, ClassDesign: &class,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ds.ApproveDesign(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.BootstrapDesignRoadmap(d.ID); err != nil {
		t.Fatal(err)
	}
	msID := fmt.Sprintf("ms_%s_%d_%d", eng.ID, maj, min)
	wid := "w_camp"
	if _, err := ds.PatchMilestone(msID, delivery.PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := ds.SetMilestoneLifecycle(eng.ID, maj, min, delivery.LifeTesting); err != nil {
		t.Fatal(err)
	}
	_, err = ds.CreateTestCases(delivery.CreateTestCasesInput{
		EngReqID: eng.ID, Major: maj, Minor: min,
		Cases: []delivery.TestCaseDraft{
			{Title: "Case A", Body: "a"},
			{Title: "Case B", Body: "b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubJobRunner{}
	srv := &Server{delivery: ds, jobs: stub, cfg: Config{}}
	return srv, ds, msID, eng.ID, maj, min
}

func waitCampaignIdle(t *testing.T, ds *delivery.Store, msID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ms, err := ds.GetMilestone(msID)
		if err != nil {
			t.Fatal(err)
		}
		st := ms.CampaignJobStatus
		if st != delivery.JobRunning && st != delivery.JobQueued {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("campaign job did not finish")
}

func TestCampaignRunFixRegressJobs(t *testing.T) {
	srv, ds, msID, engID, maj, min := setupCampaignFixture(t)

	res, err := srv.startTestRunJob(engID, maj, min, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.JobID == "" {
		t.Fatal("expected job id")
	}
	waitCampaignIdle(t, ds, msID)

	cases, err := ds.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var failed int
	for _, c := range cases.TestCases {
		if c.EngReqID == engID && c.Major == maj && c.Minor == min && c.Status == "failed" {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("expected mock run to fail odd-index case")
	}

	var bugID string
	for _, b := range cases.BuglistItems {
		if b.EngReqID == engID && b.Major == maj && b.Minor == min && b.Status == "open" {
			bugID = b.ID
			break
		}
	}
	if bugID == "" {
		t.Fatal("expected open bug")
	}

	fixRes, err := srv.startBugFixJob(bugID, false)
	if err != nil {
		t.Fatal(err)
	}
	if fixRes.JobID == "" {
		t.Fatal("expected fix job id")
	}
	deadline := time.Now().Add(3 * time.Second)
	verified := false
	for time.Now().Before(deadline) {
		b, err := ds.GetBuglistItem(bugID)
		if err != nil {
			t.Fatal(err)
		}
		if b.Status == "verified" {
			verified = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !verified {
		t.Fatal("bug not verified after fix job")
	}

	reg, err := srv.startRegressJob(engID, maj, min, false)
	if err != nil {
		t.Fatal(err)
	}
	if reg.JobID == "" {
		t.Fatal("expected regress job id")
	}
	waitCampaignIdle(t, ds, msID)
	b, err := ds.GetBuglistItem(bugID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "closed" {
		t.Fatalf("expected closed after regress, got %s", b.Status)
	}
}

func TestRealCampaignWithoutTrailerDoesNotPass(t *testing.T) {
	srv, ds, msID, engID, maj, min := setupCampaignFixture(t)
	srv.jobs = nil // Exercise the real-Worker result path, not injected-runner fallback.
	if err := ds.SetCampaignJob(msID, "campaign-real", delivery.JobRunning, delivery.CampaignKindRun, "running"); err != nil {
		t.Fatal(err)
	}

	srv.applyCampaignFinished(
		delivery.CampaignKindRun,
		msID,
		engID,
		maj,
		min,
		"",
		daemon.JobDoneBody{JobID: "campaign-real", Status: "finished", Result: "ordinary logs only"},
		false,
	)

	snap, err := ds.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range snap.TestCases {
		if tc.EngReqID == engID && tc.Major == maj && tc.Minor == min && tc.Status == "passed" {
			t.Fatalf("case %s was incorrectly marked passed without trailer", tc.ID)
		}
	}
	ms, err := ds.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if ms.CampaignJobStatus != delivery.JobError ||
		ms.CampaignJobLastEvent != "campaign result trailer missing" {
		t.Fatalf("missing trailer should be recorded as campaign error: %+v", ms)
	}
}

func TestPolishJob(t *testing.T) {
	srv, ds, msID, engID, maj, min := setupCampaignFixture(t)
	p, err := ds.CreatePolish(delivery.CreatePolishInput{
		EngReqID: engID, Major: maj, Minor: min, Title: "Polish note", Body: "tighten copy",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.startPolishJob(p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.JobID == "" {
		t.Fatal("expected polish job id")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := ds.GetPolish(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == "done" {
			_ = msID
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("polish did not finish")
}
