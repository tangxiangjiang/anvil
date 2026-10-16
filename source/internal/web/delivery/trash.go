// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Restore modes when a major already exists.
const (
	RestoreOverwrite = "overwrite"
	RestoreAppend    = "append"
)

// TrashItem is a soft-deleted delivery artifact.
type TrashItem struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Label     string          `json:"label"`
	DeletedAt string          `json:"deleted_at"`
	Preview   TrashMajorMeta  `json:"preview,omitempty"`
	Payload   json.RawMessage `json:"-"`
}

// TrashMajorMeta is list-card metadata for a trashed major.
type TrashMajorMeta struct {
	Major          int `json:"major"`
	MilestoneCount int `json:"milestone_count"`
	VersionCount   int `json:"version_count"`
	DesignCount    int `json:"design_count"`
}

// TrashMajorBundle is the serialized major + dependents.
type TrashMajorBundle struct {
	Major    MajorPlan           `json:"major"`
	Versions []TrashVersionRow   `json:"versions"`
	Designs  []TrashDesignBundle `json:"designs"`
}

// TrashVersionRow is one req version under a major.
type TrashVersionRow struct {
	EngReqID string     `json:"eng_req_id"`
	Version  ReqVersion `json:"version"`
}

// TrashDesignBundle is a design + options/versions under a major.
type TrashDesignBundle struct {
	Design   Design          `json:"design"`
	Options  []DesignOption  `json:"options"`
	Versions []DesignVersion `json:"versions,omitempty"`
}

// CountMilestonesUnderMajor counts distinct major.minor groups (and total version rows).
func (s *Store) CountMilestonesUnderMajor(major int) (milestones, versions int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*) FROM delivery_req_versions WHERE major=?`, major).Scan(&versions)
	if err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRow(`
SELECT COUNT(*) FROM (
  SELECT DISTINCT minor FROM delivery_req_versions WHERE major=?
)`, major).Scan(&milestones)
	return milestones, versions, err
}

// DeleteMajorToTrash soft-deletes a Planning major that is the highest number.
// Milestone/version/design rows under that major move into trash with it.
func (s *Store) DeleteMajorToTrash(major int) (TrashItem, error) {
	m, err := s.getMajor(major)
	if err != nil {
		return TrashItem{}, err
	}
	if m.Status != MajorPlanning {
		return TrashItem{}, fmt.Errorf("only Planning majors can be deleted (major %d is %s)", major, m.Status)
	}
	next, err := s.NextMajorNumber()
	if err != nil {
		return TrashItem{}, err
	}
	if major != next-1 {
		return TrashItem{}, fmt.Errorf("only the last major (%d) can be deleted", next-1)
	}

	bundle := TrashMajorBundle{Major: m}
	vrows, err := s.db.Query(`
