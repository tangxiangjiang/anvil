// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"sort"
	"strings"
)

const (
	maxScanRows     = 10000
	maxScanFindings = 500
)

// ReadinessScope limits diagnostics to one Project and/or Generation. Empty is
// the complete planning record. MilestoneID further narrows milestone rows.
type ReadinessScope struct {
	EngReqID    string
	Major       *int
	MilestoneID string
}

type ReadinessRow struct {
	Kind       string    `json:"kind"`
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	EngReqID   string    `json:"eng_req_id,omitempty"`
	Major      int       `json:"major"`
	Minor      int       `json:"minor,omitempty"`
	Lifecycle  string    `json:"lifecycle"`
	Transition string    `json:"transition"`
	OK         bool      `json:"ok"`
	Blockers   []Blocker `json:"blockers"`
	Href       string    `json:"href"`
}

type ConsistencyFinding struct {
	Severity string `json:"severity"` // blocking_error | warning | informational
	Code     string `json:"code"`
	Message  string `json:"message"`
	Href     string `json:"href,omitempty"`
	Kind     string `json:"kind,omitempty"`
	ID       string `json:"id,omitempty"`
	EngReqID string `json:"eng_req_id,omitempty"`
	Major    *int   `json:"major,omitempty"`
}

type ConsistencySummary struct {
	BlockingErrors int `json:"blocking_errors"`
	Warnings       int `json:"warnings"`
	Informational  int `json:"informational"`
	Inspected      int `json:"inspected"`
}

type ConsistencyReport struct {
	Findings  []ConsistencyFinding `json:"findings"`
	Summary   ConsistencySummary   `json:"summary"`
	Truncated bool                 `json:"truncated"`
}

type ReadinessReport struct {
	Rows        []ReadinessRow    `json:"rows"`
	Consistency ConsistencyReport `json:"consistency"`
}

