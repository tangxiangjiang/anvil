// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"strings"
	"testing"
)

// preflightFixture builds the smallest real chain: eng req -> design -> versions
// -> approve -> bootstrap. A roadmap milestone only exists after bootstrap, which
// is why the earlier version of these tests could not find one.
type preflightFixture struct {
	s     *Store
	engID string
	major int
	minor int
	msID  string
	desID string
	verID string
}

func newPreflightFixture(t *testing.T, title string) preflightFixture {
	return newPreflightFixtureWithGate(t, title, "- [ ] gate item")
}

func newPreflightFixtureWithGate(t *testing.T, title, gate string) preflightFixture {
	t.Helper()
	s := openTestStore(t)
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker:    WorkerRef{ID: "w1", Label: "w", Fleet: "f", Node: "n", ProjectRel: "p"},
		Title:     title,
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	v := eng.Versions[0]
	d, err := s.CreateDesign(CreateDesignInput{
		EngReqID: eng.ID, Major: v.Major, Minor: v.Minor, Title: title + " design",
	})
	if err != nil {
		t.Fatal(err)
	}
	dv, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{
		Title:           "V1",
		Gate:            gate,
		SubArchitecture: "sub",
		ClassDesign:     "class",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveDesign(d.ID); err != nil {
		t.Fatalf("approve design: %v", err)
	}
	if _, err := s.BootstrapDesignRoadmap(d.ID); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	msID := fmt.Sprintf("ms_%s_%d_%d", eng.ID, v.Major, v.Minor)
	if _, err := s.FindMilestoneByBind(eng.ID, v.Major, v.Minor); err != nil {
		t.Fatalf("bootstrap did not create %s: %v", msID, err)
	}
	return preflightFixture{
		s: s, engID: eng.ID, major: v.Major, minor: v.Minor,
		msID: msID, desID: d.ID, verID: dv.ID,
	}
}

// TestPreflightStartPhaseBlockers covers the reasons a phase cannot start. Each is
// a separate blocker so the button can list them all, not just the first.
func TestPreflightStartPhaseBlockers(t *testing.T) {
	f := newPreflightFixture(t, "Eng")

	res, err := f.s.Preflight(TransitionStartPhase, f.msID)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected blockers, got ok: %+v", res)
	}
	// No worker bound yet.
	if !hasBlocker(res, "no_worker") {
		t.Fatalf("missing no_worker blocker: %+v", res.Blockers)
	}
	// The design version's gate has one open item.
	if !hasBlocker(res, "gate_open") {
		t.Fatalf("missing gate_open blocker: %+v", res.Blockers)
	}
	for _, b := range res.Blockers {
		if strings.TrimSpace(b.Message) == "" {
			t.Fatalf("blocker with no message: %+v", b)
		}
	}
}

// TestPreflightUnknownTransition is a 400 rather than a silent ok, so a typo in
// the client cannot read as "ready".
func TestPreflightUnknownTransition(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Preflight(Transition("nope"), "x"); err == nil {
		t.Fatal("expected an error for an unknown transition")
	}
}

// TestPreflightSubmitTestNeedsInDevelopment pins one transition's state rule.
func TestPreflightSubmitTestNeedsInDevelopment(t *testing.T) {
	f := newPreflightFixture(t, "Eng2")

	res, err := f.s.Preflight(TransitionSubmitTest, f.msID)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !hasBlocker(res, "wrong_state") {
		t.Fatalf("a milestone not in development must be refused: %+v", res)
	}
}

// TestPreflightSealMajorStatesOpenWork: sealing is allowed with work open, but the
// reason list says so — it is the one irreversible move.
func TestPreflightSealMajorStatesOpenWork(t *testing.T) {
	f := newPreflightFixture(t, "Eng3")

	res, err := f.s.Preflight(TransitionSealMajor, fmt.Sprintf("%d", f.major))
	if err != nil {
		t.Fatal(err)
	}
	if !hasBlocker(res, "open_work") {
		t.Fatalf("expected open_work to be reported: %+v", res.Blockers)
	}
	msg := blockerMessage(res, "open_work")
	if !strings.Contains(msg, "milestone") {
		t.Fatalf("open_work message is not specific: %q", msg)
	}
}

