// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"strings"
)

// Pre-flight (p_0.46.11).
//
// A lifecycle transition could be forced through an incomplete Gate: the store
// warned, but the console let a determined click through, and the refusal that did
// fire explained itself only *after* the click. Both halves are fixed here by one
// function:
//
//   - Preflight returns the blockers for a transition, so the button can say why
//     it is disabled before anything is attempted.
//   - The transition itself calls the same function and refuses with the same
//     blockers.
//
// ONE implementation on purpose. Two would drift, and drift is exactly how a gate
// becomes theatre: the preview says "ready", the server says "no", and the
// operator learns to distrust both.

// Blocker is one reason a transition cannot proceed.
type Blocker struct {
	// Code is stable for tests and for the client to key on.
	Code string `json:"code"`
	// Message is the sentence shown beside the button.
	Message string `json:"message"`
	// Href is where to fix it, when there is a place to go.
	Href string `json:"href,omitempty"`
}

// PreflightResult is what GET /api/v1/delivery/preflight answers.
type PreflightResult struct {
	Transition string    `json:"transition"`
	Target     string    `json:"target"`
	OK         bool      `json:"ok"`
	Blockers   []Blocker `json:"blockers"`
}

// Transition names a lifecycle move the console can attempt.
type Transition string

const (
	TransitionStartPhase    Transition = "start_phase"
	TransitionSubmitTest    Transition = "submit_test"
	TransitionPassTest      Transition = "pass_test"
	TransitionAcceptMile    Transition = "accept_milestone"
	TransitionSealMajor     Transition = "seal_major"
	TransitionBootstrapRoad Transition = "bootstrap_roadmap"
)

// KnownTransitions is the set the API will answer for, so an unknown one is a
// 400 rather than a silent "ok".
var KnownTransitions = map[Transition]bool{
	TransitionStartPhase:    true,
	TransitionSubmitTest:    true,
	TransitionPassTest:      true,
	TransitionAcceptMile:    true,
	TransitionSealMajor:     true,
	TransitionBootstrapRoad: true,
}

// Preflight answers whether a transition may proceed, and why not.
//
// target is the entity id the transition acts on: a milestone id for the
// milestone transitions, a major number for seal, a design id for bootstrap.
func (s *Store) Preflight(t Transition, target string) (PreflightResult, error) {
	out := PreflightResult{Transition: string(t), Target: target, Blockers: []Blocker{}}
	add := func(code, msg, href string) {
		out.Blockers = append(out.Blockers, Blocker{Code: code, Message: msg, Href: href})
	}

	switch t {
	case TransitionStartPhase:
		ms, ok := s.findMilestone(target)
		if !ok {
			add("milestone_not_found", "Milestone not found.", "")
			break
		}
		bind := parseMilestoneBind(ms.ID)
		if bind == nil {
			add("not_bound", "This milestone has no requirements bind, so there is no phase to run.",
				"#/delivery/roadmap")
			break
		}
		if err := s.AssertMajorWritable(bind.Major); err != nil {
			add("major_sealed", err.Error(), "#/delivery/requirements")
		}
		if strings.TrimSpace(ms.ExecuteWorkerID) == "" {
			add("no_worker", "Pick a Worker before starting: the phase job runs on that checkout.",
				"#/delivery/roadmap/m/"+fmt.Sprintf("%d", bind.Major))
		}
		if open := s.openGatesForMilestone(ms.ID); len(open) > 0 {
			add("gate_open", gateBlockMessage(open), "#/delivery/designs")
		}
		if ms.RunState == RunRunning {
			add("already_running", "This milestone is already running.", "#/delivery/roadmap")
		}

	case TransitionSubmitTest:
		ms, ok := s.findMilestone(target)
		if !ok {
			add("milestone_not_found", "Milestone not found.", "")
			break
		}
		if ms.Status != LifeInDevelopment {
			add("wrong_state",
				fmt.Sprintf("Only a milestone in development can be submitted (this one is %s).", ms.Status),
				"#/delivery/roadmap")
		}

	case TransitionPassTest:
		ms, ok := s.findMilestone(target)
		if !ok {
			add("milestone_not_found", "Milestone not found.", "")
			break
		}
		if ms.Status != LifeTesting {
			add("wrong_state",
				fmt.Sprintf("Only a milestone in testing can pass (this one is %s).", ms.Status),
				"#/delivery/test")
			break
		}
		bind := parseMilestoneBind(ms.ID)
		if bind == nil {
			add("not_bound", "This milestone has no requirements bind.", "#/delivery/test")
			break
		}
		if err := s.assertPassCampaignClean(bind.EngID, bind.Major, bind.Minor); err != nil {
			add("campaign_not_clean", err.Error(), "#/delivery/test")
		}

	case TransitionAcceptMile:
		ms, ok := s.findMilestone(target)
		if !ok {
			add("milestone_not_found", "Milestone not found.", "")
			break
		}
		if ms.Status != LifeComplete {
			add("wrong_state",
				fmt.Sprintf("Only a complete milestone can be accepted (this one is %s).", ms.Status),
				"#/delivery/roadmap")
			break
		}
		if ms.AcceptStatus == AcceptAccepted {
			add("already_accepted", "This milestone is already accepted.", "#/delivery/roadmap")
			break
		}
		if open := OpenGateItems(ms.Acceptance); len(open) > 0 {
			add("acceptance_open", gateBlockMessage(open), "#/delivery/roadmap")
		}

	case TransitionSealMajor:
		major, ok := atoiOK(target)
		if !ok {
			add("bad_target", "A generation number is required to seal.", "")
			break
		}
		plan, err := s.getMajor(major)
		if err != nil {
			add("major_not_found", "Generation not found.", "#/delivery/requirements")
			break
		}
		if plan.Status == MajorSealed {
			add("already_sealed", "This generation is already sealed.", "#/delivery/requirements")
			break
		}
		// Sealing with open work is allowed but must be stated: it is the one
		// irreversible move, and afterwards those milestones cannot be extended.
		if n := s.countOpenMilestones(major); n > 0 {
			add("open_work",
				fmt.Sprintf("%d milestone(s) under this generation are not complete. Sealing locks them as they are.", n),
				"#/delivery/roadmap/m/"+fmt.Sprintf("%d", major))
		}

	case TransitionBootstrapRoad:
		d, err := s.getDesign(target)
		if err != nil {
			add("design_not_found", "Design not found.", "#/delivery/designs")
			break
		}
		if d.Status != "approved" {
			add("not_approved", "Approve the design before bootstrapping the roadmap.", "#/delivery/designs/"+d.ID)
			break
		}
		if vers := d.Versions; true {
			if len(vers) == 0 {
				add("no_versions", "The design has no versions, so there is nothing to publish.", "#/delivery/designs/"+d.ID)
			} else if !versionsComplete(vers) {
				add("versions_incomplete",
					"Every design version needs a Gate, sub-architecture and class design before publishing.",
					"#/delivery/designs/"+d.ID)
			}
		}

	default:
		return out, fmt.Errorf("unknown transition %q", t)
	}

	out.OK = len(out.Blockers) == 0
	return out, nil
}

