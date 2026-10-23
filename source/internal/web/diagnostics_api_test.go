// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// TestClassifyErrorCoversTheKnownClasses is the point of p_0.45.11's error work: a
// failure has to name a next step, not just a cause.
func TestClassifyErrorCoversTheKnownClasses(t *testing.T) {
	cases := []struct {
		raw      string
		status   int
		wantCode string
	}{
		{"gate checklist incomplete (V1): do the thing", 409, "gate_incomplete"},
		{"acceptance checklist incomplete: x", 409, "acceptance_incomplete"},
		{"campaign job already running", 409, "already_running"},
		{"worker \"w9\" not found in cluster inventory", 400, "worker_missing"},
		{"no scheduler fleet configured; Workbench needs one reachable fleet", 400, "no_fleet"},
		{"major is sealed", 409, "major_sealed"},
		{"session is already open in another window", 409, "session_busy"},
		{"that milestone is already accepted", 409, "already_accepted"},
		{"pass_test requires status testing (got complete)", 409, "wrong_state"},
		{"", 401, "unauthorized"},
		{"", 403, "forbidden"},
		{"", 500, "server_error"},
	}
	for _, c := range cases {
		got := classifyError(c.raw, c.status)
		if got.Code != c.wantCode {
			t.Errorf("classifyError(%q, %d) = %q, want %q", c.raw, c.status, got.Code, c.wantCode)
			continue
		}
		// Every class must be actionable: a title, a what and a next.
		if strings.TrimSpace(got.Title) == "" || strings.TrimSpace(got.What) == "" || strings.TrimSpace(got.Next) == "" {
			t.Errorf("%q: incomplete action error %+v", c.wantCode, got)
		}
	}
}

// TestClassifyErrorKeepsTheRawMessage: a wrapper that hides the original is worse
// than no wrapper, because it makes an unexpected failure look like a known one.
func TestClassifyErrorKeepsTheRawMessage(t *testing.T) {
	raw := "some entirely unexpected internal thing happened: code 42"
	got := classifyError(raw, 500)
	if got.Raw != raw {
		t.Fatalf("raw message lost: %q", got.Raw)
	}
	// An unrecognised message must not be dressed up as a known cause.
	if got.Code != "unknown" && !strings.Contains(raw, "internal") {
		t.Fatalf("unexpected classification: %+v", got)
	}
	if got.Code == "unknown" && (got.What == "" || got.Next == "") {
		t.Fatalf("unknown class still needs guidance: %+v", got)
	}
}

// TestClassifyErrorPrefersTheSpecificRule proves ordering: a message that matches
// a specific cause must not fall through to the generic one.
func TestClassifyErrorPrefersTheSpecificRule(t *testing.T) {
	got := classifyError("gate checklist incomplete (V1): a; b", 500)
	if got.Code != "gate_incomplete" {
		t.Fatalf("a 500 carrying a gate reason classified as %q; the specific rule must win", got.Code)
	}
}

func TestRequestIDsCorrelateActionableErrors(t *testing.T) {
	s, tok := wbServer(t, nil)
	w := call(t, s, tok, "POST", "/api/v1/diagnostics/classify-error",
		`{"message":"gate checklist incomplete","status":409,"request_id":"req-original"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("classify: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Request-ID"); got == "" {
		t.Fatal("response has no X-Request-ID")
	}
	var out ActionError
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.RequestID != "req-original" {
		t.Fatalf("classification lost original request id: %+v", out)
	}
}

// TestHealthWithNoFleetsIsExplicit: an empty install must say "none configured"
// rather than look healthy.
func TestHealthWithNoFleetsIsExplicit(t *testing.T) {
	s, tok := wbServer(t, nil)
	s.cfg.Fleets = nil
	w := call(t, s, tok, "GET", "/api/v1/health", "")
	if w.Code != 200 {
		t.Fatalf("health: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "No Fleet configured") {
		t.Fatalf("empty install should say so: %s", body)
	}
	if strings.Contains(body, `"ok":true`) {
		t.Fatalf("no fleets must not read as ok: %s", body)
	}
}

func TestActiveJobCountDeduplicatesVisibleRunningWork(t *testing.T) {
	snap := delivery.Snapshot{
		Roadmap: delivery.Roadmap{Milestones: []delivery.Milestone{{
			CampaignJobID: "campaign-1", CampaignJobStatus: delivery.JobRunning,
			Phases: []delivery.Phase{
				{JobID: "phase-1", JobStatus: delivery.JobQueued},
				{JobID: "finished", JobStatus: delivery.JobFinished},
			},
		}}},
		Polishes:     []delivery.Polish{{JobID: "campaign-1", JobStatus: delivery.JobRunning}},
		BuglistItems: []delivery.BuglistItem{{JobID: "fix-1", JobStatus: delivery.JobRunning}},
	}
	if got := activeJobCount(snap); got != 3 {
		t.Fatalf("activeJobCount = %d, want 3", got)
	}
}

// TestHealthReportsUnreachableFleet: one dead fleet is named, with its error, and
// the summary does not claim everything is fine.
func TestHealthReportsUnreachableFleet(t *testing.T) {
	s, tok := wbServer(t, nil)
	s.cfg.Fleets = []FleetEntry{{ID: "dead", SchedulerURL: "http://127.0.0.1:1"}}
	w := call(t, s, tok, "GET", "/api/v1/health", "")
	if w.Code != 200 {
		t.Fatalf("health: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"reachable":false`) {
		t.Fatalf("dead fleet not reported as unreachable: %s", body)
	}
	if !strings.Contains(body, "unreachable") {
		t.Fatalf("summary should say some are unreachable: %s", body)
	}
}

func TestFirstRunUsesAuthoritativeNonDemoState(t *testing.T) {
	s, tok := wbServer(t, nil)
	s.cfg.Fleets = []FleetEntry{{ID: "local", SchedulerURL: "http://127.0.0.1:1"}}
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}

	read := func() FirstRunReport {
		t.Helper()
		w := call(t, s, tok, "GET", "/api/v1/diagnostics/first-run", "")
		if w.Code != 200 {
			t.Fatalf("first run: %d %s", w.Code, w.Body.String())
		}
		var got FirstRunReport
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := read()
	if !got.AdminCreated || !got.FleetConfigured || got.ProjectBound || got.RequirementCreated || got.Completed != 2 {
		t.Fatalf("empty install state = %+v", got)
	}
	if _, err := s.delivery.EnsureProjectEng("project-1", "workspace-1", "P1", "Project one"); err != nil {
		t.Fatal(err)
	}
	got = read()
	if !got.ProjectBound || got.RequirementCreated || got.Completed != 3 {
		t.Fatalf("bound Project state = %+v", got)
	}
	if _, err := s.delivery.CreateEngReq(delivery.CreateEngReqInput{
		ProjectID:      "project-1",
		WorkspaceID:    "workspace-1",
		ProjectKey:     "P1",
		Title:          "First requirement",
		MilestoneTitle: "First requirement",
		Major:          0,
		Minor:          1,
	}); err != nil {
		t.Fatal(err)
	}
	got = read()
	if !got.RequirementCreated || got.Completed != got.Total || got.Total != 4 {
		t.Fatalf("complete state = %+v", got)
	}
}
