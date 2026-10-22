// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

type stubJobRunner struct {
	calls atomic.Int32
}

func (s *stubJobRunner) CreateJobStream(ctx context.Context, in schedclient.JobCreate, onEvent func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	s.calls.Add(1)
	if onEvent != nil {
		onEvent(daemon.JobEventBody{JobID: in.JobID, Type: "progress", Text: "stub-run"})
	}
	return daemon.JobDoneBody{JobID: in.JobID, Status: "finished", Result: "ok"}, nil
}

type failingJobRunner struct {
	doneError bool
}

func (f *failingJobRunner) CreateJobStream(context.Context, schedclient.JobCreate, func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	if f.doneError {
		return daemon.JobDoneBody{Status: "error", Error: "worker rejected start"}, nil
	}
	return daemon.JobDoneBody{}, fmt.Errorf("scheduler unavailable")
}

func TestStartPhaseJobsAutoAdvance(t *testing.T) {
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
	v2, err := ds.AddDesignVersion(d.ID, delivery.AddDesignVersionInput{Title: "P2"})
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
	_, err = ds.PatchDesignVersion(d.ID, v2.ID, delivery.PatchDesignVersionInput{
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
		allFinished := len(ms.Phases) == 2
		for _, p := range ms.Phases {
			if p.JobStatus != delivery.JobFinished {
				allFinished = false
				break
			}
		}
		if allFinished {
			if got := stub.calls.Load(); got != 2 {
				t.Fatalf("runner calls = %d, want 2", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("phases did not auto-advance from first phase %s", ph.ID)
}

func TestFirstExecuteJobStartFailureRestoresLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		doneError bool
	}{
		{name: "transport_error"},
		{name: "terminal_error", doneError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ds, msID, _, _, _ := setupCampaignFixture(t)
			if err := ds.RestoreMilestoneRun(msID, delivery.LifeAwaitingDevelopment, delivery.RunIdle); err != nil {
				t.Fatal(err)
			}
			srv.jobs = &failingJobRunner{doneError: tc.doneError}

			_, previous, err := ds.ExecuteMilestone(msID)
			if err != nil {
				t.Fatal(err)
			}
			ph, err := srv.startMilestonePhaseJobWithRollback(msID, false, &previous)
			if err != nil {
				t.Fatal(err)
			}

			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				ms, err := ds.GetMilestone(msID)
				if err != nil {
					t.Fatal(err)
				}
				if ms.Status == delivery.LifeAwaitingDevelopment && ms.RunState == delivery.RunIdle {
					for _, got := range ms.Phases {
						if got.ID == ph.ID && got.JobStatus != delivery.JobError {
							t.Fatalf("failed start should retain job error diagnostics, got %s", got.JobStatus)
						}
					}
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("first job start failure did not restore pre-Execute lifecycle")
		})
	}
}

func TestLaterPhaseJobStartFailureStillPauses(t *testing.T) {
	srv, ds, msID, _, _, _ := setupCampaignFixture(t)
	if err := ds.RestoreMilestoneRun(msID, delivery.LifeInDevelopment, delivery.RunRunning); err != nil {
		t.Fatal(err)
	}
	srv.jobs = &failingJobRunner{}
	if _, err := srv.startMilestonePhaseJob(msID, false); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ms, err := ds.GetMilestone(msID)
		if err != nil {
			t.Fatal(err)
		}
		if ms.RunState == delivery.RunPaused {
			if ms.Status != delivery.LifeInDevelopment {
				t.Fatalf("runtime failure changed lifecycle to %s", ms.Status)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("later phase job start failure did not pause run")
}
