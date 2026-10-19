// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"regexp"
	"strings"
)

// GateCheckItem is one markdown checkbox line inside a Design Version Gate field.
type GateCheckItem struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	Done  bool   `json:"done"`
}

var gateCheckRe = regexp.MustCompile(`(?i)^(\s*(?:[-*]|\d+\.)\s+)\[([ xX])\](\s+)(.*)$`)

// ParseGateChecklist extracts checkbox items from Gate markdown.
func ParseGateChecklist(gate string) []GateCheckItem {
	var out []GateCheckItem
	idx := 0
	for _, line := range strings.Split(gate, "\n") {
		m := gateCheckRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		mark := strings.TrimSpace(m[2])
		text := strings.TrimSpace(m[4])
		out = append(out, GateCheckItem{
			Index: idx,
			Text:  text,
			Done:  mark == "x" || mark == "X",
		})
		idx++
	}
	return out
}

// SetGateChecklistItem flips one checkbox by checklist index and returns new Gate text.
func SetGateChecklistItem(gate string, index int, done bool) (string, error) {
	if index < 0 {
		return "", fmt.Errorf("gate check index required")
	}
	lines := strings.Split(gate, "\n")
	cur := 0
	found := false
	mark := " "
	if done {
		mark = "x"
	}
	for i, line := range lines {
		m := gateCheckRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if cur == index {
			lines[i] = m[1] + "[" + mark + "]" + m[3] + m[4]
			found = true
			break
		}
		cur++
	}
	if !found {
		return "", fmt.Errorf("gate check index %d not found", index)
	}
	return strings.Join(lines, "\n"), nil
}

// OpenGateItems returns unchecked checklist lines (empty if none or no boxes).
func OpenGateItems(gate string) []GateCheckItem {
	all := ParseGateChecklist(gate)
	var open []GateCheckItem
	for _, it := range all {
		if !it.Done {
			open = append(open, it)
		}
	}
	return open
}

// DesignVersionForPhase resolves the Design Version aligned with a phase (1:1 sort order).
func (s *Store) DesignVersionForPhase(msID, phaseID string) (DesignVersion, string, error) {
	var empty DesignVersion
	phases, err := s.listPhases(msID)
	if err != nil {
		return empty, "", err
	}
	idx := -1
	for i, p := range phases {
		if p.ID == phaseID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return empty, "", fmt.Errorf("phase not found")
	}
	bind := parseMilestoneBind(msID)
	if bind == nil {
		return empty, "", fmt.Errorf("milestone bind required")
	}
	for _, d := range mustListDesigns(s) {
		if d.EngReqID != bind.EngID || d.Major != bind.Major || d.Minor != bind.Minor {
			continue
		}
		vers, err := s.listDesignVersions(d.ID)
		if err != nil {
			return empty, "", err
		}
		if len(vers) == 0 {
			return empty, d.ID, fmt.Errorf("design has no versions")
		}
		if idx < len(vers) {
			return vers[idx], d.ID, nil
		}
		return vers[0], d.ID, nil
	}
	return empty, "", fmt.Errorf("design not found for milestone")
}

// AssertPhaseGateReady blocks enqueue when the phase's Design Version Gate has open checkboxes.
func (s *Store) AssertPhaseGateReady(msID, phaseID string) error {
	dv, _, err := s.DesignVersionForPhase(msID, phaseID)
	if err != nil {
		// No design bind / versions — do not block execute plumbing.
		if strings.Contains(err.Error(), "design not found") || strings.Contains(err.Error(), "bind required") {
			return nil
		}
		return err
	}
	open := OpenGateItems(dv.Gate)
	if len(open) == 0 {
		return nil
	}
	parts := make([]string, 0, len(open))
	for _, it := range open {
		if it.Text == "" {
			parts = append(parts, fmt.Sprintf("#%d", it.Index))
		} else {
			parts = append(parts, it.Text)
		}
	}
	return fmt.Errorf("gate checklist incomplete (%s): %s", dv.Title, strings.Join(parts, "; "))
}