// gateBlockMessage turns open checklist items into one sentence.
func gateBlockMessage(open []GateCheckItem) string {
	if len(open) == 0 {
		return ""
	}
	names := make([]string, 0, len(open))
	for _, it := range open {
		if strings.TrimSpace(it.Text) != "" {
			names = append(names, strings.TrimSpace(it.Text))
		} else {
			names = append(names, fmt.Sprintf("#%d", it.Index))
		}
	}
	head := fmt.Sprintf("%d unchecked gate item(s): ", len(open))
	msg := head + strings.Join(names, "; ")
	if len(msg) > 300 {
		msg = msg[:297] + "…"
	}
	return msg
}

// findMilestone locates a roadmap milestone by id, through the snapshot so it
// sees the same rows the console rendered.
func (s *Store) findMilestone(msID string) (Milestone, bool) {
	snap, err := s.Snapshot()
	if err != nil {
		return Milestone{}, false
	}
	for _, m := range snap.Roadmap.Milestones {
		if m.ID == msID {
			return m, true
		}
	}
	return Milestone{}, false
}

// openGatesForMilestone collects every open gate checkbox across the milestone's
// design versions, which is what blocks a phase job.
func (s *Store) openGatesForMilestone(msID string) []GateCheckItem {
	bind := parseMilestoneBind(msID)
	if bind == nil {
		return nil
	}
	snap, err := s.Snapshot()
	if err != nil {
		return nil
	}
	var out []GateCheckItem
	for _, d := range snap.Designs {
		if d.EngReqID != bind.EngID || d.Major != bind.Major || d.Minor != bind.Minor {
			continue
		}
		for _, v := range d.Versions {
			out = append(out, OpenGateItems(v.Gate)...)
		}
	}
	return out
}

func (s *Store) countOpenMilestones(major int) int {
	snap, err := s.Snapshot()
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range snap.Roadmap.Milestones {
		bind := parseMilestoneBind(m.ID)
		if bind == nil || bind.Major != major {
			continue
		}
		if m.Status != LifeComplete {
			n++
		}
	}
	return n
}

// versionsComplete is the design-approval rule, reusable here so the preview and
// the approval cannot disagree about what "complete" means.
func versionsComplete(vers []DesignVersion) bool {
	if len(vers) == 0 {
		return false
	}
	for _, v := range vers {
		if strings.TrimSpace(v.Gate) == "" ||
			strings.TrimSpace(v.SubArchitecture) == "" ||
			strings.TrimSpace(v.ClassDesign) == "" {
			return false
		}
	}
	return true
}

func atoiOK(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