// Readiness evaluates the same Preflight rules used by mutation enforcement.
func (s *Store) Readiness(scope ReadinessScope) (ReadinessReport, error) {
	out := ReadinessReport{Rows: []ReadinessRow{}}
	snap, err := s.Snapshot()
	if err != nil {
		return out, err
	}
	for _, ms := range snap.Roadmap.Milestones {
		bind := parseMilestoneBind(ms.ID)
		if bind == nil || !scope.matches(bind.EngID, bind.Major, ms.ID) {
			continue
		}
		t, terminal := nextMilestoneTransition(ms)
		if terminal {
			continue
		}
		pf, err := s.Preflight(t, ms.ID)
		if err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, ReadinessRow{
			Kind: "milestone", ID: ms.ID, Title: ms.Title, EngReqID: bind.EngID,
			Major: bind.Major, Minor: bind.Minor, Lifecycle: ms.Status,
			Transition: string(t), OK: pf.OK, Blockers: pf.Blockers,
			Href: fmt.Sprintf("#/delivery/roadmap/m/%d", bind.Major),
		})
	}
	if scope.MilestoneID == "" {
		for _, d := range snap.Designs {
			if !scope.matches(d.EngReqID, d.Major, "") || d.Status == "bootstrapped" {
				continue
			}
			pf, err := s.Preflight(TransitionBootstrapRoad, d.ID)
			if err != nil {
				return out, err
			}
			out.Rows = append(out.Rows, ReadinessRow{
				Kind: "design", ID: d.ID, Title: d.Title, EngReqID: d.EngReqID,
				Major: d.Major, Minor: d.Minor, Lifecycle: d.Status,
				Transition: string(TransitionBootstrapRoad), OK: pf.OK, Blockers: pf.Blockers,
				Href: "#/delivery/designs/" + d.ID,
			})
		}
		for _, m := range snap.Majors {
			if scope.Major != nil && *scope.Major != m.Major || m.Status == MajorSealed {
				continue
			}
			pf, err := s.Preflight(TransitionSealMajor, fmt.Sprintf("%d", m.Major))
			if err != nil {
				return out, err
			}
			out.Rows = append(out.Rows, ReadinessRow{
				Kind: "generation", ID: fmt.Sprintf("%d", m.Major), Title: m.Title,
				Major: m.Major, Lifecycle: m.Status, Transition: string(TransitionSealMajor),
				OK: pf.OK, Blockers: pf.Blockers, Href: fmt.Sprintf("#/delivery/requirements/m/%d", m.Major),
			})
		}
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if a.Major != b.Major {
			return a.Major < b.Major
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	out.Consistency, err = s.ScanConsistency(scope)
	return out, err
}

func nextMilestoneTransition(m Milestone) (Transition, bool) {
	switch m.Status {
	case LifeAwaitingDevelopment, LifeInDevelopment:
		if m.Status == LifeAwaitingDevelopment {
			return TransitionStartPhase, false
		}
		return TransitionSubmitTest, false
	case LifeTesting:
		return TransitionPassTest, false
	case LifeComplete:
		if m.AcceptStatus == AcceptAccepted {
			return "", true
		}
		return TransitionAcceptMile, false
	default:
		return TransitionStartPhase, false
	}
}

func (s ReadinessScope) matches(engID string, major int, milestoneID string) bool {
	if s.EngReqID != "" && s.EngReqID != engID {
		return false
	}
	if s.Major != nil && *s.Major != major {
		return false
	}
	return s.MilestoneID == "" || s.MilestoneID == milestoneID
}

type readinessScanDesign struct {
	id, engID    string
	major, minor int
}
type readinessScanMilestone struct {
	id, title, status, engID string
	major, minor             int
	bound                    bool
}
type readinessScanVersion struct {
	id, designID, gate string
	engID              string
	major              int
}

// ScanConsistency is deterministic, read-only and bounded. It deliberately
// reads tolerant row shapes instead of Snapshot so damaged references become
// findings rather than load failures or panics.
func (s *Store) ScanConsistency(scope ReadinessScope) (ConsistencyReport, error) {
	report := ConsistencyReport{Findings: []ConsistencyFinding{}}
	add := func(f ConsistencyFinding) {
		if len(report.Findings) >= maxScanFindings {
			report.Truncated = true
			return
		}
		report.Findings = append(report.Findings, f)
	}

	designs, err := s.scanDesignRows()
	if err != nil {
		return report, err
	}
	milestones, err := s.scanMilestoneRows()
	if err != nil {
		return report, err
	}
	versions, err := s.scanVersionRows(designs)
	if err != nil {
		return report, err
	}
	report.Summary.Inspected = len(designs) + len(milestones) + len(versions)

	reqMilestones := map[string]bool{}
	rows, err := s.db.Query(`SELECT eng_req_id, major, minor FROM delivery_req_versions GROUP BY eng_req_id, major, minor ORDER BY eng_req_id, major, minor LIMIT ?`, maxScanRows)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var eng string
		var major, minor int
		if err := rows.Scan(&eng, &major, &minor); err != nil {
			_ = rows.Close()
			return report, err
		}
		reqMilestones[fmt.Sprintf("%s\x00%d\x00%d", eng, major, minor)] = true
	}
	if err := rows.Close(); err != nil {
		return report, err
	}

	versionCount := map[string]int{}
	designSet := map[string]bool{}
	for _, d := range designs {
		designSet[d.id] = true
	}
	for _, v := range versions {
		versionCount[v.designID]++
	}

	for _, d := range designs {
		if !scope.matches(d.engID, d.major, "") {
			continue
		}
		key := fmt.Sprintf("%s\x00%d\x00%d", d.engID, d.major, d.minor)
		if !reqMilestones[key] {
			major := d.major
			add(ConsistencyFinding{Severity: "blocking_error", Code: "orphaned_design",
				Message: "Design is bound to a Project milestone that no longer exists.",
				Href:    "#/delivery/designs/" + d.id, Kind: "design", ID: d.id, EngReqID: d.engID, Major: &major})
		}
	}
	for _, m := range milestones {
		if !m.bound || !scope.matches(m.engID, m.major, m.id) {
			continue
		}
		found := false
		for _, d := range designs {
			if d.engID == m.engID && d.major == m.major && d.minor == m.minor && versionCount[d.id] > 0 {
				found = true
				break
			}
		}
		if !found {
			major := m.major
			add(ConsistencyFinding{Severity: "blocking_error", Code: "milestone_without_versions",
				Message: "Milestone has no Design Versions to execute.",
				Href:    fmt.Sprintf("#/delivery/roadmap/m/%d", m.major), Kind: "milestone",
				ID: m.id, EngReqID: m.engID, Major: &major})
		}
	}
	for _, v := range versions {
		if !designSet[v.designID] {
			if scope.EngReqID == "" && scope.Major == nil && scope.MilestoneID == "" {
				add(ConsistencyFinding{Severity: "blocking_error", Code: "orphaned_design_version",
					Message: "Design Version references a missing Design.", Href: "#/delivery/designs",
					Kind: "design_version", ID: v.id})
			}
			continue
		}
		if !scope.matches(v.engID, v.major, "") {
			continue
		}
		if strings.TrimSpace(v.gate) != "" && len(ParseGateChecklist(v.gate)) == 0 {
			major := v.major
			add(ConsistencyFinding{Severity: "warning", Code: "gate_without_checkboxes",
				Message: "Gate contains text but zero checkboxes; it cannot block on an unchecked item.",
				Href:    "#/delivery/designs/" + v.designID, Kind: "design_version",
				ID: v.id, EngReqID: v.engID, Major: &major})
		}
	}
	if err := s.scanDependencyFindings(scope, designs, versions, add); err != nil {
		return report, err
	}

	majorStatus := map[int]string{}
	rows, err = s.db.Query(`SELECT major, status FROM delivery_majors ORDER BY major LIMIT ?`, maxScanRows)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var n int
		var st string
		if err := rows.Scan(&n, &st); err != nil {
			_ = rows.Close()
			return report, err
		}
		majorStatus[n] = st
	}
	if err := rows.Close(); err != nil {
		return report, err
	}
	for _, m := range milestones {
		if !m.bound || !scope.matches(m.engID, m.major, m.id) {
			continue
		}
		if majorStatus[m.major] == MajorSealed && m.status != LifeComplete {
			major := m.major
			add(ConsistencyFinding{Severity: "warning", Code: "sealed_generation_open_milestone",
				Message: "Sealed Generation contains an unfinished Milestone.",
				Href:    fmt.Sprintf("#/delivery/roadmap/m/%d", m.major), Kind: "milestone",
				ID: m.id, EngReqID: m.engID, Major: &major})
		}
	}
	add(ConsistencyFinding{Severity: "informational", Code: "consistency_scan_complete",
		Message: fmt.Sprintf("Read-only consistency scan completed over %d records.", report.Summary.Inspected)})
	sort.SliceStable(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		rank := func(s string) int {
			if s == "blocking_error" {
				return 0
			}
			if s == "warning" {
				return 1
			}
			return 2
		}
		if rank(a.Severity) != rank(b.Severity) {
			return rank(a.Severity) < rank(b.Severity)
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.ID < b.ID
	})
	for _, f := range report.Findings {
		switch f.Severity {
		case "blocking_error":
			report.Summary.BlockingErrors++
		case "warning":
			report.Summary.Warnings++
		default:
			report.Summary.Informational++
		}
	}
	return report, nil
}