SELECT eng_req_id, major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at
FROM delivery_req_versions WHERE major=?`, major)
	if err != nil {
		return TrashItem{}, err
	}
	for vrows.Next() {
		var row TrashVersionRow
		var v ReqVersion
		if err := vrows.Scan(
			&row.EngReqID, &v.Major, &v.Minor, &v.Patch, &v.Title, &v.Status,
			&v.Problem, &v.Goals, &v.NonGoals, &v.Constraints, &v.Success, &v.UpdatedAt,
		); err != nil {
			_ = vrows.Close()
			return TrashItem{}, err
		}
		row.Version = v
		bundle.Versions = append(bundle.Versions, row)
	}
	if err := vrows.Close(); err != nil {
		return TrashItem{}, err
	}

	drows, err := s.db.Query(`SELECT `+designSelectCols+` FROM delivery_designs WHERE major=?`, major)
	if err != nil {
		return TrashItem{}, err
	}
	var designs []Design
	for drows.Next() {
		var d Design
		if err := scanDesign(drows, &d); err != nil {
			_ = drows.Close()
			return TrashItem{}, err
		}
		designs = append(designs, d)
	}
	if err := drows.Close(); err != nil {
		return TrashItem{}, err
	}
	for _, d := range designs {
		opts, err := s.listOptions(d.ID)
		if err != nil {
			return TrashItem{}, err
		}
		vers, err := s.listDesignVersions(d.ID)
		if err != nil {
			return TrashItem{}, err
		}
		bundle.Designs = append(bundle.Designs, TrashDesignBundle{Design: d, Options: opts, Versions: vers})
	}

	raw, err := json.Marshal(bundle)
	if err != nil {
		return TrashItem{}, err
	}
	ms, vers, _ := s.CountMilestonesUnderMajor(major)
	id := fmt.Sprintf("trash_major_%d_%d", major, time.Now().UnixNano())
	label := fmt.Sprintf("Major %s (%s) · %d milestone(s)",
		majorLabel(major), m.Title, ms)
	ts := nowRFC3339()

	tx, err := s.db.Begin()
	if err != nil {
		return TrashItem{}, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`INSERT INTO delivery_trash(id, kind, label, payload, deleted_at) VALUES(?,?,?,?,?)`,
		id, "major", label, string(raw), ts)
	if err != nil {
		return TrashItem{}, err
	}
	for _, db := range bundle.Designs {
		if _, err := tx.Exec(`DELETE FROM delivery_design_version_deps WHERE design_id=?`, db.Design.ID); err != nil {
			return TrashItem{}, err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_design_versions WHERE design_id=?`, db.Design.ID); err != nil {
			return TrashItem{}, err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_design_options WHERE design_id=?`, db.Design.ID); err != nil {
			return TrashItem{}, err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_designs WHERE id=?`, db.Design.ID); err != nil {
			return TrashItem{}, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM delivery_req_versions WHERE major=?`, major); err != nil {
		return TrashItem{}, err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_buglist_items WHERE major=?`, major); err != nil {
		return TrashItem{}, err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_test_cases WHERE major=?`, major); err != nil {
		return TrashItem{}, err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_polishes WHERE major=?`, major); err != nil {
		return TrashItem{}, err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_gates WHERE ref_id LIKE ?`, fmt.Sprintf("%%:%d.%%", major)); err != nil {
		return TrashItem{}, err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_majors WHERE major=?`, major); err != nil {
		return TrashItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return TrashItem{}, err
	}

	return TrashItem{
		ID: id, Kind: "major", Label: label, DeletedAt: ts,
		Preview: TrashMajorMeta{Major: major, MilestoneCount: ms, VersionCount: vers, DesignCount: len(bundle.Designs)},
	}, nil
}

func majorLabel(major int) string {
	if major == 0 {
		return "0.x"
	}
	return fmt.Sprintf("%d.x", major)
}

// ListTrash returns trash items newest first.
func (s *Store) ListTrash() ([]TrashItem, error) {
	rows, err := s.db.Query(`SELECT id, kind, label, payload, deleted_at FROM delivery_trash ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []TrashItem
	for rows.Next() {
		var it TrashItem
		var payload string
		if err := rows.Scan(&it.ID, &it.Kind, &it.Label, &payload, &it.DeletedAt); err != nil {
			return nil, err
		}
		it.Payload = json.RawMessage(payload)
		if it.Kind == "major" {
			var b TrashMajorBundle
			if json.Unmarshal([]byte(payload), &b) == nil {
				ms := map[int]bool{}
				for _, v := range b.Versions {
					ms[v.Version.Minor] = true
				}
				it.Preview = TrashMajorMeta{
					Major: b.Major.Major, MilestoneCount: len(ms),
					VersionCount: len(b.Versions), DesignCount: len(b.Designs),
				}
			}
		}
		list = append(list, it)
	}
	if list == nil {
		list = []TrashItem{}
	}
	return list, rows.Err()
}

// PurgeTrash permanently removes a trash item.
func (s *Store) PurgeTrash(id string) error {
	res, err := s.db.Exec(`DELETE FROM delivery_trash WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("trash item not found")
	}
	return nil
}

// RestoreTrashResult describes restore outcome.
type RestoreTrashResult struct {
	Major    MajorPlan `json:"major"`
	Mode     string    `json:"mode"`
	Conflict bool      `json:"conflict"`
	Message  string    `json:"message"`
}

