// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"strings"
)

// SeedBugfixPhases appends planned phases for each open/fixing bug (idempotent by phase id).
func (s *Store) SeedBugfixPhases(msID, engID string, major, minor int) ([]Phase, error) {
	msID = strings.TrimSpace(msID)
	engID = strings.TrimSpace(engID)
	if msID == "" || engID == "" {
		return nil, fmt.Errorf("milestone and eng_req_id required")
	}
	bugs, err := s.listBuglistForBind(engID, major, minor)
	if err != nil {
		return nil, err
	}
	phases, err := s.listPhases(msID)
	if err != nil {
		return nil, err
	}
	exist := map[string]bool{}
	maxOrder := -1
	for _, p := range phases {
		exist[p.ID] = true
		// sort_order is not on Phase struct — query max separately
	}
	var maxOrd int
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) FROM delivery_phases WHERE milestone_id=?`, msID).Scan(&maxOrd)
	maxOrder = maxOrd

	var added []Phase
	for _, b := range bugs {
		if b.Status != "open" && b.Status != "fixing" {
			continue
		}
		phID := fmt.Sprintf("%s_bf_%s", msID, b.ID)
		if exist[phID] {
			continue
		}
		maxOrder++
		title := "Bugfix: " + strings.TrimSpace(b.Title)
		if title == "Bugfix:" {
			title = "Bugfix: " + b.ID
		}
		_, err := s.db.Exec(`
INSERT INTO delivery_phases(id, milestone_id, title, status, sort_order) VALUES(?,?,?,?,?)`,
			phID, msID, title, "planned", maxOrder)
		if err != nil {
			return nil, err
		}
		added = append(added, Phase{ID: phID, Title: title, Status: "planned"})
		exist[phID] = true
	}
	if added == nil {
		added = []Phase{}
	}
	return added, nil
}
