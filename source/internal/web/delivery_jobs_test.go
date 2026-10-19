// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

type stubJobRunner struct {
	calls int
}

func (s *stubJobRunner) CreateJobStream(ctx context.Context, in schedclient.JobCreate, onEvent func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	s.calls++
	if onEvent != nil {
		onEvent(daemon.JobEventBody{JobID: in.JobID, Type: "progress", Text: "stub-run"})
	}
	return daemon.JobDoneBody{JobID: in.JobID, Status: "finished", Result: "ok"}, nil
}

func TestStartPhaseJobWithStubRunner(t *testing.T) {
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

	// Reuse delivery package bootstrap via create eng + design path in store tests —
	// minimal: CreateEngReq + design bootstrap copied inline.
	eng, err := ds.CreateEngReq(delivery.CreateEngReqInput{
		Worker:    delivery.WorkerRef{ID: "w_job", Label: "job", Fleet: "local", Node: "n1", ProjectRel: "job"},
		Title:     "Job eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	d, err := ds.CreateDesign(delivery.CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "Job design",
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
	wid := "w_checkout"
	if _, err := ds.PatchMilestone(msID, delivery.PatchMilestoneInput{ExecuteWorkerID: &wid}); err != nil {
		t.Fatal(err)
	}
	if err := ds.SetMilestoneLifecycle(eng.ID, maj, min, delivery.LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.PatchMilestone(msID, delivery.PatchMilestoneInput{Execute: true}); err != nil {
		t.Fatal(err)
	}

	stub := &stubJobRunner{}
	srv := &Server{delivery: ds, jobs: stub, cfg: Config{}}
	ph, err := srv.startMilestonePhaseJob(msID, false)
	if err != nil {
		t.Fatal(err)
	}
	if ph.JobID == "" {
		t.Fatal("expected job id")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ms, err := ds.GetMilestone(msID)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ms.Phases {
			if p.ID == ph.ID && p.JobStatus == delivery.JobFinished {
				if stub.calls < 1 {
					t.Fatal("runner not called")
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("phase did not finish in time")
}
