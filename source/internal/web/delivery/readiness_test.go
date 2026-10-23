// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"strings"
	"testing"
)

func TestConsistencyScanEmptyIsInformational(t *testing.T) {
	s := openTestStore(t)
	report, err := s.ScanConsistency(ReadinessScope{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.BlockingErrors != 0 || report.Summary.Warnings != 0 ||
		!findingCode(report, "consistency_scan_complete") {
		t.Fatalf("unexpected empty report: %+v", report)
	}
}

func TestConsistencyScanAcceptsAConsistentRecord(t *testing.T) {
	f := newPreflightFixtureWithGate(t, "consistent", "- [x] complete")
	major := f.major
	report, err := f.s.ScanConsistency(ReadinessScope{EngReqID: f.engID, Major: &major})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.BlockingErrors != 0 || report.Summary.Warnings != 0 {
		t.Fatalf("consistent record reported defects: %+v", report.Findings)
	}
}

func TestConsistencyScanFindsInvariantsAndSurvivesDamage(t *testing.T) {
	s := openTestStore(t)
	_, err := s.db.Exec(`
INSERT INTO delivery_majors(major,title,status,summary,created_at,updated_at) VALUES(2,'2.x','sealed','','t','t');
INSERT INTO delivery_eng_reqs(id,worker_id,title,reqs_rel,status,created_at,updated_at,project_id,project_key,workspace_id)
VALUES('project-a','','A','','active','t','t','','','');
INSERT INTO delivery_req_versions(eng_req_id,major,minor,patch,title,status,problem,goals,non_goals,constraints_text,success,updated_at)
VALUES('project-a',2,1,1,'A','awaiting_development','','','','','','t');
INSERT INTO delivery_milestones(id,title,status,acceptance,x,y,sort_order)
VALUES('ms_project-a_2_1','A','awaiting_development','',0,0,1);
INSERT INTO delivery_designs(id,eng_req_id,major,minor,patch,title,status,chosen_option_id,adr,updated_at)
VALUES('design-a','project-a',2,1,1,'A','approved','','','t');
INSERT INTO delivery_design_versions(id,design_id,sort_order,title,gate,sub_architecture,class_design,status,updated_at)
VALUES('version-a1','design-a',1,'one','plain gate text','s','c','ready','t'),
      ('version-a2','design-a',2,'two','- [x] done','s','c','ready','t');
INSERT INTO delivery_design_version_deps(design_id,version_id,depends_on_id)
VALUES('design-a','version-a1','version-a2'),('design-a','version-a2','version-a1');
`)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.ScanConsistency(ReadinessScope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{
		"version_dependency_cycle", "gate_without_checkboxes",
		"sealed_generation_open_milestone", "consistency_scan_complete",
	} {
		if !findingCode(report, code) {
			t.Fatalf("missing %s: %+v", code, report.Findings)
		}
	}

	// Disable FK enforcement only to model a partially damaged historical DB.
	if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
INSERT INTO delivery_designs(id,eng_req_id,major,minor,patch,title,status,chosen_option_id,adr,updated_at)
VALUES('orphan','missing-project',9,9,1,'orphan','draft','','','t');
INSERT INTO delivery_design_version_deps(design_id,version_id,depends_on_id)
VALUES('design-a','version-a1','missing-version')`); err != nil {
		t.Fatal(err)
	}
	report, err = s.ScanConsistency(ReadinessScope{})
	if err != nil {
		t.Fatal(err)
	}
	if !findingCode(report, "orphaned_design") || !findingCode(report, "version_dependency_missing") {
		t.Fatalf("damaged references not reported: %+v", report.Findings)
	}
}

func TestConsistencyScanIsScopeSafe(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`
INSERT INTO delivery_designs(id,eng_req_id,major,minor,patch,title,status,chosen_option_id,adr,updated_at)
VALUES('orphan-a','project-a',1,1,1,'A','draft','','','t'),
      ('orphan-b','project-b',1,1,1,'B','draft','','','t')`); err != nil {
		t.Fatal(err)
	}
	major := 1
	report, err := s.ScanConsistency(ReadinessScope{EngReqID: "project-a", Major: &major})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Findings {
		if f.EngReqID == "project-b" || strings.Contains(f.Href, "orphan-b") {
			t.Fatalf("cross-scope finding leaked: %+v", f)
		}
	}
	if !findingCode(report, "orphaned_design") {
		t.Fatalf("expected scoped orphan: %+v", report.Findings)
	}
}

func TestReadinessUsesPreflightResult(t *testing.T) {
	f := newPreflightFixtureWithGate(t, "readiness", "- [ ] fix me")
	major := f.major
	report, err := f.s.Readiness(ReadinessScope{EngReqID: f.engID, Major: &major, MilestoneID: f.msID})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("want one milestone row, got %+v", report.Rows)
	}
	pf, err := f.s.Preflight(TransitionStartPhase, f.msID)
	if err != nil {
		t.Fatal(err)
	}
	if blockerMessage(pf, "gate_open") != blockerMessage(PreflightResult{Blockers: report.Rows[0].Blockers}, "gate_open") {
		t.Fatalf("readiness drifted from preflight: row=%+v preflight=%+v", report.Rows[0], pf)
	}
}

func findingCode(report ConsistencyReport, code string) bool {
	for _, f := range report.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