// RestoreMajorFromTrash restores a trashed major.
// If the major number already exists, mode must be overwrite or append.
func (s *Store) RestoreMajorFromTrash(id, mode string) (RestoreTrashResult, error) {
	var payload, kind string
	err := s.db.QueryRow(`SELECT kind, payload FROM delivery_trash WHERE id=?`, id).Scan(&kind, &payload)
	if err != nil {
		return RestoreTrashResult{}, fmt.Errorf("trash item not found")
	}
	if kind != "major" {
		return RestoreTrashResult{}, fmt.Errorf("unsupported trash kind %s", kind)
	}
	var bundle TrashMajorBundle
	if err := json.Unmarshal([]byte(payload), &bundle); err != nil {
		return RestoreTrashResult{}, err
	}
	major := bundle.Major.Major
	_, exists, err := s.majorExists(major)
	if err != nil {
		return RestoreTrashResult{}, err
	}

	mode = strings.TrimSpace(strings.ToLower(mode))
	if exists {
		if mode != RestoreOverwrite && mode != RestoreAppend {
			return RestoreTrashResult{
				Conflict: true,
				Major:    bundle.Major,
				Message: fmt.Sprintf(
					"Major %s already exists. Overwrite its milestones, or append trashed milestones onto it.",
					majorLabel(major),
				),
			}, nil
		}
	} else {
		mode = "recreate"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return RestoreTrashResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	ts := nowRFC3339()
	if !exists {
		_, err = tx.Exec(`
INSERT INTO delivery_majors(major, title, status, summary, created_at, updated_at)
VALUES(?,?,?,?,?,?)`,
			bundle.Major.Major, bundle.Major.Title, bundle.Major.Status, bundle.Major.Summary,
			bundle.Major.CreatedAt, ts)
		if err != nil {
			return RestoreTrashResult{}, err
		}
	} else if mode == RestoreOverwrite {
		// Remove live versions/designs under this major, then restore trash payload.
		drows, err := tx.Query(`SELECT id FROM delivery_designs WHERE major=?`, major)
		if err != nil {
			return RestoreTrashResult{}, err
		}
		var ids []string
		for drows.Next() {
			var id string
			_ = drows.Scan(&id)
			ids = append(ids, id)
		}
		_ = drows.Close()
		for _, did := range ids {
			if _, err := tx.Exec(`DELETE FROM delivery_design_version_deps WHERE design_id=?`, did); err != nil {
				return RestoreTrashResult{}, err
			}
			if _, err := tx.Exec(`DELETE FROM delivery_design_versions WHERE design_id=?`, did); err != nil {
				return RestoreTrashResult{}, err
			}
			if _, err := tx.Exec(`DELETE FROM delivery_design_options WHERE design_id=?`, did); err != nil {
				return RestoreTrashResult{}, err
			}
		}
		if _, err := tx.Exec(`DELETE FROM delivery_designs WHERE major=?`, major); err != nil {
			return RestoreTrashResult{}, err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_req_versions WHERE major=?`, major); err != nil {
			return RestoreTrashResult{}, err
		}
		if _, err := tx.Exec(`UPDATE delivery_majors SET title=?, status=?, summary=?, updated_at=? WHERE major=?`,
			bundle.Major.Title, bundle.Major.Status, bundle.Major.Summary, ts, major); err != nil {
			return RestoreTrashResult{}, err
		}
	}

	minorMap := map[int]int{}
	if mode == RestoreAppend {
		var maxMinor sql.NullInt64
		if err := tx.QueryRow(`SELECT MAX(minor) FROM delivery_req_versions WHERE major=?`, major).Scan(&maxMinor); err != nil {
			return RestoreTrashResult{}, err
		}
		next := 1
		if maxMinor.Valid {
			next = int(maxMinor.Int64) + 1
		}
		seen := map[int]bool{}
		var ordered []int
		for _, row := range bundle.Versions {
			if !seen[row.Version.Minor] {
				seen[row.Version.Minor] = true
				ordered = append(ordered, row.Version.Minor)
			}
		}
		// Stable order by original minor.
		for i := 0; i < len(ordered); i++ {
			for j := i + 1; j < len(ordered); j++ {
				if ordered[j] < ordered[i] {
					ordered[i], ordered[j] = ordered[j], ordered[i]
				}
			}
		}
		for _, old := range ordered {
			minorMap[old] = next
			next++
		}
	}

	for _, row := range bundle.Versions {
		v := row.Version
		newMinor := v.Minor
		if mode == RestoreAppend {
			newMinor = minorMap[v.Minor]
		}
		_, err = tx.Exec(`
INSERT INTO delivery_req_versions(eng_req_id, major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			row.EngReqID, major, newMinor, v.Patch, v.Title, v.Status,
			v.Problem, v.Goals, v.NonGoals, v.Constraints, v.Success, v.UpdatedAt)
		if err != nil {
			return RestoreTrashResult{}, fmt.Errorf("restore version: %w", err)
		}
	}

	for _, db := range bundle.Designs {
		d := db.Design
		newMinor := d.Minor
		if mode == RestoreAppend {
			if mapped, ok := minorMap[d.Minor]; ok {
				newMinor = mapped
			} else {
				newMinor = d.Minor
			}
		}
		newID := d.ID
		if mode == RestoreAppend || mode == RestoreOverwrite {
			// Avoid PK clash on overwrite re-insert of same ids (overwrite deleted them).
			if mode == RestoreAppend {
				newID = fmt.Sprintf("%s_r%d", d.ID, time.Now().UnixNano()%100000)
			}
		}
		_, err = tx.Exec(`
INSERT INTO delivery_designs(id, eng_req_id, major, minor, patch, title, status, chosen_option_id, adr, updated_at,
  adr_context, adr_decision, adr_consequences, adr_spec, adr_architecture, adr_sequence, adr_sample)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID, d.EngReqID, major, newMinor, d.Patch, d.Title, d.Status, d.ChosenOptionID, d.ADR, d.UpdatedAt,
			d.AdrContext, d.AdrDecision, d.AdrConsequences, d.AdrSpec, d.AdrArchitecture, d.AdrSequence, d.AdrSample)
		if err != nil {
			return RestoreTrashResult{}, fmt.Errorf("restore design: %w", err)
		}
		for i, o := range db.Options {
			oid := o.ID
			if mode == RestoreAppend {
				oid = fmt.Sprintf("%s_%d", newID, i)
			}
			_, err = tx.Exec(`
INSERT INTO delivery_design_options(id, design_id, title, summary, tradeoffs, risks, out_of_scope, sort_order)
VALUES(?,?,?,?,?,?,?,?)`, oid, newID, o.Title, o.Summary, o.Tradeoffs, o.Risks, o.OutOfScope, i)
			if err != nil {
				return RestoreTrashResult{}, err
			}
		}
		idMap := map[string]string{} // old version id → new
		for _, v := range db.Versions {
			vid := v.ID
			if mode == RestoreAppend {
				vid = fmt.Sprintf("%s_v%d", newID, v.SortOrder)
			}
			idMap[v.ID] = vid
			st := v.Status
			if st == "" {
				if designVersionReady(v) {
					st = "ready"
				} else {
					st = "draft"
				}
			}
			_, err = tx.Exec(`
INSERT INTO delivery_design_versions(id, design_id, sort_order, title, gate, sub_architecture, class_design, status, updated_at)
VALUES(?,?,?,?,?,?,?,?,?)`,
				vid, newID, v.SortOrder, v.Title, v.Gate, v.SubArchitecture, v.ClassDesign, st, v.UpdatedAt)
			if err != nil {
				return RestoreTrashResult{}, err
			}
		}
		for _, v := range db.Versions {
			newVid := idMap[v.ID]
			for _, dep := range v.DependsOn {
				newDep, ok := idMap[dep]
				if !ok {
					continue
				}
				if _, err := tx.Exec(`
INSERT INTO delivery_design_version_deps(design_id, version_id, depends_on_id) VALUES(?,?,?)`,
					newID, newVid, newDep); err != nil {
					return RestoreTrashResult{}, err
				}
			}
		}
	}

	if _, err := tx.Exec(`DELETE FROM delivery_trash WHERE id=?`, id); err != nil {
		return RestoreTrashResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RestoreTrashResult{}, err
	}
	out, _ := s.getMajor(major)
	msg := "Restored major " + majorLabel(major)
	if mode == RestoreAppend {
		msg += " (milestones appended)"
	} else if mode == RestoreOverwrite {
		msg += " (existing milestones overwritten)"
	}
	return RestoreTrashResult{Major: out, Mode: mode, Message: msg}, nil
}

func (s *Store) majorExists(major int) (MajorPlan, bool, error) {
	m, err := s.getMajor(major)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return MajorPlan{}, false, nil
		}
		return MajorPlan{}, false, err
	}
	return m, true, nil
}
