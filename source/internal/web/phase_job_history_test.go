// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"testing"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

// TestPhaseJobEventsArePersisted is the gap this fixes: a phase job started from
// the Roadmap wrote exactly one string (`job_last_event`) and nothing else, so a
// failure could not be diagnosed from the console. The history must hold it.
func TestPhaseJobEventsArePersisted(t *testing.T) {
	runner := newWBRunner()
	runner.events = []daemon.JobEventBody{
		{Type: "progress", Step: 1},
		{Type: "tool_call", Name: "shell"},
		{Type: "assistant", Text: "did the thing"},
	}
	s, _ := wbServer(t, runner)
	close(runner.release) // let the stub finish immediately

	jobID := "phasejob-test"
	s.beginPhaseJob(jobID, "ms_eng_1_2", "ph_1", "w1")
	s.recordJobEvent(jobID, "ph_1", daemon.JobEventBody{Type: "assistant", Text: "one"})
	s.recordJobEvent(jobID, "ph_1", daemon.JobEventBody{Type: "error", Text: "boom", IsError: true})
	if s.delivery == nil {
		t.Fatal("delivery store missing")
	}
	if err := s.delivery.FinishJob(jobID, "error", "boom"); err != nil {
		t.Fatal(err)
	}

	events, err := s.delivery.JobEvents(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("persisted %d events, want 2", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("sequence not monotonic: %+v", events)
	}
	if !events[1].IsError {
		t.Fatal("error flag not persisted")
	}

	jobs, err := s.delivery.ListJobs(delivery.JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var found *delivery.JobRecord
	for i := range jobs {
		if jobs[i].JobID == jobID {
			found = &jobs[i]
		}
	}
	if found == nil {
		t.Fatalf("phase job absent from history: %+v", jobs)
	}
	if found.Kind != "phase" {
		t.Fatalf("kind = %q, want phase", found.Kind)
	}
	if found.MilestoneID != "ms_eng_1_2" || found.PhaseID != "ph_1" {
		t.Fatalf("bind not recorded: %+v", found)
	}
	if found.Status != "error" || found.Error != "boom" {
		t.Fatalf("terminal state not recorded: %+v", found)
	}
}

// TestPhaseJobStreamPersistsThroughRun drives the real streaming path so the
// wiring is proven, not just the helpers.
func TestPhaseJobStreamPersistsThroughRun(t *testing.T) {
	runner := newWBRunner()
	runner.events = []daemon.JobEventBody{
		{Type: "progress", Step: 1},
		{Type: "assistant", Text: "streamed"},
	}
	s, _ := wbServer(t, runner)
	close(runner.release)

	jobID := "phasejob-stream"
	s.beginPhaseJob(jobID, "ms_eng_1_2", "ph_2", "w1")
	s.runPhaseJob("ms_eng_1_2", "ph_2", jobID, schedclient.JobCreate{JobID: jobID})

	events, err := s.delivery.JobEvents(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatalf("stream path persisted %d events, want at least 2", len(events))
	}
	if events[0].Type != "progress" || events[1].Text != "streamed" {
		t.Fatalf("unexpected persisted events: %+v", events)
	}
}

// TestWorkbenchAndPhaseJobsShareOneHistory pins the intent: one list, two entry
// points, so an operator does not learn two vocabularies for the same thing.
func TestWorkbenchAndPhaseJobsShareOneHistory(t *testing.T) {
	s, _ := wbServer(t, newWBRunner())
	if err := s.delivery.BeginJob(delivery.JobRecord{JobID: "wb-1", Kind: "workbench"}); err != nil {
		t.Fatal(err)
	}
	s.beginPhaseJob("ph-1", "ms_1_1", "phase-a", "w1")

	jobs, err := s.delivery.ListJobs(delivery.JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, j := range jobs {
		kinds[j.Kind] = true
	}
	if !kinds["workbench"] || !kinds["phase"] {
		t.Fatalf("history should hold both kinds, got %+v", kinds)
	}
}

var _ = context.Background
