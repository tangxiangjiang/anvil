// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/delivery"
)

func TestPreflightBlocksOpenGate(t *testing.T) {
	dir := t.TempDir()
	as, err := auth.Open(filepath.Join(dir, "web.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer as.Close()
	ds, err := delivery.Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, err := ds.CreateEngReq(delivery.CreateEngReqInput{
		Worker:    delivery.WorkerRef{ID: "w_pf", Label: "pf", Fleet: "local", Node: "n1", ProjectRel: "pf"},
		Title:     "PF",
		ReqsRel:   "docs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	d, err := ds.CreateDesign(delivery.CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "D",
	})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := ds.AddDesignVersion(d.ID, delivery.AddDesignVersionInput{Title: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	gate := "- [ ] must check\n"
	arch, class := "a", "c"
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
	wid := "w_pf"
	if _, err := ds.PatchMilestone(msID, delivery.PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := ds.SetMilestoneLifecycle(eng.ID, maj, min, delivery.LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	srv := &Server{delivery: ds, cfg: Config{}} // no fleets → local stub allowed after gate
	_, err = srv.preflightMilestonePhaseJob(msID, false)
	if err == nil {
		t.Fatal("expected gate preflight failure")
	}
	before, _ := ds.GetMilestone(msID)
	if before.RunState == delivery.RunRunning {
		t.Fatal("preflight must not start running")
	}
}