func (s *Store) scanDesignRows() ([]readinessScanDesign, error) {
	rows, err := s.db.Query(`SELECT id, eng_req_id, major, minor FROM delivery_designs ORDER BY id LIMIT ?`, maxScanRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []readinessScanDesign{}
	for rows.Next() {
		var d readinessScanDesign
		if err := rows.Scan(&d.id, &d.engID, &d.major, &d.minor); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) scanMilestoneRows() ([]readinessScanMilestone, error) {
	rows, err := s.db.Query(`SELECT id, title, status FROM delivery_milestones ORDER BY id LIMIT ?`, maxScanRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []readinessScanMilestone{}
	for rows.Next() {
		var m readinessScanMilestone
		if err := rows.Scan(&m.id, &m.title, &m.status); err != nil {
			return nil, err
		}
		if b := parseMilestoneBind(m.id); b != nil {
			m.engID, m.major, m.minor, m.bound = b.EngID, b.Major, b.Minor, true
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) scanVersionRows(designs []readinessScanDesign) ([]readinessScanVersion, error) {
	dm := map[string]readinessScanDesign{}
	for _, d := range designs {
		dm[d.id] = d
	}
	rows, err := s.db.Query(`SELECT id, design_id, gate FROM delivery_design_versions ORDER BY design_id, sort_order, id LIMIT ?`, maxScanRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []readinessScanVersion{}
	for rows.Next() {
		var v readinessScanVersion
		if err := rows.Scan(&v.id, &v.designID, &v.gate); err != nil {
			return nil, err
		}
		if d, ok := dm[v.designID]; ok {
			v.engID, v.major = d.engID, d.major
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) scanDependencyFindings(scope ReadinessScope, designs []readinessScanDesign, versions []readinessScanVersion, add func(ConsistencyFinding)) error {
	dm := map[string]readinessScanDesign{}
	for _, d := range designs {
		dm[d.id] = d
	}
	vm := map[string]readinessScanVersion{}
	for _, v := range versions {
		vm[v.id] = v
	}
	graph := map[string][]string{}
	rows, err := s.db.Query(`SELECT design_id, version_id, depends_on_id FROM delivery_design_version_deps ORDER BY design_id, version_id, depends_on_id LIMIT ?`, maxScanRows)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var designID, from, to string
		if err := rows.Scan(&designID, &from, &to); err != nil {
			return err
		}
		d, ok := dm[designID]
		if !ok || !scope.matches(d.engID, d.major, "") {
			continue
		}
		if _, okFrom := vm[from]; !okFrom {
			major := d.major
			add(ConsistencyFinding{Severity: "blocking_error", Code: "version_dependency_missing", Message: "Version dependency references a missing source Version.", Href: "#/delivery/designs/" + designID, Kind: "design", ID: designID, EngReqID: d.engID, Major: &major})
			continue
		}
		if target, okTo := vm[to]; !okTo || target.designID != designID {
			major := d.major
			add(ConsistencyFinding{Severity: "blocking_error", Code: "version_dependency_missing", Message: "Version dependency references a missing or cross-Design Version.", Href: "#/delivery/designs/" + designID, Kind: "design", ID: designID, EngReqID: d.engID, Major: &major})
			continue
		}
		graph[from] = append(graph[from], to)
	}
	state := map[string]uint8{}
	cyclicDesign := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, to := range graph[id] {
			if visit(to) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	ids := make([]string, 0, len(vm))
	for id := range vm {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if visit(id) {
			cyclicDesign[vm[id].designID] = true
		}
	}
	for _, d := range designs {
		if cyclicDesign[d.id] && scope.matches(d.engID, d.major, "") {
			major := d.major
			add(ConsistencyFinding{Severity: "blocking_error", Code: "version_dependency_cycle", Message: "Design Version dependency graph contains a cycle.", Href: "#/delivery/designs/" + d.id, Kind: "design", ID: d.id, EngReqID: d.engID, Major: &major})
		}
	}
	return rows.Err()
}