// AssertCampaignGatesReady requires every Design Version under the bind to have no open checkboxes.
func (s *Store) AssertCampaignGatesReady(engID string, major, minor int) error {
	engID = strings.TrimSpace(engID)
	for _, d := range mustListDesigns(s) {
		if d.EngReqID != engID || d.Major != major || d.Minor != minor {
			continue
		}
		vers, err := s.listDesignVersions(d.ID)
		if err != nil {
			return err
		}
		for _, dv := range vers {
			open := OpenGateItems(dv.Gate)
			if len(open) == 0 {
				continue
			}
			parts := make([]string, 0, len(open))
			for _, it := range open {
				if it.Text == "" {
					parts = append(parts, fmt.Sprintf("#%d", it.Index))
				} else {
					parts = append(parts, it.Text)
				}
			}
			return fmt.Errorf("gate checklist incomplete (%s): %s", dv.Title, strings.Join(parts, "; "))
		}
		return nil
	}
	return nil
}

// assertAcceptChecklist requires milestone Acceptance checkboxes (if any) to be checked.
func (s *Store) assertAcceptChecklist(ms Milestone) error {
	open := OpenGateItems(ms.Acceptance)
	if len(open) == 0 {
		return nil
	}
	parts := make([]string, 0, len(open))
	for _, it := range open {
		if it.Text == "" {
			parts = append(parts, fmt.Sprintf("#%d", it.Index))
		} else {
			parts = append(parts, it.Text)
		}
	}
	return fmt.Errorf("acceptance checklist incomplete: %s", strings.Join(parts, "; "))
}

// ToggleMilestoneAcceptanceCheck flips a checkbox in milestone.acceptance (allowed anytime).
func (s *Store) ToggleMilestoneAcceptanceCheck(msID string, index int, done bool) (Milestone, error) {
	ms, err := s.getMilestone(msID)
	if err != nil {
		return Milestone{}, err
	}
	next, err := SetGateChecklistItem(ms.Acceptance, index, done)
	if err != nil {
		return Milestone{}, err
	}
	_, err = s.db.Exec(`UPDATE delivery_milestones SET acceptance=? WHERE id=?`, next, msID)
	if err != nil {
		return Milestone{}, err
	}
	return s.getMilestone(msID)
}

// ToggleDesignVersionGateCheck sets one Gate checkbox. Allowed after Design Approve
// (checklist is an execute control, not an authoring edit).
func (s *Store) ToggleDesignVersionGateCheck(designID, versionID string, index int, done bool) (DesignVersion, error) {
	designID = strings.TrimSpace(designID)
	versionID = strings.TrimSpace(versionID)
	if _, err := s.getDesign(designID); err != nil {
		return DesignVersion{}, err
	}
	v, err := s.getDesignVersion(designID, versionID)
	if err != nil {
		return DesignVersion{}, err
	}
	next, err := SetGateChecklistItem(v.Gate, index, done)
	if err != nil {
		return DesignVersion{}, err
	}
	ts := nowRFC3339()
	_, err = s.db.Exec(`
UPDATE delivery_design_versions SET gate=?, updated_at=? WHERE id=? AND design_id=?`,
		next, ts, versionID, designID)
	if err != nil {
		return DesignVersion{}, err
	}
	_, _ = s.db.Exec(`UPDATE delivery_designs SET updated_at=? WHERE id=?`, ts, designID)
	v.Gate = next
	v.UpdatedAt = ts
	return v, nil
}

func (s *Store) getDesignVersion(designID, versionID string) (DesignVersion, error) {
	vers, err := s.listDesignVersions(designID)
	if err != nil {
		return DesignVersion{}, err
	}
	for _, v := range vers {
		if v.ID == versionID {
			return v, nil
		}
	}
	return DesignVersion{}, fmt.Errorf("design version not found")
}