// TestPreflightGateBlockerNamesTheItem is the acceptance that matters: the reason a
// button is disabled must point at the specific unchecked item, not say
// "incomplete".
func TestPreflightGateBlockerNamesTheItem(t *testing.T) {
	f := newPreflightFixtureWithGate(t, "Eng4", "- [ ] checkpointing works\n- [x] logging works")

	res, err := f.s.Preflight(TransitionStartPhase, f.msID)
	if err != nil {
		t.Fatal(err)
	}
	msg := blockerMessage(res, "gate_open")
	if msg == "" {
		t.Fatalf("missing gate_open blocker: %+v", res.Blockers)
	}
	if !strings.Contains(msg, "checkpointing") {
		t.Fatalf("gate blocker does not name the item: %q", msg)
	}
	if strings.Contains(msg, "logging") {
		t.Fatalf("gate blocker names a checked item: %q", msg)
	}
}

// TestPreflightPassTestRequiresACleanCampaign covers the accept-side rule.
func TestPreflightPassTestRequiresACleanCampaign(t *testing.T) {
	f := newPreflightFixture(t, "Eng5")

	res, err := f.s.Preflight(TransitionPassTest, f.msID)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected blockers, got ok: %+v", res)
	}
	// Either the state is wrong or the campaign is not clean; both are refusals.
	if !hasBlocker(res, "wrong_state") && !hasBlocker(res, "campaign_not_clean") && !hasBlocker(res, "not_bound") {
		t.Fatalf("unexpected blockers: %+v", res.Blockers)
	}
}

// TestPreflightAndEnforcementAgree is the anti-theatre test: the preview and the
// transition must refuse on the same grounds, because they read the same gate text
// through OpenGateItems.
func TestPreflightAndEnforcementAgree(t *testing.T) {
	f := newPreflightFixtureWithGate(t, "Eng6", "- [ ] an open item")

	preview, err := f.s.Preflight(TransitionStartPhase, f.msID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.OK || !hasBlocker(preview, "gate_open") {
		t.Fatalf("preview should report the open gate: %+v", preview)
	}

	// The enforcement path refuses too, naming the same item.
	err = f.s.AssertCampaignGatesReady(f.engID, f.major, f.minor)
	if err == nil {
		t.Fatal("enforcement allowed a transition the preview blocked")
	}
	if !strings.Contains(err.Error(), "an open item") {
		t.Fatalf("enforcement reason differs from the preview's item: %v", err)
	}
}

// TestPreflightClearsWhenSatisfied proves the preview is not permanently negative:
// once the blockers are resolved it says ok.
func TestPreflightClearsWhenSatisfied(t *testing.T) {
	f := newPreflightFixtureWithGate(t, "Eng7", "- [x] done")
	if err := f.s.SetMilestoneLifecycle(f.engID, f.major, f.minor, LifeAwaitingDevelopment); err != nil {
		t.Fatal(err)
	}
	// Bind a worker.
	if _, err := f.s.PatchMilestone(f.msID, PatchMilestoneInput{ExecuteWorkerID: strPtr("w1")}); err != nil {
		t.Fatal(err)
	}

	res, err := f.s.Preflight(TransitionStartPhase, f.msID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected ready, got %+v", res.Blockers)
	}
	if len(res.Blockers) != 0 {
		t.Fatalf("ok result carries blockers: %+v", res.Blockers)
	}
}

func TestAllTransitionEnforcementUsesPreflight(t *testing.T) {
	f := newPreflightFixtureWithGate(t, "all transitions", "- [ ] evidence")
	cases := []struct {
		transition Transition
		target     string
	}{
		{TransitionStartPhase, f.msID},
		{TransitionSubmitTest, f.msID},
		{TransitionPassTest, f.msID},
		{TransitionAcceptMile, f.msID},
		{TransitionSealMajor, fmt.Sprintf("%d", f.major)},
		{TransitionBootstrapRoad, f.desID},
	}
	for _, tc := range cases {
		t.Run(string(tc.transition), func(t *testing.T) {
			preview, err := f.s.Preflight(tc.transition, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			enforced := f.s.EnforcePreflight(tc.transition, tc.target)
			if preview.OK != (enforced == nil) {
				t.Fatalf("preview/enforcement drift: preview=%+v enforcement=%v", preview, enforced)
			}
			if enforced != nil {
				for _, b := range preview.Blockers {
					if b.Severity != "warning" && !strings.Contains(enforced.Error(), b.Message) {
						t.Fatalf("enforcement omitted %q: %v", b.Message, enforced)
					}
				}
			}
		})
	}
}

func hasBlocker(res PreflightResult, code string) bool {
	for _, b := range res.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

func blockerMessage(res PreflightResult, code string) string {
	for _, b := range res.Blockers {
		if b.Code == code {
			return b.Message
		}
	}
	return ""
}
