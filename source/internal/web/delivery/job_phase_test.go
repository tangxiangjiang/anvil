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

func TestPhaseJobFieldsAndPrompt(t *testing.T) {
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

	_, msID, _, _ := bootstrapExecMilestone(t, s)

	ms, err := s.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms.Phases) == 0 {
		t.Fatal("expected phases")
	}
	ph := ms.Phases[0]
	if err := s.SetPhaseJob(ph.ID, "job_1", JobQueued, "queued"); err != nil {
		t.Fatal(err)
	}
	ms, err = s.GetMilestone(msID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ms.Phases {
		if p.ID == ph.ID {
			found = true
			if p.JobID != "job_1" || p.JobStatus != JobQueued {
				t.Fatalf("job fields %#v", p)
			}
		}
	}
	if !found {
		t.Fatal("phase missing after set")
	}

	prompt, err := s.BuildPhasePrompt(msID, ph.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(prompt), "phase") ||
		!strings.Contains(prompt, "Implement") {
		t.Fatalf("prompt too thin: %q", prompt)
	}

	next, err := s.NextRunnablePhase(msID)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != ph.ID {
		t.Fatalf("want active/queued phase %s, got %s", ph.ID, next.ID)
	}

	// Finish all phases.
	for _, p := range ms.Phases {
		_ = s.CompletePhaseJob(p.ID, JobFinished, "finished", "ok")
	}
	if _, err := s.NextRunnablePhase(msID); err == nil {
		t.Fatal("expected no runnable when all finished")
	}
}
