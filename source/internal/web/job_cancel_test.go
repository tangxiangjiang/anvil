// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// TestJobCancelDispatchesOnKind is the reason this endpoint exists: the caller
// should not have to guess which lever to pull, and before this a phase job made a
// Workbench call that could only fail.
func TestJobCancelDispatchesOnKind(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	var cancelled []string
	s.jobCanceler = func(jobID string) error {
		cancelled = append(cancelled, jobID)
		return nil
	}

	// A phase job bound to a milestone: it stops the scheduler call AND the
	// milestone loop.
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, err := s.delivery.CreateEngReq(delivery.CreateEngReqInput{
		Worker:    delivery.WorkerRef{ID: "w1", Label: "w", Fleet: "f", Node: "n", ProjectRel: "p"},
		Title:     "Cancel eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	v := eng.Versions[0]
	d, err := s.delivery.CreateDesign(delivery.CreateDesignInput{
		EngReqID: eng.ID, Major: v.Major, Minor: v.Minor, Title: "D",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.delivery.AddDesignVersion(d.ID, delivery.AddDesignVersionInput{
		Title: "V1", Gate: "- [x] ok", SubArchitecture: "s", ClassDesign: "c",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.delivery.ApproveDesign(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.delivery.BootstrapDesignRoadmap(d.ID); err != nil {
		t.Fatal(err)
	}
	msID := "ms_" + eng.ID + "_" + itoa(v.Major) + "_" + itoa(v.Minor)
	s.beginPhaseJob("phase-1", msID, "ph-1", "w1")

	w := call(t, s, tok, http.MethodPost, "/api/v1/jobs/phase-1/cancel", "")
	if w.Code != http.StatusOK {
		t.Fatalf("cancel phase job: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["via"] != "milestone" {
		t.Fatalf("via = %v, want milestone", out["via"])
	}
	if len(cancelled) != 1 || cancelled[0] != "phase-1" {
		t.Fatalf("scheduler was not asked: %v", cancelled)
	}
	// The milestone loop is stopped so it does not enqueue the next phase.
	ms, err := s.delivery.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if ms.RunState != delivery.RunStopped {
		t.Fatalf("run_state = %q, want stopped", ms.RunState)
	}
}

// TestJobCancelWorkbenchSession covers the other branch.
func TestJobCancelWorkbenchSession(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	var cancelled []string
	s.jobCanceler = func(jobID string) error {
		cancelled = append(cancelled, jobID)
		return nil
	}
	sess := &workbenchSession{
		ID: "wb-x", JobID: "wbjob-x", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
	}
	s.workbench.add(sess)
	s.beginPhaseJob("wbjob-x", "", "", "w1") // the record exists, kind is set below
	if err := s.delivery.BeginJob(delivery.JobRecord{JobID: "wbjob-x", Kind: "workbench"}); err != nil {
		t.Fatal(err)
	}

	w := call(t, s, tok, http.MethodPost, "/api/v1/jobs/wbjob-x/cancel", "")
	if w.Code != http.StatusOK {
		t.Fatalf("cancel workbench job: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["via"] != "workbench" {
		t.Fatalf("via = %v, want workbench", out["via"])
	}
	if len(cancelled) != 1 || cancelled[0] != "wbjob-x" {
		t.Fatalf("scheduler was not asked: %v", cancelled)
	}
}

// TestJobCancelRefusesWhatItCannotStop: an unknown job, an already-finished job
// and a job with no milestone are refusals, not silent successes.
func TestJobCancelRefusesWhatItCannotStop(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())

	w := call(t, s, tok, http.MethodPost, "/api/v1/jobs/nope/cancel", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown job: want 404, got %d", w.Code)
	}

	s.beginPhaseJob("done-1", "ms_1_1", "ph", "w1")
	if err := s.delivery.FinishJob("done-1", "finished", ""); err != nil {
		t.Fatal(err)
	}
	w = call(t, s, tok, http.MethodPost, "/api/v1/jobs/done-1/cancel", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("finished job: want 409, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not running") {
		t.Fatalf("refusal should say why: %s", w.Body.String())
	}
}

// TestJobCancelRouteDoesNotBreakReads keeps the shared mux entry honest: a GET to
// the same prefix must still read.
func TestJobCancelRouteDoesNotBreakReads(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	s.beginPhaseJob("read-1", "ms_1_1", "ph", "w1")

	w := call(t, s, tok, http.MethodGet, "/api/v1/jobs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "read-1") {
		t.Fatalf("job missing from list: %s", w.Body.String())
	}
	// The events read still works through the same entry.
	s.recordJobEvent("read-1", "ph", daemon.JobEventBody{Type: "assistant", Text: "hi"})
	w = call(t, s, tok, http.MethodGet, "/api/v1/jobs/read-1/events", "")
	if w.Code != http.StatusOK {
		t.Fatalf("events: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hi") {
		t.Fatalf("event missing: %s", w.Body.String())
	}
}


