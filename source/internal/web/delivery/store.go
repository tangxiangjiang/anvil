// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package delivery is the anvil-web Delivery domain store (milestone 0.33).
package delivery

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Store persists Delivery entities in the shared anvil-web SQLite DB.
type Store struct {
	db *sql.DB
}

// Open migrates delivery_* tables on an existing DB connection.
func Open(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("delivery: nil db")
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS delivery_eng_reqs (
  id TEXT PRIMARY KEY,
  worker_id TEXT NOT NULL UNIQUE,
  worker_label TEXT NOT NULL DEFAULT '',
  fleet TEXT NOT NULL DEFAULT '',
  node TEXT NOT NULL DEFAULT '',
  project_rel TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  reqs_rel TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  project_id TEXT NOT NULL DEFAULT '',
  project_key TEXT NOT NULL DEFAULT '',
  workspace_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS delivery_req_versions (
  eng_req_id TEXT NOT NULL REFERENCES delivery_eng_reqs(id) ON DELETE CASCADE,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  patch INTEGER NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'draft',
  problem TEXT NOT NULL DEFAULT '',
  goals TEXT NOT NULL DEFAULT '',
  non_goals TEXT NOT NULL DEFAULT '',
  constraints_text TEXT NOT NULL DEFAULT '',
  success TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (eng_req_id, major, minor, patch)
);
CREATE TABLE IF NOT EXISTS delivery_designs (
  id TEXT PRIMARY KEY,
  eng_req_id TEXT NOT NULL,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  patch INTEGER NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'draft',
  chosen_option_id TEXT NOT NULL DEFAULT '',
  adr TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  adr_context TEXT NOT NULL DEFAULT '',
  adr_decision TEXT NOT NULL DEFAULT '',
  adr_consequences TEXT NOT NULL DEFAULT '',
  adr_spec TEXT NOT NULL DEFAULT '',
  adr_architecture TEXT NOT NULL DEFAULT '',
  adr_sequence TEXT NOT NULL DEFAULT '',
  adr_sample TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS delivery_design_options (
  id TEXT PRIMARY KEY,
  design_id TEXT NOT NULL REFERENCES delivery_designs(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  tradeoffs TEXT NOT NULL DEFAULT '',
  risks TEXT NOT NULL DEFAULT '',
  out_of_scope TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS delivery_design_versions (
  id TEXT PRIMARY KEY,
  design_id TEXT NOT NULL REFERENCES delivery_designs(id) ON DELETE CASCADE,
  sort_order INTEGER NOT NULL DEFAULT 1,
  title TEXT NOT NULL DEFAULT '',
  gate TEXT NOT NULL DEFAULT '',
  sub_architecture TEXT NOT NULL DEFAULT '',
  class_design TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS delivery_design_version_deps (
  design_id TEXT NOT NULL,
  version_id TEXT NOT NULL REFERENCES delivery_design_versions(id) ON DELETE CASCADE,
  depends_on_id TEXT NOT NULL REFERENCES delivery_design_versions(id) ON DELETE CASCADE,
  PRIMARY KEY (version_id, depends_on_id)
);
CREATE TABLE IF NOT EXISTS delivery_milestones (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'planned',
  acceptance TEXT NOT NULL DEFAULT '',
  x REAL NOT NULL DEFAULT 0,
  y REAL NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0,
  execute_worker_id TEXT NOT NULL DEFAULT '',
  run_state TEXT NOT NULL DEFAULT 'idle',
  test_note TEXT NOT NULL DEFAULT '',
  campaign_job_id TEXT NOT NULL DEFAULT '',
  campaign_job_status TEXT NOT NULL DEFAULT '',
  campaign_job_kind TEXT NOT NULL DEFAULT '',
  campaign_job_last_event TEXT NOT NULL DEFAULT '',
  accept_status TEXT NOT NULL DEFAULT '',
  accepted_at TEXT NOT NULL DEFAULT '',
  merge_status TEXT NOT NULL DEFAULT '',
  merge_note TEXT NOT NULL DEFAULT '',
  merged_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS delivery_buglist_items (
  id TEXT PRIMARY KEY,
  eng_req_id TEXT NOT NULL,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'open',
  created_at TEXT NOT NULL,
  job_id TEXT NOT NULL DEFAULT '',
  job_status TEXT NOT NULL DEFAULT '',
  job_last_event TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS delivery_test_cases (
  id TEXT PRIMARY KEY,
  eng_req_id TEXT NOT NULL,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'ready',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS delivery_polishes (
  id TEXT PRIMARY KEY,
  eng_req_id TEXT NOT NULL,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'ready',
  created_at TEXT NOT NULL,
  job_id TEXT NOT NULL DEFAULT '',
  job_status TEXT NOT NULL DEFAULT '',
  job_last_event TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS delivery_phases (
  id TEXT PRIMARY KEY,
  milestone_id TEXT NOT NULL REFERENCES delivery_milestones(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'planned',
  sort_order INTEGER NOT NULL DEFAULT 0,
  job_id TEXT NOT NULL DEFAULT '',
  job_status TEXT NOT NULL DEFAULT '',
  job_last_event TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS delivery_edges (
  from_id TEXT NOT NULL,
  to_id TEXT NOT NULL,
  PRIMARY KEY (from_id, to_id)
);
CREATE TABLE IF NOT EXISTS delivery_gates (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  ref_type TEXT NOT NULL,
  ref_id TEXT NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  href TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS delivery_majors (
  major INTEGER PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'planning',
  summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS delivery_trash (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  payload TEXT NOT NULL,
  deleted_at TEXT NOT NULL
);
`)
	if err != nil {
		return err
	}
	if err := s.ensureDesignOptionOutOfScope(); err != nil {
		return err
	}
	if err := s.ensureEngReqProjectColumns(); err != nil {
		return err
	}
	if err := s.ensureDesignADRColumns(); err != nil {
		return err
	}
	if err := s.ensureDesignVersionsTable(); err != nil {
		return err
	}
	if err := s.ensureDesignVersionDepsTable(); err != nil {
		return err
	}
	if err := s.ensureMilestoneExecuteWorker(); err != nil {
		return err
	}
	if err := s.ensureMilestoneRunState(); err != nil {
		return err
	}
	if err := s.ensureMilestoneTestNote(); err != nil {
		return err
	}
	if err := s.ensureBuglistTable(); err != nil {
		return err
	}
	if err := s.ensureTestCasesTable(); err != nil {
		return err
	}
	if err := s.ensurePhaseJobColumns(); err != nil {
		return err
	}
	if err := s.ensureCampaignJobColumns(); err != nil {
		return err
	}
	if err := s.ensureBuglistJobColumns(); err != nil {
		return err
	}
	if err := s.ensurePolishJobColumns(); err != nil {
		return err
	}
	if err := s.ensureMilestoneAcceptColumns(); err != nil {
		return err
	}
	if err := s.ensurePolishesTable(); err != nil {
		return err
	}
	// p_0.46.10: keep the work.
	if err := s.ensureJobEventTables(); err != nil {
		return err
	}
	if err := s.ensureAuditTable(); err != nil {
		return err
	}
	return s.ensureTrashProvenance()
}

func (s *Store) ensureMilestoneAcceptColumns() error {
	for _, q := range []string{
		`ALTER TABLE delivery_milestones ADD COLUMN accept_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_milestones ADD COLUMN accepted_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_milestones ADD COLUMN merge_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_milestones ADD COLUMN merge_note TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_milestones ADD COLUMN merged_at TEXT NOT NULL DEFAULT ''`,
	} {
		_, err := s.db.Exec(q)
		if err == nil {
			continue
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate column") {
			continue
		}
		return err
	}
	return nil
}

func (s *Store) ensurePhaseJobColumns() error {
	for _, q := range []string{
		`ALTER TABLE delivery_phases ADD COLUMN job_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_phases ADD COLUMN job_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_phases ADD COLUMN job_last_event TEXT NOT NULL DEFAULT ''`,
	} {
		_, err := s.db.Exec(q)
		if err == nil {
			continue
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate column") {
			continue
		}
		return err
	}
	return nil
}

func (s *Store) ensureCampaignJobColumns() error {
	for _, q := range []string{
		`ALTER TABLE delivery_milestones ADD COLUMN campaign_job_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_milestones ADD COLUMN campaign_job_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_milestones ADD COLUMN campaign_job_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_milestones ADD COLUMN campaign_job_last_event TEXT NOT NULL DEFAULT ''`,
	} {
		_, err := s.db.Exec(q)
		if err == nil {
			continue
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate column") {
			continue
		}
		return err
	}
	return nil
}

func (s *Store) ensureBuglistJobColumns() error {
	for _, q := range []string{
		`ALTER TABLE delivery_buglist_items ADD COLUMN job_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_buglist_items ADD COLUMN job_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_buglist_items ADD COLUMN job_last_event TEXT NOT NULL DEFAULT ''`,
	} {
		_, err := s.db.Exec(q)
		if err == nil {
			continue
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate column") {
			continue
		}
		return err
	}
	return nil
}

func (s *Store) ensurePolishJobColumns() error {
	for _, q := range []string{
		`ALTER TABLE delivery_polishes ADD COLUMN job_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_polishes ADD COLUMN job_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_polishes ADD COLUMN job_last_event TEXT NOT NULL DEFAULT ''`,
	} {
		_, err := s.db.Exec(q)
		if err == nil {
			continue
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate column") {
			continue
		}
		return err
	}
	return nil
}

// ensureMilestoneExecuteWorker adds execute_worker_id on older DBs (0.39.1).
func (s *Store) ensureMilestoneExecuteWorker() error {
	_, err := s.db.Exec(`ALTER TABLE delivery_milestones ADD COLUMN execute_worker_id TEXT NOT NULL DEFAULT ''`)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column") {
		return nil
	}
	return err
}

func (s *Store) ensureMilestoneRunState() error {
	_, err := s.db.Exec(`ALTER TABLE delivery_milestones ADD COLUMN run_state TEXT NOT NULL DEFAULT 'idle'`)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column") {
		return nil
	}
	return err
}

func (s *Store) ensureMilestoneTestNote() error {
	_, err := s.db.Exec(`ALTER TABLE delivery_milestones ADD COLUMN test_note TEXT NOT NULL DEFAULT ''`)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column") {
		return nil
	}
	return err
}

func (s *Store) ensureBuglistTable() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS delivery_buglist_items (
  id TEXT PRIMARY KEY,
  eng_req_id TEXT NOT NULL,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'open',
  created_at TEXT NOT NULL
)`)
	return err
}

func (s *Store) ensureTestCasesTable() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS delivery_test_cases (
  id TEXT PRIMARY KEY,
  eng_req_id TEXT NOT NULL,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'ready',
  created_at TEXT NOT NULL
)`)
	return err
}

func (s *Store) ensurePolishesTable() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS delivery_polishes (
  id TEXT PRIMARY KEY,
  eng_req_id TEXT NOT NULL,
  major INTEGER NOT NULL,
  minor INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'ready',
  created_at TEXT NOT NULL
)`)
	return err
}

// ensureDesignVersionsTable creates delivery_design_versions on older DBs.
func (s *Store) ensureDesignVersionsTable() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS delivery_design_versions (
  id TEXT PRIMARY KEY,
  design_id TEXT NOT NULL REFERENCES delivery_designs(id) ON DELETE CASCADE,
  sort_order INTEGER NOT NULL DEFAULT 1,
  title TEXT NOT NULL DEFAULT '',
  gate TEXT NOT NULL DEFAULT '',
  sub_architecture TEXT NOT NULL DEFAULT '',
  class_design TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft',
  updated_at TEXT NOT NULL
)`)
	return err
}

// ensureDesignVersionDepsTable creates Version depends_on edges on older DBs.
func (s *Store) ensureDesignVersionDepsTable() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS delivery_design_version_deps (
  design_id TEXT NOT NULL,
  version_id TEXT NOT NULL REFERENCES delivery_design_versions(id) ON DELETE CASCADE,
  depends_on_id TEXT NOT NULL REFERENCES delivery_design_versions(id) ON DELETE CASCADE,
  PRIMARY KEY (version_id, depends_on_id)
)`)
	return err
}

// ensureDesignOptionOutOfScope adds out_of_scope on existing DBs (CREATE IF NOT EXISTS skips alters).
func (s *Store) ensureDesignOptionOutOfScope() error {
	_, err := s.db.Exec(`ALTER TABLE delivery_design_options ADD COLUMN out_of_scope TEXT NOT NULL DEFAULT ''`)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column") {
		return nil
	}
	return err
}

// ensureEngReqProjectColumns adds Project-scoped Delivery fields (0.34+).
func (s *Store) ensureEngReqProjectColumns() error {
	for _, col := range []string{
		`ALTER TABLE delivery_eng_reqs ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_eng_reqs ADD COLUMN project_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_eng_reqs ADD COLUMN workspace_id TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(col); err != nil {
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "duplicate column") {
				return err
			}
		}
	}
	_, _ = s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_eng_project ON delivery_eng_reqs(project_id) WHERE project_id != ''`)
	return nil
}

// ensureDesignADRColumns adds structured ADR markdown sections on delivery_designs.
func (s *Store) ensureDesignADRColumns() error {
	for _, col := range []string{
		`ALTER TABLE delivery_designs ADD COLUMN adr_context TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_designs ADD COLUMN adr_decision TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_designs ADD COLUMN adr_consequences TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_designs ADD COLUMN adr_spec TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_designs ADD COLUMN adr_architecture TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_designs ADD COLUMN adr_sequence TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_designs ADD COLUMN adr_sample TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(col); err != nil {
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "duplicate column") {
				return err
			}
		}
	}
	return nil
}

// Major statuses for generation planning.
const (
	MajorPlanning   = "planning"
	MajorDeveloping = "developing"
	MajorSealed     = "sealed"
)

// MajorPlan is one product generation (major version line).
type MajorPlan struct {
	Major     int    `json:"major"`
	Title     string `json:"title"`
	Status    string `json:"status"` // planning | developing | sealed
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// WorkerRef is denormalized worker identity for eng-req binding.
type WorkerRef struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Fleet      string `json:"fleet"`
	Node       string `json:"node"`
	ProjectRel string `json:"project_rel"`
}

// ReqVersion is one major.minor.patch slice.
type ReqVersion struct {
	Major       int    `json:"major"`
	Minor       int    `json:"minor"`
	Patch       int    `json:"patch"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Problem     string `json:"problem"`
	Goals       string `json:"goals"`
	NonGoals    string `json:"non_goals"`
	Constraints string `json:"constraints"`
	Success     string `json:"success"`
	UpdatedAt   string `json:"updated_at"`
}

// EngReq is a Project-bound engineering requirement (Worker optional for execute/path).
type EngReq struct {
	ID          string       `json:"id"`
	WorkerID    string       `json:"worker_id"`
	ProjectID   string       `json:"project_id,omitempty"`
	ProjectKey  string       `json:"project_key,omitempty"`
	WorkspaceID string       `json:"workspace_id,omitempty"`
	Title       string       `json:"title"`
	ReqsRel     string       `json:"reqs_rel"`
	Status      string       `json:"status"`
	Versions    []ReqVersion `json:"versions"`
	CreatedAt   string       `json:"created_at,omitempty"`
	UpdatedAt   string       `json:"updated_at,omitempty"`
	WorkerMeta  WorkerRef    `json:"-"`
}

// DesignOption is one ADR option card.
type DesignOption struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Tradeoffs  string `json:"tradeoffs"`
	Risks      string `json:"risks"`
	OutOfScope string `json:"out_of_scope"`
}

// DesignVersion is one AI execution boundary under a Design (Gate + Sub-architecture + Class design).
type DesignVersion struct {
	ID              string   `json:"id"`
	SortOrder       int      `json:"sort_order"`
	Title           string   `json:"title"`
	Gate            string   `json:"gate"`
	SubArchitecture string   `json:"sub_architecture"`
	ClassDesign     string   `json:"class_design"`
	Status          string   `json:"status"`               // draft | ready
	DependsOn       []string `json:"depends_on,omitempty"` // prerequisite Version ids
	UpdatedAt       string   `json:"updated_at"`
}

func designVersionReady(v DesignVersion) bool {
	return strings.TrimSpace(v.Gate) != "" &&
		strings.TrimSpace(v.SubArchitecture) != "" &&
		strings.TrimSpace(v.ClassDesign) != ""
}

func (v *DesignVersion) refreshStatus() {
	if v == nil {
		return
	}
	if designVersionReady(*v) {
		v.Status = "ready"
	} else {
		v.Status = "draft"
	}
}

// Design is bound to an eng-req version.
type Design struct {
	ID              string          `json:"id"`
	EngReqID        string          `json:"eng_req_id"`
	Version         string          `json:"version"`
	Major           int             `json:"major"`
	Minor           int             `json:"minor"`
	Patch           int             `json:"patch"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	ChosenOptionID  string          `json:"chosen_option_id"` // legacy; unused for Approve
	ADR             string          `json:"adr"`              // composed export / legacy summary
	AdrContext      string          `json:"adr_context"`
	AdrDecision     string          `json:"adr_decision"`
	AdrConsequences string          `json:"adr_consequences"`
	AdrSpec         string          `json:"adr_spec"`
	AdrArchitecture string          `json:"adr_architecture"`
	AdrSequence     string          `json:"adr_sequence"`
	AdrSample       string          `json:"adr_sample"`
	Options         []DesignOption  `json:"options"`  // legacy; no longer required
	Versions        []DesignVersion `json:"versions"` // ordered execution units
	UpdatedAt       string          `json:"updated_at"`
}

func (d *Design) hydrateADRFallback() {
	if d == nil {
		return
	}
	emptySections := d.AdrContext == "" && d.AdrDecision == "" && d.AdrConsequences == "" &&
		d.AdrSpec == "" && d.AdrArchitecture == "" && d.AdrSequence == "" && d.AdrSample == ""
	if emptySections && strings.TrimSpace(d.ADR) != "" {
		d.AdrDecision = d.ADR
	}
}

func composeADR(d Design) string {
	var b strings.Builder
	write := func(title, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(title)
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	write("Context", d.AdrContext)
	write("Decision", d.AdrDecision)
	write("Consequences", d.AdrConsequences)
	write("Spec", d.AdrSpec)
	write("Architecture", d.AdrArchitecture)
	write("Sequence", d.AdrSequence)
	write("Sample & capacity", d.AdrSample)
	if b.Len() == 0 {
		return strings.TrimSpace(d.ADR)
	}
	return b.String()
}

const designSelectCols = `id, eng_req_id, major, minor, patch, title, status, chosen_option_id, adr, updated_at,
  adr_context, adr_decision, adr_consequences, adr_spec, adr_architecture, adr_sequence, adr_sample`

func scanDesign(scanner interface {
	Scan(dest ...any) error
}, d *Design) error {
	err := scanner.Scan(
		&d.ID, &d.EngReqID, &d.Major, &d.Minor, &d.Patch,
		&d.Title, &d.Status, &d.ChosenOptionID, &d.ADR, &d.UpdatedAt,
		&d.AdrContext, &d.AdrDecision, &d.AdrConsequences, &d.AdrSpec,
		&d.AdrArchitecture, &d.AdrSequence, &d.AdrSample,
	)
	if err != nil {
		return err
	}
	d.Version = fmt.Sprintf("%d.%d.%d", d.Major, d.Minor, d.Patch)
	d.hydrateADRFallback()
	return nil
}

// Phase job_status values (0.39.5+).
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobFinished  = "finished"
	JobError     = "error"
	JobCancelled = "cancelled"
)

// Phase is a step under a milestone.
type Phase struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	JobID        string `json:"job_id,omitempty"`
	JobStatus    string `json:"job_status,omitempty"`
	JobLastEvent string `json:"job_last_event,omitempty"`
}

// Run-state values for milestone execute controls (pre-Job).
const (
	RunIdle    = "idle"
	RunRunning = "running"
	RunPaused  = "paused"
	RunStopped = "stopped"
)

// Campaign job kinds on a milestone (0.39.8).
const (
	CampaignKindRun     = "run"
	CampaignKindRegress = "regress"
)

// Milestone accept_status values (0.39.12).
const (
	AcceptNone     = ""
	AcceptPending  = "pending"
	AcceptAccepted = "accepted"
)

// Milestone is a roadmap canvas node.
type Milestone struct {
	ID                   string  `json:"id"`
	Title                string  `json:"title"`
	Status               string  `json:"status"`
	Acceptance           string  `json:"acceptance"`
	X                    float64 `json:"x"`
	Y                    float64 `json:"y"`
	ExecuteWorkerID      string  `json:"execute_worker_id"`
	RunState             string  `json:"run_state"`
	TestNote             string  `json:"test_note"`
	CampaignJobID        string  `json:"campaign_job_id,omitempty"`
	CampaignJobStatus    string  `json:"campaign_job_status,omitempty"`
	CampaignJobKind      string  `json:"campaign_job_kind,omitempty"`
	CampaignJobLastEvent string  `json:"campaign_job_last_event,omitempty"`
	AcceptStatus         string  `json:"accept_status,omitempty"`
	AcceptedAt           string  `json:"accepted_at,omitempty"`
	MergeStatus          string  `json:"merge_status,omitempty"`
	MergeNote            string  `json:"merge_note,omitempty"`
	MergedAt             string  `json:"merged_at,omitempty"`
	Phases               []Phase `json:"phases"`
}

// Polish is a post-complete requirement polish item under a milestone.
type Polish struct {
	ID           string `json:"id"`
	EngReqID     string `json:"eng_req_id"`
	Major        int    `json:"major"`
	Minor        int    `json:"minor"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	JobID        string `json:"job_id,omitempty"`
	JobStatus    string `json:"job_status,omitempty"`
	JobLastEvent string `json:"job_last_event,omitempty"`
}

// BuglistItem is a test-fail / campaign bug bound to a milestone (eng_req + major.minor).
type BuglistItem struct {
	ID           string `json:"id"`
	EngReqID     string `json:"eng_req_id"`
	Major        int    `json:"major"`
	Minor        int    `json:"minor"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Status       string `json:"status"` // open | fixing | verified | closed
	CreatedAt    string `json:"created_at"`
	JobID        string `json:"job_id,omitempty"`
	JobStatus    string `json:"job_status,omitempty"`
	JobLastEvent string `json:"job_last_event,omitempty"`
}

// TestCase is a campaign test case under a milestone bind.
type TestCase struct {
	ID        string `json:"id"`
	EngReqID  string `json:"eng_req_id"`
	Major     int    `json:"major"`
	Minor     int    `json:"minor"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"` // draft | ready | passed | failed
	CreatedAt string `json:"created_at"`
}

// Edge is a milestone dependency.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Gate is a pending approval item.
type Gate struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	RefType   string `json:"ref_type"`
	RefID     string `json:"ref_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Href      string `json:"href"`
	CreatedAt string `json:"created_at"`
}

// Roadmap is the graph payload.
type Roadmap struct {
	Milestones []Milestone `json:"milestones"`
	Edges      []Edge      `json:"edges"`
}

// Snapshot is the UI hydrate payload.
type Snapshot struct {
	Majors       []MajorPlan   `json:"majors"`
	TrashCount   int           `json:"trash_count"`
	Workers      []WorkerRef   `json:"workers"`
	EngReqs      []EngReq      `json:"eng_reqs"`
	Designs      []Design      `json:"designs"`
	Roadmap      Roadmap       `json:"roadmap"`
	Gates        []Gate        `json:"gates"`
	Polishes     []Polish      `json:"polishes"`
	BuglistItems []BuglistItem `json:"buglist_items"`
	TestCases    []TestCase    `json:"test_cases"`
}

// Snapshot loads the full Delivery graph for the UI.
func (s *Store) Snapshot() (Snapshot, error) {
	var out Snapshot
	majors, err := s.ListMajors()
	if err != nil {
		return out, err
	}
	out.Majors = majors
	engs, err := s.listEngReqs()
	if err != nil {
		return out, err
	}
	out.EngReqs = engs
	out.Workers = workersFromEngs(engs)
	designs, err := s.listDesigns()
	if err != nil {
		return out, err
	}
	out.Designs = designs
	rm, err := s.loadRoadmap()
	if err != nil {
		return out, err
	}
	out.Roadmap = rm
	gates, err := s.listGates()
	if err != nil {
		return out, err
	}
	out.Gates = gates
	polishes, err := s.listPolishes()
	if err != nil {
		return out, err
	}
	out.Polishes = polishes
	bugs, err := s.listBuglistItems()
	if err != nil {
		return out, err
	}
	out.BuglistItems = bugs
	cases, err := s.listTestCases()
	if err != nil {
		return out, err
	}
	out.TestCases = cases
	var trashN int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM delivery_trash`).Scan(&trashN)
	out.TrashCount = trashN
	return out, nil
}

// ListMajors returns generation plans ordered by major.
func (s *Store) ListMajors() ([]MajorPlan, error) {
	rows, err := s.db.Query(`
SELECT major, title, status, summary, created_at, updated_at
FROM delivery_majors ORDER BY major`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []MajorPlan
	for rows.Next() {
		var m MajorPlan
		if err := rows.Scan(&m.Major, &m.Title, &m.Status, &m.Summary, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	if list == nil {
		list = []MajorPlan{}
	}
	return list, rows.Err()
}

func normalizeMajorStatus(st string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(st)) {
	case MajorPlanning, "plan", "planned":
		return MajorPlanning, nil
	case MajorDeveloping, "dev", "in_development", "in-development":
		return MajorDeveloping, nil
	case MajorSealed, "frozen", "locked", "封板":
		return MajorSealed, nil
	default:
		return "", fmt.Errorf("status must be planning, developing, or sealed")
	}
}

// AddMajor inserts a generation plan. New majors always start as Planning.
func (s *Store) AddMajor(major int, title, status, summary string) (MajorPlan, error) {
	if major < 0 {
		return MajorPlan{}, fmt.Errorf("major must be >= 0")
	}
	_ = status // ignored — always planning
	if strings.TrimSpace(title) == "" {
		if major == 0 {
			title = "0.x"
		} else {
			title = fmt.Sprintf("%d.0", major)
		}
	}
	ts := nowRFC3339()
	_, err := s.db.Exec(`
INSERT INTO delivery_majors(major, title, status, summary, created_at, updated_at)
VALUES(?,?,?,?,?,?)`, major, title, MajorPlanning, summary, ts, ts)
	if err != nil {
		return MajorPlan{}, fmt.Errorf("add major: %w", err)
	}
	return MajorPlan{Major: major, Title: title, Status: MajorPlanning, Summary: summary, CreatedAt: ts, UpdatedAt: ts}, nil
}

// NextMajorStatus returns the next status in planning → developing → sealed, or "".
func NextMajorStatus(current string) string {
	switch current {
	case MajorPlanning:
		return MajorDeveloping
	case MajorDeveloping:
		return MajorSealed
	default:
		return ""
	}
}

// CanStartMajor reports whether planning → developing is allowed for major.
// Requires the previous major (major-1) to exist and be sealed. Major 0 has no predecessor.
func (s *Store) CanStartMajor(major int) (ok bool, reason string, err error) {
	if major <= 0 {
		return true, "", nil
	}
	prev, err := s.getMajor(major - 1)
	if err != nil {
		return false, fmt.Sprintf("previous major %d is missing — seal the prior generation first", major-1), nil
	}
	if prev.Status != MajorSealed {
		return false, fmt.Sprintf("previous major %s must be Sealed before starting %s",
			majorLabel(major-1), majorLabel(major)), nil
	}
	return true, "", nil
}

// AdvanceMajor moves status one step: planning → developing → sealed.
func (s *Store) AdvanceMajor(major int) (MajorPlan, error) {
	m, err := s.getMajor(major)
	if err != nil {
		return MajorPlan{}, err
	}
	next := NextMajorStatus(m.Status)
	if next == "" {
		return MajorPlan{}, fmt.Errorf("major %d is already sealed — no further transition", major)
	}
	if next == MajorDeveloping {
		ok, reason, err := s.CanStartMajor(major)
		if err != nil {
			return MajorPlan{}, err
		}
		if !ok {
			return MajorPlan{}, fmt.Errorf("%s", reason)
		}
	}
	m.Status = next
	m.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`UPDATE delivery_majors SET status=?, updated_at=? WHERE major=?`, m.Status, m.UpdatedAt, major)
	if err != nil {
		return MajorPlan{}, err
	}
	return m, nil
}

// UpdateMajor patches title/summary on planning/developing majors. Sealed is read-only for fields.
// Status may only advance one step (same as AdvanceMajor).
func (s *Store) UpdateMajor(major int, title, status, summary *string) (MajorPlan, error) {
	m, err := s.getMajor(major)
	if err != nil {
		return MajorPlan{}, err
	}
	if (title != nil || summary != nil) && m.Status == MajorSealed {
		return MajorPlan{}, fmt.Errorf("sealed majors are read-only")
	}
	if title != nil {
		m.Title = strings.TrimSpace(*title)
	}
	if summary != nil {
		m.Summary = *summary
	}
	if status != nil {
		st, err := normalizeMajorStatus(*status)
		if err != nil {
			return MajorPlan{}, err
		}
		next := NextMajorStatus(m.Status)
		if st != next {
			return MajorPlan{}, fmt.Errorf("status only advances planning → developing → sealed (next is %s)", nextOrNone(next))
		}
		m.Status = st
	}
	m.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`UPDATE delivery_majors SET title=?, status=?, summary=?, updated_at=? WHERE major=?`,
		m.Title, m.Status, m.Summary, m.UpdatedAt, major)
	if err != nil {
		return MajorPlan{}, err
	}
	return m, nil
}

func nextOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func (s *Store) getMajor(major int) (MajorPlan, error) {
	var m MajorPlan
	err := s.db.QueryRow(`
SELECT major, title, status, summary, created_at, updated_at FROM delivery_majors WHERE major=?`, major).
		Scan(&m.Major, &m.Title, &m.Status, &m.Summary, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return MajorPlan{}, fmt.Errorf("major %d not found", major)
	}
	return m, nil
}

// DevelopingMajor returns the lowest major with status developing, or -1 if none.
func (s *Store) DevelopingMajor() (int, error) {
	var maj sql.NullInt64
	err := s.db.QueryRow(`
SELECT MIN(major) FROM delivery_majors WHERE status=?`, MajorDeveloping).Scan(&maj)
	if err != nil {
		return -1, err
	}
	if !maj.Valid {
		return -1, nil
	}
	return int(maj.Int64), nil
}

// AssertMajorWritable fails when major is missing or sealed.
func (s *Store) AssertMajorWritable(major int) error {
	m, err := s.getMajor(major)
	if err != nil {
		return fmt.Errorf("major %d is not in the generation plan — add it first", major)
	}
	if m.Status == MajorSealed {
		return fmt.Errorf("major %d is sealed — open a developing generation to add milestones", major)
	}
	return nil
}

// NextMajorNumber returns max(major)+1 (0 if empty).
func (s *Store) NextMajorNumber() (int, error) {
	var max sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(major) FROM delivery_majors`).Scan(&max); err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	return int(max.Int64) + 1, nil
}

func workersFromEngs(engs []EngReq) []WorkerRef {
	seen := map[string]bool{}
	var out []WorkerRef
	for _, e := range engs {
		if e.WorkerMeta.ID == "" || seen[e.WorkerMeta.ID] {
			continue
		}
		seen[e.WorkerMeta.ID] = true
		out = append(out, e.WorkerMeta)
	}
	return out
}

func (s *Store) listEngReqs() ([]EngReq, error) {
	rows, err := s.db.Query(`
SELECT id, worker_id, worker_label, fleet, node, project_rel, title, reqs_rel, status, created_at, updated_at,
       project_id, project_key, workspace_id
FROM delivery_eng_reqs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	var list []EngReq
	for rows.Next() {
		var e EngReq
		if err := rows.Scan(
			&e.ID, &e.WorkerID, &e.WorkerMeta.Label, &e.WorkerMeta.Fleet, &e.WorkerMeta.Node, &e.WorkerMeta.ProjectRel,
			&e.Title, &e.ReqsRel, &e.Status, &e.CreatedAt, &e.UpdatedAt,
			&e.ProjectID, &e.ProjectKey, &e.WorkspaceID,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		e.WorkerMeta.ID = e.WorkerID
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Load versions after closing eng rows — MaxOpenConns(1) cannot nest Queries.
	for i := range list {
		vers, err := s.listVersions(list[i].ID)
		if err != nil {
			return nil, err
		}
		list[i].Versions = vers
	}
	return list, nil
}

func (s *Store) listVersions(engID string) ([]ReqVersion, error) {
	rows, err := s.db.Query(`
SELECT major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at
FROM delivery_req_versions WHERE eng_req_id=? ORDER BY major, minor, patch`, engID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []ReqVersion
	for rows.Next() {
		var v ReqVersion
		if err := rows.Scan(
			&v.Major, &v.Minor, &v.Patch, &v.Title, &v.Status,
			&v.Problem, &v.Goals, &v.NonGoals, &v.Constraints, &v.Success, &v.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

// MilestonePhaseInput is one row in the milestone Phase list (roadmap template).
type MilestonePhaseInput struct {
	Title    string // phase title / delivery headline
	Delivery string // short delivery note (stored on version problem)
}

// CreateEngReqInput creates a Project- or Worker-bound eng-req (or attaches a milestone if already bound).
// Milestone body fields follow doc/roadmaps m_*.md: Goals, Overall acceptance, Non-goals, Linked design, Phase list.
type CreateEngReqInput struct {
	Worker         WorkerRef
	ProjectID      string // preferred bind key (0.34+)
	ProjectKey     string
	WorkspaceID    string
	Title          string
	ReqsRel        string
	ID             string // optional
	Major          int    // generation (0.x allowed); ignored when AutoMajor
	AutoMajor      bool   // pick DevelopingMajor
	Minor          int    // milestone; 0 = auto next
	MilestoneTitle string // version title; defaults to Title
	Status         string // planned | in_progress | done | draft (default planned)
	Goals          string
	NonGoals       string                // unused in Requirements UI; kept for store compat
	Success        string                // Completion criteria (capabilities / features to ship)
	Constraints    string                // unused in Requirements UI; kept for store compat
	Phases         []MilestonePhaseInput // req versions under the milestone (patch 1..N)
	ShellOnly      bool                  // create eng row without first milestone (Project open)
}

// NextMilestone returns the next major.minor for engID (patch always starts at 1).
// major < 0 → use DevelopingMajor() (or 0 if none planned).
// Empty engID / no versions → (major, 1).
func (s *Store) NextMilestone(engID string, major int) (int, int, error) {
	if major < 0 {
		d, err := s.DevelopingMajor()
		if err != nil {
			return 0, 0, err
		}
		if d < 0 {
			major = 0
		} else {
			major = d
		}
	}
	if engID == "" {
		return major, 1, nil
	}
	var maxMinor sql.NullInt64
	err := s.db.QueryRow(`
SELECT MAX(minor) FROM delivery_req_versions WHERE eng_req_id=? AND major=?`, engID, major).Scan(&maxMinor)
	if err != nil {
		return 0, 0, err
	}
	if !maxMinor.Valid {
		return major, 1, nil
	}
	return major, int(maxMinor.Int64) + 1, nil
}

// EngIDByWorker returns the eng-req id bound to workerID, if any.
func (s *Store) EngIDByWorker(workerID string) (id string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT id FROM delivery_eng_reqs WHERE worker_id=?`, workerID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// EngIDByProject returns the eng-req id bound to projectID, if any.
func (s *Store) EngIDByProject(projectID string) (id string, ok bool, err error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", false, nil
	}
	err = s.db.QueryRow(`SELECT id FROM delivery_eng_reqs WHERE project_id=?`, projectID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// EnsureProjectEng opens or creates a Project-scoped eng-req shell (no milestone yet).
func (s *Store) EnsureProjectEng(projectID, workspaceID, projectKey, title string) (EngReq, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return EngReq{}, fmt.Errorf("project_id required")
	}
	if id, ok, err := s.EngIDByProject(projectID); err != nil {
		return EngReq{}, err
	} else if ok {
		return s.GetEngReq(id)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSpace(projectKey)
	}
	if title == "" {
		title = "Untitled project"
	}
	return s.CreateEngReq(CreateEngReqInput{
		ProjectID:   projectID,
		ProjectKey:  projectKey,
		WorkspaceID: workspaceID,
		Title:       title,
		ReqsRel:     "docs/requirements",
		ShellOnly:   true,
		Worker: WorkerRef{
			ID:    "proj:" + projectID,
			Label: title,
		},
	})
}

// GetEngReq loads one eng-req with versions.
func (s *Store) GetEngReq(id string) (EngReq, error) {
	var e EngReq
	err := s.db.QueryRow(`
SELECT id, worker_id, worker_label, fleet, node, project_rel, title, reqs_rel, status, created_at, updated_at,
       project_id, project_key, workspace_id
FROM delivery_eng_reqs WHERE id=?`, id).Scan(
		&e.ID, &e.WorkerID, &e.WorkerMeta.Label, &e.WorkerMeta.Fleet, &e.WorkerMeta.Node, &e.WorkerMeta.ProjectRel,
		&e.Title, &e.ReqsRel, &e.Status, &e.CreatedAt, &e.UpdatedAt,
		&e.ProjectID, &e.ProjectKey, &e.WorkspaceID,
	)
	if err != nil {
		return EngReq{}, fmt.Errorf("eng req not found")
	}
	e.WorkerMeta.ID = e.WorkerID
	vers, err := s.listVersions(e.ID)
	if err != nil {
		return EngReq{}, err
	}
	e.Versions = vers
	return e, nil
}

// CreateEngReq inserts eng-req + first milestone version, or adds the next milestone on an existing bind.
// Milestone id is major.minor (patch=1 for the first phase under that milestone).
func (s *Store) CreateEngReq(in CreateEngReqInput) (EngReq, error) {
	w := in.Worker
	w.ID = strings.TrimSpace(w.ID)
	projectID := strings.TrimSpace(in.ProjectID)
	projectKey := strings.TrimSpace(in.ProjectKey)
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if projectID != "" && w.ID == "" {
		w.ID = "proj:" + projectID
	}
	if w.ID == "" {
		return EngReq{}, fmt.Errorf("project_id or worker_id required")
	}
	if w.Label == "" {
		w.Label = w.ID
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Untitled eng req"
	}
	msTitle := strings.TrimSpace(in.MilestoneTitle)
	if msTitle == "" {
		msTitle = title
	}
	reqs := strings.TrimSpace(in.ReqsRel)
	if reqs == "" {
		reqs = "docs/requirements"
	}

	var existingID string
	var found bool
	var err error
	if projectID != "" {
		existingID, found, err = s.EngIDByProject(projectID)
	} else {
		existingID, found, err = s.EngIDByWorker(w.ID)
	}
	if err != nil {
		return EngReq{}, err
	}
	if found {
		if in.ShellOnly {
			return s.GetEngReq(existingID)
		}
		major := in.Major
		if in.AutoMajor {
			d, err := s.DevelopingMajor()
			if err != nil {
				return EngReq{}, err
			}
			if d < 0 {
				return EngReq{}, fmt.Errorf("no major is in development — set one to developing in the generation plan")
			}
			major = d
		}
		if err := s.AssertMajorWritable(major); err != nil {
			return EngReq{}, err
		}
		minor := in.Minor
		if minor <= 0 {
			_, minor, err = s.NextMilestone(existingID, major)
			if err != nil {
				return EngReq{}, err
			}
		}
		if err := s.insertMilestoneVersions(existingID, major, minor, msTitle, in); err != nil {
			return EngReq{}, err
		}
		ts := nowRFC3339()
		_, _ = s.db.Exec(`UPDATE delivery_eng_reqs SET title=?, reqs_rel=?, worker_label=?, fleet=?, node=?, project_rel=?,
			project_id=CASE WHEN project_id='' THEN ? ELSE project_id END,
			project_key=CASE WHEN project_key='' THEN ? ELSE project_key END,
			workspace_id=CASE WHEN workspace_id='' THEN ? ELSE workspace_id END,
			updated_at=? WHERE id=?`,
			title, reqs, w.Label, w.Fleet, w.Node, w.ProjectRel, projectID, projectKey, workspaceID, ts, existingID)
		return s.GetEngReq(existingID)
	}

	if !in.ShellOnly {
		major := in.Major
		if in.AutoMajor {
			d, err := s.DevelopingMajor()
			if err != nil {
				return EngReq{}, err
			}
			if d < 0 {
				return EngReq{}, fmt.Errorf("no major is in development — set one to developing in the generation plan")
			}
			major = d
		}
		if err := s.AssertMajorWritable(major); err != nil {
			return EngReq{}, err
		}
		_ = major
	}

	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = fmt.Sprintf("eng_%d", time.Now().UnixNano())
	}
	ts := nowRFC3339()
	tx, err := s.db.Begin()
	if err != nil {
		return EngReq{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`
INSERT INTO delivery_eng_reqs(id, worker_id, worker_label, fleet, node, project_rel, title, reqs_rel, status, created_at, updated_at,
  project_id, project_key, workspace_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.ID, w.Label, w.Fleet, w.Node, w.ProjectRel, title, reqs, "active", ts, ts,
		projectID, projectKey, workspaceID)
	if err != nil {
		return EngReq{}, fmt.Errorf("create eng req: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return EngReq{}, err
	}
	if in.ShellOnly {
		return s.GetEngReq(id)
	}
	major := in.Major
	if in.AutoMajor {
		d, err := s.DevelopingMajor()
		if err != nil {
			return EngReq{}, err
		}
		major = d
	}
	minor := in.Minor
	if minor <= 0 {
		_, minor, err = s.NextMilestone("", major)
		if err != nil {
			return EngReq{}, err
		}
	}
	if err := s.insertMilestoneVersions(id, major, minor, msTitle, in); err != nil {
		return EngReq{}, err
	}
	return s.GetEngReq(id)
}

// Milestone lifecycle statuses (per major.minor). Create always starts at awaiting_design.
const (
	LifeAwaitingDesign      = "awaiting_design"
	LifeAwaitingRoadmap     = "awaiting_roadmap"
	LifeAwaitingDevelopment = "awaiting_development"
	LifeInDevelopment       = "in_development"
	LifeTesting             = "testing"
	LifeComplete            = "complete"
)

func normalizeMilestoneStatus(s string) string {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case LifeAwaitingDesign, LifeAwaitingRoadmap, LifeAwaitingDevelopment,
		LifeInDevelopment, LifeTesting, LifeComplete:
		return strings.TrimSpace(strings.ToLower(s))
	case "planned", "draft":
		return LifeAwaitingDesign
	case "in_progress":
		return LifeInDevelopment
	case "done":
		return LifeComplete
	case "approved":
		// Legacy gate mark; treat as past design for display/compat.
		return "approved"
	default:
		return LifeAwaitingDesign
	}
}

// SetMilestoneLifecycle sets status on all versions under eng_req major.minor.
func (s *Store) SetMilestoneLifecycle(engID string, major, minor int, status string) error {
	st := normalizeMilestoneStatus(status)
	switch st {
	case LifeAwaitingDesign, LifeAwaitingRoadmap, LifeAwaitingDevelopment,
		LifeInDevelopment, LifeTesting, LifeComplete:
	default:
		return fmt.Errorf("invalid lifecycle status %q", status)
	}
	if err := s.AssertMajorWritable(major); err != nil {
		return err
	}
	ts := nowRFC3339()
	res, err := s.db.Exec(`
UPDATE delivery_req_versions SET status=?, updated_at=? WHERE eng_req_id=? AND major=? AND minor=?`,
		st, ts, engID, major, minor)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("milestone %d.%d not found", major, minor)
	}
	_, _ = s.db.Exec(`UPDATE delivery_eng_reqs SET updated_at=? WHERE id=?`, ts, engID)
	return nil
}

func cleanMilestonePhases(msTitle string, phases []MilestonePhaseInput) []MilestonePhaseInput {
	var out []MilestonePhaseInput
	for _, p := range phases {
		t := strings.TrimSpace(p.Title)
		d := strings.TrimSpace(p.Delivery)
		if t == "" && d == "" {
			continue
		}
		if t == "" {
			t = d
			d = ""
		}
		out = append(out, MilestonePhaseInput{Title: t, Delivery: d})
	}
	if len(out) == 0 {
		t := strings.TrimSpace(msTitle)
		if t == "" {
			t = "Untitled"
		}
		out = []MilestonePhaseInput{{Title: t}}
	}
	return out
}

// insertMilestoneVersions writes patch 1..N for a milestone from the roadmap template fields.
func (s *Store) insertMilestoneVersions(engID string, major, minor int, msTitle string, in CreateEngReqInput) error {
	if err := s.AssertMajorWritable(major); err != nil {
		return err
	}
	phases := cleanMilestonePhases(msTitle, in.Phases)
	// Create always locks lifecycle at awaiting_design (ignore client status).
	status := LifeAwaitingDesign
	ts := nowRFC3339()
	msName := strings.TrimSpace(msTitle)
	for i, p := range phases {
		patch := i + 1
		goals, nonGoals, constraints, success := "", "", "", ""
		if patch == 1 {
			goals = in.Goals
			nonGoals = in.NonGoals
			constraints = in.Constraints
			success = in.Success
		}
		title := p.Title
		problem := p.Delivery
		if patch == 1 && msName != "" {
			title = msName
			if p.Title != "" && p.Title != msName {
				if problem != "" {
					problem = p.Title + " — " + problem
				} else {
					problem = p.Title
				}
			}
		}
		_, err := s.db.Exec(`
INSERT INTO delivery_req_versions(eng_req_id, major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			engID, major, minor, patch, title, status, problem, goals, nonGoals, constraints, success, ts)
		if err != nil {
			return fmt.Errorf("create milestone phase %d: %w", patch, err)
		}
	}
	_, _ = s.db.Exec(`UPDATE delivery_eng_reqs SET updated_at=? WHERE id=?`, ts, engID)
	return nil
}

// AddVersionInput adds a version node.
type AddVersionInput struct {
	EngReqID string
	Major    int
	Minor    int
	Patch    int
	Title    string
}

// DeleteMilestone removes all phases (patches) under eng_req major.minor.
// Allowed only when the generation major is not sealed.
func (s *Store) DeleteMilestone(engID string, major, minor int) error {
	if err := s.AssertMajorWritable(major); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`
SELECT COUNT(*) FROM delivery_req_versions WHERE eng_req_id=? AND major=? AND minor=?`,
		engID, major, minor).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("milestone %d.%d not found", major, minor)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	drows, err := tx.Query(`
SELECT id FROM delivery_designs WHERE eng_req_id=? AND major=? AND minor=?`, engID, major, minor)
	if err != nil {
		return err
	}
	var designIDs []string
	for drows.Next() {
		var id string
		_ = drows.Scan(&id)
		designIDs = append(designIDs, id)
	}
	_ = drows.Close()
	for _, id := range designIDs {
		if _, err := tx.Exec(`DELETE FROM delivery_design_version_deps WHERE design_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_design_versions WHERE design_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_design_options WHERE design_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_designs WHERE id=?`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
DELETE FROM delivery_req_versions WHERE eng_req_id=? AND major=? AND minor=?`, engID, major, minor); err != nil {
		return err
	}
	ref := fmt.Sprintf("%s:%d.%d.%%", engID, major, minor)
	if _, err := tx.Exec(`DELETE FROM delivery_gates WHERE ref_id LIKE ?`, ref); err != nil {
		return err
	}
	_, _ = tx.Exec(`UPDATE delivery_eng_reqs SET updated_at=? WHERE id=?`, nowRFC3339(), engID)
	return tx.Commit()
}

// DeleteVersion removes one patch under a milestone and renumbers remaining patches 1..n.
// Milestone-level fields (goals/success/…) from the former patch 1 are kept on the new patch 1.
// At least one version must remain.
func (s *Store) DeleteVersion(engID string, major, minor, patch int) error {
	if err := s.AssertMajorWritable(major); err != nil {
		return err
	}
	rows, err := s.db.Query(`
SELECT patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at
FROM delivery_req_versions WHERE eng_req_id=? AND major=? AND minor=? ORDER BY patch`,
		engID, major, minor)
	if err != nil {
		return err
	}
	type row struct {
		PatchN                                                                   int
		Title, Status, Problem, Goals, NonGoals, Constraints, Success, UpdatedAt string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.PatchN, &r.Title, &r.Status, &r.Problem, &r.Goals, &r.NonGoals, &r.Constraints, &r.Success, &r.UpdatedAt); err != nil {
			_ = rows.Close()
			return err
		}
		all = append(all, r)
	}
	_ = rows.Close()
	if len(all) <= 1 {
		return fmt.Errorf("cannot delete the last version of a milestone")
	}
	found := false
	var head row
	var keep []row
	for i, r := range all {
		if i == 0 {
			head = r
		}
		if r.PatchN == patch {
			found = true
			continue
		}
		keep = append(keep, r)
	}
	if !found {
		return fmt.Errorf("version %d.%d.%d not found", major, minor, patch)
	}
	ts := nowRFC3339()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
DELETE FROM delivery_req_versions WHERE eng_req_id=? AND major=? AND minor=?`,
		engID, major, minor); err != nil {
		return err
	}
	for i, r := range keep {
		newPatch := i + 1
		goals, nonGoals, constraints, success := r.Goals, r.NonGoals, r.Constraints, r.Success
		if newPatch == 1 {
			goals, nonGoals, constraints, success = head.Goals, head.NonGoals, head.Constraints, head.Success
		} else {
			goals, nonGoals, constraints, success = "", "", "", ""
		}
		status := r.Status
		if status == "" {
			status = head.Status
		}
		if _, err := tx.Exec(`
INSERT INTO delivery_req_versions(eng_req_id, major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			engID, major, minor, newPatch, r.Title, status, r.Problem, goals, nonGoals, constraints, success, ts); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE delivery_eng_reqs SET updated_at=? WHERE id=?`, ts, engID); err != nil {
		return err
	}
	return tx.Commit()
}

// AddVersion inserts a version inheriting lifecycle status from patch 1 when present.
func (s *Store) AddVersion(in AddVersionInput) (ReqVersion, error) {
	if err := s.AssertMajorWritable(in.Major); err != nil {
		return ReqVersion{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Untitled"
	}
	status := LifeAwaitingDesign
	if head, err := s.getVersion(in.EngReqID, in.Major, in.Minor, 1); err == nil && head.Status != "" {
		status = normalizeMilestoneStatus(head.Status)
	}
	ts := nowRFC3339()
	v := ReqVersion{
		Major: in.Major, Minor: in.Minor, Patch: in.Patch,
		Title: title, Status: status, UpdatedAt: ts,
	}
	_, err := s.db.Exec(`
INSERT INTO delivery_req_versions(eng_req_id, major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.EngReqID, v.Major, v.Minor, v.Patch, v.Title, v.Status, "", "", "", "", "", ts)
	if err != nil {
		return ReqVersion{}, fmt.Errorf("add version: %w", err)
	}
	_, _ = s.db.Exec(`UPDATE delivery_eng_reqs SET updated_at=? WHERE id=?`, ts, in.EngReqID)
	return v, nil
}

// UpdateVersionInput patches structured fields.
type UpdateVersionInput struct {
	EngReqID    string
	Major       int
	Minor       int
	Patch       int
	Title       *string
	Problem     *string
	Goals       *string
	NonGoals    *string
	Constraints *string
	Success     *string
}

// UpdateVersion updates non-nil fields.
func (s *Store) UpdateVersion(in UpdateVersionInput) (ReqVersion, error) {
	v, err := s.getVersion(in.EngReqID, in.Major, in.Minor, in.Patch)
	if err != nil {
		return ReqVersion{}, err
	}
	if in.Title != nil {
		v.Title = strings.TrimSpace(*in.Title)
	}
	if in.Problem != nil {
		v.Problem = *in.Problem
	}
	if in.Goals != nil {
		v.Goals = *in.Goals
	}
	if in.NonGoals != nil {
		v.NonGoals = *in.NonGoals
	}
	if in.Constraints != nil {
		v.Constraints = *in.Constraints
	}
	if in.Success != nil {
		v.Success = *in.Success
	}
	v.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`
UPDATE delivery_req_versions SET title=?, problem=?, goals=?, non_goals=?, constraints_text=?, success=?, updated_at=?
WHERE eng_req_id=? AND major=? AND minor=? AND patch=?`,
		v.Title, v.Problem, v.Goals, v.NonGoals, v.Constraints, v.Success, v.UpdatedAt,
		in.EngReqID, in.Major, in.Minor, in.Patch)
	if err != nil {
		return ReqVersion{}, err
	}
	return v, nil
}

func (s *Store) getVersion(engID string, major, minor, patch int) (ReqVersion, error) {
	var v ReqVersion
	err := s.db.QueryRow(`
SELECT major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at
FROM delivery_req_versions WHERE eng_req_id=? AND major=? AND minor=? AND patch=?`,
		engID, major, minor, patch,
	).Scan(
		&v.Major, &v.Minor, &v.Patch, &v.Title, &v.Status,
		&v.Problem, &v.Goals, &v.NonGoals, &v.Constraints, &v.Success, &v.UpdatedAt,
	)
	if err != nil {
		return ReqVersion{}, fmt.Errorf("version not found")
	}
	return v, nil
}

// ApproveVersion sets status approved and upserts a gate item.
func (s *Store) ApproveVersion(engID string, major, minor, patch int) (ReqVersion, error) {
	v, err := s.getVersion(engID, major, minor, patch)
	if err != nil {
		return ReqVersion{}, err
	}
	v.Status = "approved"
	v.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`
UPDATE delivery_req_versions SET status=?, updated_at=? WHERE eng_req_id=? AND major=? AND minor=? AND patch=?`,
		v.Status, v.UpdatedAt, engID, major, minor, patch)
	if err != nil {
		return ReqVersion{}, err
	}
	href := fmt.Sprintf("#/delivery/requirements/%s/%d/%d/%d", engID, major, minor, patch)
	refID := fmt.Sprintf("%s:%d.%d.%d", engID, major, minor, patch)
	gateID := "gate_req_" + refID
	_, _ = s.db.Exec(`DELETE FROM delivery_gates WHERE id=?`, gateID)
	_, err = s.db.Exec(`
INSERT INTO delivery_gates(id, kind, ref_type, ref_id, title, status, href, created_at)
VALUES(?,?,?,?,?,?,?,?)`,
		gateID, "req_approve", "req_version", refID,
		fmt.Sprintf("Req %d.%d.%d approved", major, minor, patch),
		"done", href, v.UpdatedAt)
	return v, err
}

func (s *Store) listDesigns() ([]Design, error) {
	rows, err := s.db.Query(`SELECT ` + designSelectCols + ` FROM delivery_designs ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	var list []Design
	for rows.Next() {
		var d Design
		if err := scanDesign(rows, &d); err != nil {
			_ = rows.Close()
			return nil, err
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range list {
		opts, err := s.listOptions(list[i].ID)
		if err != nil {
			return nil, err
		}
		list[i].Options = opts
		vers, err := s.listDesignVersions(list[i].ID)
		if err != nil {
			return nil, err
		}
		list[i].Versions = vers
	}
	return list, nil
}

func (s *Store) listOptions(designID string) ([]DesignOption, error) {
	rows, err := s.db.Query(`
SELECT id, title, summary, tradeoffs, risks, COALESCE(out_of_scope,'') FROM delivery_design_options
WHERE design_id=? ORDER BY sort_order, id`, designID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []DesignOption
	for rows.Next() {
		var o DesignOption
		if err := rows.Scan(&o.ID, &o.Title, &o.Summary, &o.Tradeoffs, &o.Risks, &o.OutOfScope); err != nil {
			return nil, err
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

// PatchDesignInput updates design fields and optional option cards.
type PatchDesignInput struct {
	ChosenOptionID  *string
	ADR             *string // legacy single blob; if set alone, maps to AdrDecision
	AdrContext      *string
	AdrDecision     *string
	AdrConsequences *string
	AdrSpec         *string
	AdrArchitecture *string
	AdrSequence     *string
	AdrSample       *string
	Options         []DesignOption // partial update by id when set
}

// PatchDesign updates chosen option, ADR sections, and/or option card fields.
func (s *Store) PatchDesign(id string, in PatchDesignInput) (Design, error) {
	d, err := s.getDesign(id)
	if err != nil {
		return Design{}, err
	}
	if d.Status == "approved" {
		return Design{}, fmt.Errorf("approved design is read-only — open View only")
	}
	if in.ChosenOptionID != nil {
		d.ChosenOptionID = *in.ChosenOptionID
	}
	sectionsPatched := false
	if in.AdrContext != nil {
		d.AdrContext = *in.AdrContext
		sectionsPatched = true
	}
	if in.AdrDecision != nil {
		d.AdrDecision = *in.AdrDecision
		sectionsPatched = true
	}
	if in.AdrConsequences != nil {
		d.AdrConsequences = *in.AdrConsequences
		sectionsPatched = true
	}
	if in.AdrSpec != nil {
		d.AdrSpec = *in.AdrSpec
		sectionsPatched = true
	}
	if in.AdrArchitecture != nil {
		d.AdrArchitecture = *in.AdrArchitecture
		sectionsPatched = true
	}
	if in.AdrSequence != nil {
		d.AdrSequence = *in.AdrSequence
		sectionsPatched = true
	}
	if in.AdrSample != nil {
		d.AdrSample = *in.AdrSample
		sectionsPatched = true
	}
	if in.ADR != nil && !sectionsPatched {
		d.AdrDecision = *in.ADR
		sectionsPatched = true
	}
	if sectionsPatched {
		d.ADR = composeADR(d)
	} else if in.ADR != nil {
		d.ADR = *in.ADR
	}
	d.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`UPDATE delivery_designs SET chosen_option_id=?, adr=?, updated_at=?,
  adr_context=?, adr_decision=?, adr_consequences=?, adr_spec=?, adr_architecture=?, adr_sequence=?, adr_sample=?
WHERE id=?`,
		d.ChosenOptionID, d.ADR, d.UpdatedAt,
		d.AdrContext, d.AdrDecision, d.AdrConsequences, d.AdrSpec, d.AdrArchitecture, d.AdrSequence, d.AdrSample,
		id)
	if err != nil {
		return Design{}, err
	}
	for _, o := range in.Options {
		if o.ID == "" {
			continue
		}
		_, err = s.db.Exec(`
UPDATE delivery_design_options SET title=?, summary=?, tradeoffs=?, risks=?, out_of_scope=?
WHERE id=? AND design_id=?`,
			o.Title, o.Summary, o.Tradeoffs, o.Risks, o.OutOfScope, o.ID, id)
		if err != nil {
			return Design{}, err
		}
	}
	return s.getDesign(id)
}

func (s *Store) getDesign(id string) (Design, error) {
	var d Design
	err := scanDesign(s.db.QueryRow(`SELECT `+designSelectCols+` FROM delivery_designs WHERE id=?`, id), &d)
	if err != nil {
		return Design{}, fmt.Errorf("design not found")
	}
	opts, err := s.listOptions(d.ID)
	if err != nil {
		return Design{}, err
	}
	d.Options = opts
	vers, err := s.listDesignVersions(d.ID)
	if err != nil {
		return Design{}, err
	}
	d.Versions = vers
	return d, nil
}

func (s *Store) listDesignVersions(designID string) ([]DesignVersion, error) {
	rows, err := s.db.Query(`
SELECT id, sort_order, title, gate, sub_architecture, class_design, status, updated_at
FROM delivery_design_versions WHERE design_id=? ORDER BY sort_order, id`, designID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []DesignVersion
	byID := map[string]*DesignVersion{}
	for rows.Next() {
		var v DesignVersion
		if err := rows.Scan(&v.ID, &v.SortOrder, &v.Title, &v.Gate, &v.SubArchitecture, &v.ClassDesign, &v.Status, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.refreshStatus()
		v.DependsOn = []string{}
		list = append(list, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		return []DesignVersion{}, nil
	}
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	drows, err := s.db.Query(`
SELECT version_id, depends_on_id FROM delivery_design_version_deps
WHERE design_id=? ORDER BY version_id, depends_on_id`, designID)
	if err != nil {
		return nil, err
	}
	defer drows.Close()
	for drows.Next() {
		var vid, dep string
		if err := drows.Scan(&vid, &dep); err != nil {
			return nil, err
		}
		if v := byID[vid]; v != nil {
			v.DependsOn = append(v.DependsOn, dep)
		}
	}
	return list, drows.Err()
}

func validateDesignVersionsForApprove(vers []DesignVersion) error {
	if len(vers) == 0 {
		return fmt.Errorf("add at least one Version before approve")
	}
	for i, v := range vers {
		if !designVersionReady(v) {
			label := strings.TrimSpace(v.Title)
			if label == "" {
				label = fmt.Sprintf("Version %d", i+1)
			}
			return fmt.Errorf("%s needs Gate, Sub-architecture, and Class design", label)
		}
	}
	if _, err := topoSortVersions(vers); err != nil {
		return err
	}
	return nil
}

// topoSortVersions returns Versions in prerequisite-first order (stable by sort_order, id).
func topoSortVersions(vers []DesignVersion) ([]DesignVersion, error) {
	if len(vers) == 0 {
		return []DesignVersion{}, nil
	}
	byID := map[string]DesignVersion{}
	indeg := map[string]int{}
	// dependents[prereq] = versions that depend on prereq
	dependents := map[string][]string{}
	for _, v := range vers {
		byID[v.ID] = v
		indeg[v.ID] = 0
	}
	for _, v := range vers {
		seen := map[string]bool{}
		for _, dep := range v.DependsOn {
			if dep == "" || dep == v.ID {
				return nil, fmt.Errorf("invalid dependency on %s", v.ID)
			}
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("version %s depends on unknown %s", v.ID, dep)
			}
			if seen[dep] {
				continue
			}
			seen[dep] = true
			indeg[v.ID]++
			dependents[dep] = append(dependents[dep], v.ID)
		}
	}
	var ready []string
	for id, d := range indeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		a, b := byID[ready[i]], byID[ready[j]]
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		return a.ID < b.ID
	})
	var out []DesignVersion
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, byID[id])
		var unlocked []string
		for _, child := range dependents[id] {
			indeg[child]--
			if indeg[child] == 0 {
				unlocked = append(unlocked, child)
			}
		}
		sort.Slice(unlocked, func(i, j int) bool {
			a, b := byID[unlocked[i]], byID[unlocked[j]]
			if a.SortOrder != b.SortOrder {
				return a.SortOrder < b.SortOrder
			}
			return a.ID < b.ID
		})
		ready = append(ready, unlocked...)
	}
	if len(out) != len(vers) {
		return nil, fmt.Errorf("Version dependency cycle detected")
	}
	return out, nil
}

func (s *Store) replaceVersionDepends(designID, versionID string, dependsOn []string) error {
	ids := map[string]bool{}
	rows, err := s.db.Query(`SELECT id FROM delivery_design_versions WHERE design_id=?`, designID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids[id] = true
	}
	_ = rows.Close()
	if !ids[versionID] {
		return fmt.Errorf("version not found")
	}
	unique := make([]string, 0, len(dependsOn))
	seen := map[string]bool{}
	for _, dep := range dependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if dep == versionID {
			return fmt.Errorf("version cannot depend on itself")
		}
		if !ids[dep] {
			return fmt.Errorf("depends_on %q is not a Version on this design", dep)
		}
		if seen[dep] {
			continue
		}
		seen[dep] = true
		unique = append(unique, dep)
	}
	// Provisional graph for cycle check
	list, err := s.listDesignVersions(designID)
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID == versionID {
			list[i].DependsOn = unique
			break
		}
	}
	if _, err := topoSortVersions(list); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM delivery_design_version_deps WHERE design_id=? AND version_id=?`, designID, versionID); err != nil {
		return err
	}
	for _, dep := range unique {
		if _, err := tx.Exec(`
INSERT INTO delivery_design_version_deps(design_id, version_id, depends_on_id) VALUES(?,?,?)`,
			designID, versionID, dep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddDesignVersionInput creates a Version card under a draft design.
type AddDesignVersionInput struct {
	Title           string
	Gate            string
	SubArchitecture string
	ClassDesign     string
}

// AddDesignVersion appends a Version (sort_order = max+1).
func (s *Store) AddDesignVersion(designID string, in AddDesignVersionInput) (DesignVersion, error) {
	d, err := s.getDesign(designID)
	if err != nil {
		return DesignVersion{}, err
	}
	if d.Status == "approved" {
		return DesignVersion{}, fmt.Errorf("approved design is read-only")
	}
	maxOrd := 0
	for _, v := range d.Versions {
		if v.SortOrder > maxOrd {
			maxOrd = v.SortOrder
		}
	}
	ord := maxOrd + 1
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = fmt.Sprintf("Version %d", ord)
	}
	ts := nowRFC3339()
	v := DesignVersion{
		ID:              fmt.Sprintf("%s_v%d_%d", designID, ord, time.Now().UnixNano()%1e6),
		SortOrder:       ord,
		Title:           title,
		Gate:            strings.TrimSpace(in.Gate),
		SubArchitecture: strings.TrimSpace(in.SubArchitecture),
		ClassDesign:     strings.TrimSpace(in.ClassDesign),
		DependsOn:       []string{},
		UpdatedAt:       ts,
	}
	v.refreshStatus()
	_, err = s.db.Exec(`
INSERT INTO delivery_design_versions(id, design_id, sort_order, title, gate, sub_architecture, class_design, status, updated_at)
VALUES(?,?,?,?,?,?,?,?,?)`,
		v.ID, designID, v.SortOrder, v.Title, v.Gate, v.SubArchitecture, v.ClassDesign, v.Status, v.UpdatedAt)
	if err != nil {
		return DesignVersion{}, err
	}
	_, _ = s.db.Exec(`UPDATE delivery_designs SET updated_at=? WHERE id=?`, ts, designID)
	return v, nil
}

// PatchDesignVersionInput updates Version fields (nil = leave unchanged).
type PatchDesignVersionInput struct {
	Title           *string
	Gate            *string
	SubArchitecture *string
	ClassDesign     *string
	SortOrder       *int
	DependsOn       *[]string // when set, replaces dependency set
}

// PatchDesignVersion updates one Version under a draft design.
func (s *Store) PatchDesignVersion(designID, versionID string, in PatchDesignVersionInput) (DesignVersion, error) {
	d, err := s.getDesign(designID)
	if err != nil {
		return DesignVersion{}, err
	}
	if d.Status == "approved" {
		return DesignVersion{}, fmt.Errorf("approved design is read-only")
	}
	var v DesignVersion
	err = s.db.QueryRow(`
SELECT id, sort_order, title, gate, sub_architecture, class_design, status, updated_at
FROM delivery_design_versions WHERE id=? AND design_id=?`, versionID, designID).Scan(
		&v.ID, &v.SortOrder, &v.Title, &v.Gate, &v.SubArchitecture, &v.ClassDesign, &v.Status, &v.UpdatedAt)
	if err != nil {
		return DesignVersion{}, fmt.Errorf("version not found")
	}
	if in.Title != nil {
		v.Title = strings.TrimSpace(*in.Title)
	}
	if in.Gate != nil {
		v.Gate = *in.Gate
	}
	if in.SubArchitecture != nil {
		v.SubArchitecture = *in.SubArchitecture
	}
	if in.ClassDesign != nil {
		v.ClassDesign = *in.ClassDesign
	}
	if in.SortOrder != nil && *in.SortOrder > 0 {
		v.SortOrder = *in.SortOrder
	}
	v.refreshStatus()
	v.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`
UPDATE delivery_design_versions SET sort_order=?, title=?, gate=?, sub_architecture=?, class_design=?, status=?, updated_at=?
WHERE id=? AND design_id=?`,
		v.SortOrder, v.Title, v.Gate, v.SubArchitecture, v.ClassDesign, v.Status, v.UpdatedAt, versionID, designID)
	if err != nil {
		return DesignVersion{}, err
	}
	if in.DependsOn != nil {
		if err := s.replaceVersionDepends(designID, versionID, *in.DependsOn); err != nil {
			return DesignVersion{}, err
		}
	}
	_, _ = s.db.Exec(`UPDATE delivery_designs SET updated_at=? WHERE id=?`, v.UpdatedAt, designID)
	// Reload with depends_on
	list, err := s.listDesignVersions(designID)
	if err != nil {
		return DesignVersion{}, err
	}
	for _, x := range list {
		if x.ID == versionID {
			return x, nil
		}
	}
	return DesignVersion{}, fmt.Errorf("version not found")
}

// DeleteDesignVersion removes a Version and renumbers remaining 1..n (draft designs only).
func (s *Store) DeleteDesignVersion(designID, versionID string) error {
	d, err := s.getDesign(designID)
	if err != nil {
		return err
	}
	if d.Status == "approved" {
		return fmt.Errorf("approved design is read-only")
	}
	found := false
	for _, v := range d.Versions {
		if v.ID == versionID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("version not found")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM delivery_design_version_deps WHERE design_id=? AND (version_id=? OR depends_on_id=?)`,
		designID, versionID, versionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_design_versions WHERE id=? AND design_id=?`, versionID, designID); err != nil {
		return err
	}
	rows, err := tx.Query(`
SELECT id FROM delivery_design_versions WHERE design_id=? ORDER BY sort_order, id`, designID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	_ = rows.Close()
	ts := nowRFC3339()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE delivery_design_versions SET sort_order=?, updated_at=? WHERE id=?`, i+1, ts, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE delivery_designs SET updated_at=? WHERE id=?`, ts, designID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteDesign removes a design and its option/version cards.
func (s *Store) DeleteDesign(id string) error {
	if _, err := s.getDesign(id); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM delivery_design_version_deps WHERE design_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_design_versions WHERE design_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_design_options WHERE design_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_designs WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ApproveDesign marks design approved when Versions are complete, advances lifecycle, seeds roadmap.
func (s *Store) ApproveDesign(id string) (Design, error) {
	d, err := s.getDesign(id)
	if err != nil {
		return Design{}, err
	}
	if d.Status == "approved" {
		return d, nil
	}
	if err := validateDesignVersionsForApprove(d.Versions); err != nil {
		return Design{}, err
	}
	if err := s.renumberVersionsByTopo(id); err != nil {
		return Design{}, err
	}
	d.Status = "approved"
	d.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`UPDATE delivery_designs SET status=?, updated_at=? WHERE id=?`, d.Status, d.UpdatedAt, id)
	if err != nil {
		return Design{}, err
	}
	if d.EngReqID != "" {
		_ = s.SetMilestoneLifecycle(d.EngReqID, d.Major, d.Minor, LifeAwaitingRoadmap)
	}
	_ = s.seedRoadmapFromDesign(id)
	return s.getDesign(id)
}

// BootstrapDesignRoadmap seeds eng-req patches + canvas phases from Design Versions (idempotent when phases exist).
func (s *Store) BootstrapDesignRoadmap(id string) (Design, error) {
	d, err := s.getDesign(id)
	if err != nil {
		return Design{}, err
	}
	if d.Status != "approved" {
		return Design{}, fmt.Errorf("approve design before bootstrap")
	}
	if err := validateDesignVersionsForApprove(d.Versions); err != nil {
		return Design{}, err
	}
	if err := s.seedRoadmapFromDesign(id); err != nil {
		return Design{}, err
	}
	return s.getDesign(id)
}

// renumberVersionsByTopo sets sort_order 1..n from topological dependency order.
func (s *Store) renumberVersionsByTopo(designID string) error {
	list, err := s.listDesignVersions(designID)
	if err != nil {
		return err
	}
	ordered, err := topoSortVersions(list)
	if err != nil {
		return err
	}
	ts := nowRFC3339()
	for i, v := range ordered {
		if _, err := s.db.Exec(`UPDATE delivery_design_versions SET sort_order=?, updated_at=? WHERE id=? AND design_id=?`,
			i+1, ts, v.ID, designID); err != nil {
			return err
		}
	}
	return nil
}

// seedRoadmapFromDesign aligns eng-req patches and canvas phases 1:1 with Design Versions (topo order).
func (s *Store) seedRoadmapFromDesign(designID string) error {
	d, err := s.getDesign(designID)
	if err != nil {
		return err
	}
	if len(d.Versions) == 0 {
		return nil
	}
	if err := s.renumberVersionsByTopo(designID); err != nil {
		return err
	}
	d, err = s.getDesign(designID)
	if err != nil {
		return err
	}
	ordered, err := topoSortVersions(d.Versions)
	if err != nil {
		return err
	}
	d.Versions = ordered
	if err := s.alignEngReqPatchesFromVersions(d); err != nil {
		return err
	}
	return s.ensureCanvasPhasesFromVersions(d)
}

func (s *Store) alignEngReqPatchesFromVersions(d Design) error {
	if d.EngReqID == "" {
		return nil
	}
	rows, err := s.db.Query(`
SELECT patch, title, problem FROM delivery_req_versions
WHERE eng_req_id=? AND major=? AND minor=? ORDER BY patch`, d.EngReqID, d.Major, d.Minor)
	if err != nil {
		return err
	}
	type existing struct {
		patch          int
		title, problem string
	}
	var have []existing
	for rows.Next() {
		var e existing
		if err := rows.Scan(&e.patch, &e.title, &e.problem); err != nil {
			_ = rows.Close()
			return err
		}
		have = append(have, e)
	}
	_ = rows.Close()

	status := LifeAwaitingRoadmap
	if len(have) > 0 {
		_ = s.db.QueryRow(`
SELECT status FROM delivery_req_versions WHERE eng_req_id=? AND major=? AND minor=? AND patch=1`,
			d.EngReqID, d.Major, d.Minor).Scan(&status)
	}
	ts := nowRFC3339()
	for i, v := range d.Versions {
		patch := i + 1 // sort_order aligned to 1-based patch index
		title := strings.TrimSpace(v.Title)
		if title == "" {
			title = fmt.Sprintf("Phase %d", patch)
		}
		gate := strings.TrimSpace(v.Gate)
		found := false
		for _, e := range have {
			if e.patch == patch {
				found = true
				break
			}
		}
		if found {
			continue
		}
		_, err = s.db.Exec(`
INSERT INTO delivery_req_versions(eng_req_id, major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.EngReqID, d.Major, d.Minor, patch, title, status, gate, "", "", "", "", ts)
		if err != nil {
			return fmt.Errorf("align patch %d: %w", patch, err)
		}
	}
	_, _ = s.db.Exec(`UPDATE delivery_eng_reqs SET updated_at=? WHERE id=?`, ts, d.EngReqID)
	return nil
}

func (s *Store) ensureCanvasPhasesFromVersions(d Design) error {
	msID := fmt.Sprintf("ms_%s_%d_%d", d.EngReqID, d.Major, d.Minor)
	var exists string
	err := s.db.QueryRow(`SELECT id FROM delivery_milestones WHERE id=?`, msID).Scan(&exists)
	if err != nil {
		// Create milestone node for this design's major.minor
		title := d.Title
		if title == "" {
			title = fmt.Sprintf("%d.%d", d.Major, d.Minor)
		}
		acceptance := strings.TrimSpace(d.Versions[0].Gate)
		x, y := s.nextMilestoneCanvasSlot()
		_, err = s.db.Exec(`
INSERT INTO delivery_milestones(id, title, status, acceptance, x, y, sort_order)
VALUES(?,?,?,?,?,?,?)`,
			msID, title, "planned", acceptance, x, y, d.Minor)
		if err != nil {
			return err
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_phases WHERE milestone_id=?`, msID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil // already seeded
	}
	for i, v := range d.Versions {
		title := strings.TrimSpace(v.Title)
		if title == "" {
			title = fmt.Sprintf("Version %d", i+1)
		}
		phID := fmt.Sprintf("%s_ph_%d", msID, i+1)
		_, err = s.db.Exec(`
INSERT INTO delivery_phases(id, milestone_id, title, status, sort_order) VALUES(?,?,?,?,?)`,
			phID, msID, title, "planned", i)
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateDesignInput binds a new draft design to a milestone (major.minor).
type CreateDesignInput struct {
	EngReqID string
	Major    int
	Minor    int
	Title    string
}

// CreateDesign inserts a draft design bound to a milestone (Versions authored separately).
func (s *Store) CreateDesign(in CreateDesignInput) (Design, error) {
	engID := strings.TrimSpace(in.EngReqID)
	if engID == "" {
		return Design{}, fmt.Errorf("eng_req_id required")
	}
	engs, err := s.listEngReqs()
	if err != nil {
		return Design{}, err
	}
	var eng *EngReq
	for i := range engs {
		if engs[i].ID == engID {
			eng = &engs[i]
			break
		}
	}
	if eng == nil {
		return Design{}, fmt.Errorf("eng-req not found")
	}
	var head *ReqVersion
	for i := range eng.Versions {
		v := &eng.Versions[i]
		if v.Major == in.Major && v.Minor == in.Minor {
			if head == nil || v.Patch > head.Patch {
				head = v
			}
		}
	}
	if head == nil {
		return Design{}, fmt.Errorf("milestone %d.%d not found on eng-req", in.Major, in.Minor)
	}
	existing, err := s.listDesigns()
	if err != nil {
		return Design{}, err
	}
	for _, d := range existing {
		if d.EngReqID == engID && d.Major == in.Major && d.Minor == in.Minor {
			return Design{}, fmt.Errorf("design already exists for %d.%d", in.Major, in.Minor)
		}
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = head.Title
	}
	if title == "" {
		title = fmt.Sprintf("Design %d.%d", in.Major, in.Minor)
	}
	ts := nowRFC3339()
	id := fmt.Sprintf("des_%s_%d_%d_%d", engID, in.Major, in.Minor, time.Now().UnixNano()%1e6)
	_, err = s.db.Exec(`
INSERT INTO delivery_designs(id, eng_req_id, major, minor, patch, title, status, chosen_option_id, adr, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, engID, in.Major, in.Minor, head.Patch, title, "draft", "",
		"Draft from requirement — author Versions (Gate / Sub-architecture / Class design), polish ADR, then Approve.", ts)
	if err != nil {
		return Design{}, err
	}
	return s.getDesign(id)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// PatchMilestoneInput updates canvas position, execute Worker, run_state, Execute, or Test actions.
type PatchMilestoneInput struct {
	X               *float64
	Y               *float64
	ExecuteWorkerID *string
	RunState        *string
	TestNote        *string
	Execute         bool
	SubmitTest      bool
	PassTest        bool
	FailTest        bool
	FailTitle       string
	FailBody        string
	ReadyDev        bool // awaiting_roadmap → awaiting_development
	AcceptMilestone bool // complete + pending → accepted
}

// PatchMilestonePosition updates canvas card coordinates (persisted).
func (s *Store) PatchMilestonePosition(id string, x, y float64) (Milestone, error) {
	return s.PatchMilestone(id, PatchMilestoneInput{X: &x, Y: &y})
}

func normalizeRunState(s string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case RunIdle, RunRunning, RunPaused, RunStopped:
		return strings.TrimSpace(strings.ToLower(s)), nil
	default:
		return "", fmt.Errorf("invalid run_state %q", s)
	}
}

// PatchMilestone updates position and/or execute_worker_id / run_state; Execute / Test advance lifecycle.
func (s *Store) PatchMilestone(id string, in PatchMilestoneInput) (Milestone, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Milestone{}, fmt.Errorf("milestone id required")
	}
	cur, err := s.getMilestone(id)
	if err != nil {
		return Milestone{}, err
	}

	actionN := 0
	if in.Execute {
		actionN++
	}
	if in.SubmitTest {
		actionN++
	}
	if in.PassTest {
		actionN++
	}
	if in.FailTest {
		actionN++
	}
	if in.ReadyDev {
		actionN++
	}
	if in.AcceptMilestone {
		actionN++
	}
	if actionN > 1 {
		return Milestone{}, fmt.Errorf("only one of execute, submit_test, pass_test, fail_test, ready_dev, accept_milestone allowed")
	}

	x, y := cur.X, cur.Y
	if in.X != nil {
		x = *in.X
	}
	if in.Y != nil {
		y = *in.Y
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x > 20000 {
		x = 20000
	}
	if y > 20000 {
		y = 20000
	}

	workerID := cur.ExecuteWorkerID
	if in.ExecuteWorkerID != nil {
		workerID = strings.TrimSpace(*in.ExecuteWorkerID)
	}

	runState := cur.RunState
	if runState == "" {
		runState = RunIdle
	}
	if in.RunState != nil {
		rs, err := normalizeRunState(*in.RunState)
		if err != nil {
			return Milestone{}, err
		}
		runState = rs
	}

	testNote := cur.TestNote
	if in.TestNote != nil {
		testNote = strings.TrimSpace(*in.TestNote)
	}

	status := cur.Status
	acceptStatus := cur.AcceptStatus
	acceptedAt := cur.AcceptedAt
	bind := parseMilestoneBind(id)

	if in.Execute {
		if workerID == "" {
			return Milestone{}, fmt.Errorf("execute_worker_id required to execute")
		}
		if len(cur.Phases) == 0 {
			return Milestone{}, fmt.Errorf("milestone has no phases; Bootstrap from Designs first")
		}
		status = LifeInDevelopment
		runState = RunRunning
		if bind != nil {
			if err := s.SetMilestoneLifecycle(bind.EngID, bind.Major, bind.Minor, LifeInDevelopment); err != nil {
				return Milestone{}, err
			}
		}
	}

	if in.SubmitTest {
		if status != LifeInDevelopment {
			return Milestone{}, fmt.Errorf("submit_test requires status %s (got %s)", LifeInDevelopment, status)
		}
		status = LifeTesting
		runState = RunStopped
		if bind != nil {
			if err := s.SetMilestoneLifecycle(bind.EngID, bind.Major, bind.Minor, LifeTesting); err != nil {
				return Milestone{}, err
			}
		}
	}

	if in.PassTest {
		if status != LifeTesting {
			return Milestone{}, fmt.Errorf("pass_test requires status %s (got %s)", LifeTesting, status)
		}
		if bind == nil {
			return Milestone{}, fmt.Errorf("pass_test requires bound milestone id")
		}
		if err := s.assertPassCampaignClean(bind.EngID, bind.Major, bind.Minor); err != nil {
			return Milestone{}, err
		}
		status = LifeComplete
		runState = RunIdle
		acceptStatus = AcceptPending
		acceptedAt = ""
		if err := s.SetMilestoneLifecycle(bind.EngID, bind.Major, bind.Minor, LifeComplete); err != nil {
			return Milestone{}, err
		}
	}

	if in.FailTest {
		if status != LifeTesting {
			return Milestone{}, fmt.Errorf("fail_test requires status %s (got %s)", LifeTesting, status)
		}
		if bind == nil {
			return Milestone{}, fmt.Errorf("fail_test requires bound milestone id")
		}
		title := strings.TrimSpace(in.FailTitle)
		body := strings.TrimSpace(in.FailBody)
		if title == "" && body == "" {
			return Milestone{}, fmt.Errorf("fail_test requires title or body")
		}
		if title == "" {
			title = truncateRunes(body, 48)
		}
		if _, err := s.CreateBuglistItem(CreateBuglistInput{
			EngReqID: bind.EngID,
			Major:    bind.Major,
			Minor:    bind.Minor,
			Title:    title,
			Body:     body,
		}); err != nil {
			return Milestone{}, err
		}
		if _, err := s.SeedBugfixPhases(id, bind.EngID, bind.Major, bind.Minor); err != nil {
			return Milestone{}, err
		}
		status = LifeAwaitingRoadmap
		runState = RunIdle
		acceptStatus = AcceptNone
		acceptedAt = ""
		if err := s.SetMilestoneLifecycle(bind.EngID, bind.Major, bind.Minor, LifeAwaitingRoadmap); err != nil {
			return Milestone{}, err
		}
	}

	if in.ReadyDev {
		if status != LifeAwaitingRoadmap {
			return Milestone{}, fmt.Errorf("ready_dev requires status %s (got %s)", LifeAwaitingRoadmap, status)
		}
		if bind == nil {
			return Milestone{}, fmt.Errorf("ready_dev requires bound milestone id")
		}
		unfinished := false
		for _, p := range cur.Phases {
			st := strings.ToLower(p.Status)
			if st != "finished" && st != "done" && st != JobFinished {
				unfinished = true
				break
			}
		}
		if !unfinished {
			return Milestone{}, fmt.Errorf("ready_dev requires at least one unfinished phase (bugfix or planned)")
		}
		status = LifeAwaitingDevelopment
		runState = RunIdle
		if err := s.SetMilestoneLifecycle(bind.EngID, bind.Major, bind.Minor, LifeAwaitingDevelopment); err != nil {
			return Milestone{}, err
		}
	}

	if in.AcceptMilestone {
		if status != LifeComplete {
			return Milestone{}, fmt.Errorf("accept_milestone requires status %s (got %s)", LifeComplete, status)
		}
		if acceptStatus == AcceptAccepted {
			// idempotent
		} else {
			if err := s.assertAcceptChecklist(cur); err != nil {
				return Milestone{}, err
			}
			acceptStatus = AcceptAccepted
			acceptedAt = nowRFC3339()
		}
	}

	res, err := s.db.Exec(`
UPDATE delivery_milestones SET x=?, y=?, execute_worker_id=?, run_state=?, test_note=?, status=?,
  accept_status=?, accepted_at=? WHERE id=?`,
		x, y, workerID, runState, testNote, status, acceptStatus, acceptedAt, id)
	if err != nil {
		return Milestone{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Milestone{}, fmt.Errorf("milestone not found")
	}
	return s.getMilestone(id)
}

type milestoneBind struct {
	EngID string
	Major int
	Minor int
}

// parseMilestoneBind parses bootstrap id `ms_{engId}_{major}_{minor}`.
func parseMilestoneBind(msID string) *milestoneBind {
	id := strings.TrimSpace(msID)
	if !strings.HasPrefix(id, "ms_") {
		return nil
	}
	rest := id[3:]
	// Find last two _N_N segments.
	i := strings.LastIndex(rest, "_")
	if i <= 0 {
		return nil
	}
	minorStr := rest[i+1:]
	rest2 := rest[:i]
	j := strings.LastIndex(rest2, "_")
	if j < 0 {
		return nil
	}
	majorStr := rest2[j+1:]
	engID := rest2[:j]
	if engID == "" {
		return nil
	}
	major, err1 := strconv.Atoi(majorStr)
	minor, err2 := strconv.Atoi(minorStr)
	if err1 != nil || err2 != nil {
		return nil
	}
	return &milestoneBind{EngID: engID, Major: major, Minor: minor}
}

// GetMilestone returns a milestone with phases.
func (s *Store) GetMilestone(id string) (Milestone, error) {
	return s.getMilestone(id)
}

// RestoreMilestoneRun restores status + run_state after a failed Execute start (and eng lifecycle).
func (s *Store) RestoreMilestoneRun(id, status, runState string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("milestone id required")
	}
	rs := strings.TrimSpace(strings.ToLower(runState))
	if rs == "" {
		rs = RunIdle
	}
	norm, err := normalizeRunState(rs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE delivery_milestones SET status=?, run_state=? WHERE id=?`, status, norm, id)
	if err != nil {
		return err
	}
	if bind := parseMilestoneBind(id); bind != nil && status != "" {
		_ = s.SetMilestoneLifecycle(bind.EngID, bind.Major, bind.Minor, status)
	}
	return nil
}

func (s *Store) getMilestone(id string) (Milestone, error) {
	var m Milestone
	err := s.db.QueryRow(`
SELECT id, title, status, acceptance, x, y, execute_worker_id, COALESCE(run_state,'idle'), COALESCE(test_note,''),
  COALESCE(campaign_job_id,''), COALESCE(campaign_job_status,''), COALESCE(campaign_job_kind,''), COALESCE(campaign_job_last_event,''),
  COALESCE(accept_status,''), COALESCE(accepted_at,''),
  COALESCE(merge_status,''), COALESCE(merge_note,''), COALESCE(merged_at,'')
FROM delivery_milestones WHERE id=?`, id).Scan(
		&m.ID, &m.Title, &m.Status, &m.Acceptance, &m.X, &m.Y, &m.ExecuteWorkerID, &m.RunState, &m.TestNote,
		&m.CampaignJobID, &m.CampaignJobStatus, &m.CampaignJobKind, &m.CampaignJobLastEvent,
		&m.AcceptStatus, &m.AcceptedAt, &m.MergeStatus, &m.MergeNote, &m.MergedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return Milestone{}, fmt.Errorf("milestone not found")
		}
		return Milestone{}, err
	}
	ph, err := s.listPhases(m.ID)
	if err != nil {
		return Milestone{}, err
	}
	m.Phases = ph
	return m, nil
}

func (s *Store) nextMilestoneCanvasSlot() (x, y float64) {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM delivery_milestones`).Scan(&n)
	const colW, rowH = 240.0, 120.0
	cols := 4
	return 80 + float64(n%cols)*colW, 80 + float64(n/cols)*rowH
}

func (s *Store) loadRoadmap() (Roadmap, error) {
	var rm Roadmap
	rows, err := s.db.Query(`
SELECT id, title, status, acceptance, x, y, execute_worker_id, COALESCE(run_state,'idle'), COALESCE(test_note,''),
  COALESCE(campaign_job_id,''), COALESCE(campaign_job_status,''), COALESCE(campaign_job_kind,''), COALESCE(campaign_job_last_event,''),
  COALESCE(accept_status,''), COALESCE(accepted_at,''),
  COALESCE(merge_status,''), COALESCE(merge_note,''), COALESCE(merged_at,'')
FROM delivery_milestones ORDER BY sort_order, id`)
	if err != nil {
		return rm, err
	}
	for rows.Next() {
		var m Milestone
		if err := rows.Scan(&m.ID, &m.Title, &m.Status, &m.Acceptance, &m.X, &m.Y, &m.ExecuteWorkerID, &m.RunState, &m.TestNote,
			&m.CampaignJobID, &m.CampaignJobStatus, &m.CampaignJobKind, &m.CampaignJobLastEvent,
			&m.AcceptStatus, &m.AcceptedAt, &m.MergeStatus, &m.MergeNote, &m.MergedAt); err != nil {
			_ = rows.Close()
			return rm, err
		}
		rm.Milestones = append(rm.Milestones, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return rm, err
	}
	if err := rows.Close(); err != nil {
		return rm, err
	}
	for i := range rm.Milestones {
		ph, err := s.listPhases(rm.Milestones[i].ID)
		if err != nil {
			return rm, err
		}
		rm.Milestones[i].Phases = ph
	}
	erows, err := s.db.Query(`SELECT from_id, to_id FROM delivery_edges`)
	if err != nil {
		return rm, err
	}
	defer erows.Close()
	for erows.Next() {
		var e Edge
		if err := erows.Scan(&e.From, &e.To); err != nil {
			return rm, err
		}
		rm.Edges = append(rm.Edges, e)
	}
	if rm.Milestones == nil {
		rm.Milestones = []Milestone{}
	}
	if rm.Edges == nil {
		rm.Edges = []Edge{}
	}
	return rm, erows.Err()
}

func (s *Store) listPhases(msID string) ([]Phase, error) {
	rows, err := s.db.Query(`
SELECT id, title, status, COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_phases WHERE milestone_id=? ORDER BY sort_order, id`, msID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Phase
	for rows.Next() {
		var p Phase
		if err := rows.Scan(&p.ID, &p.Title, &p.Status, &p.JobID, &p.JobStatus, &p.JobLastEvent); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// GetPhase returns one phase by id.
func (s *Store) GetPhase(phaseID string) (Phase, string, error) {
	var p Phase
	var msID string
	err := s.db.QueryRow(`
SELECT id, milestone_id, title, status, COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_phases WHERE id=?`, phaseID).Scan(
		&p.ID, &msID, &p.Title, &p.Status, &p.JobID, &p.JobStatus, &p.JobLastEvent)
	if err != nil {
		if err == sql.ErrNoRows {
			return Phase{}, "", fmt.Errorf("phase not found")
		}
		return Phase{}, "", err
	}
	return p, msID, nil
}

// NextRunnablePhase returns the first phase that is not finished (by sort_order).
func (s *Store) NextRunnablePhase(msID string) (Phase, error) {
	phases, err := s.listPhases(msID)
	if err != nil {
		return Phase{}, err
	}
	for _, p := range phases {
		st := strings.ToLower(p.Status)
		if st == "finished" || st == "done" || st == JobFinished {
			continue
		}
		if p.JobStatus == JobRunning || p.JobStatus == JobQueued {
			return p, nil
		}
		return p, nil
	}
	return Phase{}, fmt.Errorf("no runnable phases")
}

// ActivePhaseJob returns the phase currently queued/running for a milestone, if any.
func (s *Store) ActivePhaseJob(msID string) (Phase, error) {
	phases, err := s.listPhases(msID)
	if err != nil {
		return Phase{}, err
	}
	for _, p := range phases {
		if p.JobStatus == JobRunning || p.JobStatus == JobQueued {
			return p, nil
		}
	}
	return Phase{}, fmt.Errorf("no active phase job")
}

// SetPhaseJob binds a job to a phase.
func (s *Store) SetPhaseJob(phaseID, jobID, jobStatus, lastEvent string) error {
	_, err := s.db.Exec(`
UPDATE delivery_phases SET job_id=?, job_status=?, job_last_event=? WHERE id=?`,
		jobID, jobStatus, truncateRunes(lastEvent, 280), phaseID)
	return err
}

// AppendPhaseJobEvent updates the last-event snippet and status.
func (s *Store) AppendPhaseJobEvent(phaseID, jobStatus, lastEvent string) error {
	_, err := s.db.Exec(`
UPDATE delivery_phases SET job_status=?, job_last_event=? WHERE id=?`,
		jobStatus, truncateRunes(lastEvent, 280), phaseID)
	return err
}

// CompletePhaseJob marks phase finished/error and clears running state fields appropriately.
func (s *Store) CompletePhaseJob(phaseID, jobStatus, phaseStatus, lastEvent string) error {
	_, err := s.db.Exec(`
UPDATE delivery_phases SET job_status=?, status=?, job_last_event=? WHERE id=?`,
		jobStatus, phaseStatus, truncateRunes(lastEvent, 280), phaseID)
	return err
}

// FindPhaseByJobID locates a phase with the given job id.
func (s *Store) FindPhaseByJobID(jobID string) (Phase, string, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Phase{}, "", fmt.Errorf("job id required")
	}
	var p Phase
	var msID string
	err := s.db.QueryRow(`
SELECT id, milestone_id, title, status, COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_phases WHERE job_id=?`, jobID).Scan(
		&p.ID, &msID, &p.Title, &p.Status, &p.JobID, &p.JobStatus, &p.JobLastEvent)
	if err != nil {
		if err == sql.ErrNoRows {
			return Phase{}, "", fmt.Errorf("phase not found for job")
		}
		return Phase{}, "", err
	}
	return p, msID, nil
}

// BuildPhasePrompt composes the v1 agent prompt for a phase under a milestone.
func (s *Store) BuildPhasePrompt(msID, phaseID string) (string, error) {
	ms, err := s.getMilestone(msID)
	if err != nil {
		return "", err
	}
	ph, _, err := s.GetPhase(phaseID)
	if err != nil {
		return "", err
	}
	phases, err := s.listPhases(msID)
	if err != nil {
		return "", err
	}
	idx, total := 1, len(phases)
	for i, p := range phases {
		if p.ID == phaseID {
			idx = i + 1
			break
		}
	}
	bind := parseMilestoneBind(msID)
	var gate, arch, class, success, project string
	if bind != nil {
		eng, err := s.GetEngReq(bind.EngID)
		if err == nil {
			project = eng.ProjectKey
			if project == "" {
				project = eng.WorkerMeta.ProjectRel
			}
			var head *ReqVersion
			for i := range eng.Versions {
				v := &eng.Versions[i]
				if v.Major == bind.Major && v.Minor == bind.Minor {
					if head == nil || v.Patch < head.Patch {
						head = v
					}
				}
			}
			if head != nil {
				success = head.Success
				if success == "" {
					success = head.Goals
				}
			}
		}
		for _, d := range mustListDesigns(s) {
			if d.EngReqID == bind.EngID && d.Major == bind.Major && d.Minor == bind.Minor {
				vers, _ := s.listDesignVersions(d.ID)
				if idx-1 < len(vers) {
					dv := vers[idx-1]
					gate, arch, class = dv.Gate, dv.SubArchitecture, dv.ClassDesign
				} else if len(vers) > 0 {
					dv := vers[0]
					gate, arch, class = dv.Gate, dv.SubArchitecture, dv.ClassDesign
				}
				break
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Milestone %s", ms.Title)
	if bind != nil {
		fmt.Fprintf(&b, " (%d.%d)", bind.Major, bind.Minor)
	}
	fmt.Fprintf(&b, " phase %d/%d: %s\n", idx, total, ph.Title)
	if project != "" {
		fmt.Fprintf(&b, "Worker checkout: %s\n", project)
	}
	if gate != "" {
		fmt.Fprintf(&b, "Gate: %s\n", truncateRunes(gate, 800))
	}
	if arch != "" {
		fmt.Fprintf(&b, "Sub-architecture: %s\n", truncateRunes(arch, 800))
	}
	if class != "" {
		fmt.Fprintf(&b, "Class design: %s\n", truncateRunes(class, 800))
	}
	if success != "" {
		fmt.Fprintf(&b, "Req success / acceptance: %s\n", truncateRunes(success, 800))
	} else if ms.Acceptance != "" {
		fmt.Fprintf(&b, "Req success / acceptance: %s\n", truncateRunes(ms.Acceptance, 800))
	}
	b.WriteString("Implement this phase only; commit on the current branch when done.\n")
	return b.String(), nil
}

func mustListDesigns(s *Store) []Design {
	list, err := s.listDesigns()
	if err != nil {
		return nil
	}
	return list
}

func (s *Store) listGates() ([]Gate, error) {
	rows, err := s.db.Query(`
SELECT id, kind, ref_type, ref_id, title, status, href, created_at
FROM delivery_gates WHERE status='pending' ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Gate
	for rows.Next() {
		var g Gate
		if err := rows.Scan(&g.ID, &g.Kind, &g.RefType, &g.RefID, &g.Title, &g.Status, &g.Href, &g.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	if list == nil {
		list = []Gate{}
	}
	return list, rows.Err()
}

func (s *Store) listPolishes() ([]Polish, error) {
	rows, err := s.db.Query(`
SELECT id, eng_req_id, major, minor, title, body, status, created_at,
  COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_polishes ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Polish
	for rows.Next() {
		var p Polish
		if err := rows.Scan(&p.ID, &p.EngReqID, &p.Major, &p.Minor, &p.Title, &p.Body, &p.Status, &p.CreatedAt,
			&p.JobID, &p.JobStatus, &p.JobLastEvent); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	if list == nil {
		list = []Polish{}
	}
	return list, rows.Err()
}

// CreatePolishInput authors a polish under a milestone bind.
type CreatePolishInput struct {
	EngReqID string
	Major    int
	Minor    int
	Title    string
	Body     string
}

// CreatePolish inserts a polish and appends a line to the head Version success field.
func (s *Store) CreatePolish(in CreatePolishInput) (Polish, error) {
	engID := strings.TrimSpace(in.EngReqID)
	if engID == "" {
		return Polish{}, fmt.Errorf("eng_req_id required")
	}
	title := strings.TrimSpace(in.Title)
	body := strings.TrimSpace(in.Body)
	if title == "" && body == "" {
		return Polish{}, fmt.Errorf("title or body required")
	}
	if title == "" {
		title = truncateRunes(body, 48)
	}
	eng, err := s.GetEngReq(engID)
	if err != nil {
		return Polish{}, err
	}
	var head *ReqVersion
	for i := range eng.Versions {
		v := &eng.Versions[i]
		if v.Major == in.Major && v.Minor == in.Minor {
			if head == nil || v.Patch < head.Patch {
				head = v
			}
		}
	}
	if head == nil {
		return Polish{}, fmt.Errorf("milestone %d.%d not found", in.Major, in.Minor)
	}

	ts := nowRFC3339()
	id := fmt.Sprintf("pol_%d", time.Now().UnixNano())
	_, err = s.db.Exec(`
INSERT INTO delivery_polishes(id, eng_req_id, major, minor, title, body, status, created_at)
VALUES(?,?,?,?,?,?,?,?)`,
		id, engID, in.Major, in.Minor, title, body, "ready", ts)
	if err != nil {
		return Polish{}, err
	}

	line := "· Polish: " + title
	if body != "" && body != title {
		line += " — " + truncateRunes(body, 120)
	}
	next := strings.TrimSpace(head.Success)
	if next != "" {
		next += "\n"
	}
	next += line
	_, err = s.db.Exec(`
UPDATE delivery_req_versions SET success=?, updated_at=?
WHERE eng_req_id=? AND major=? AND minor=? AND patch=?`,
		next, ts, engID, head.Major, head.Minor, head.Patch)
	if err != nil {
		return Polish{}, err
	}

	return Polish{
		ID: id, EngReqID: engID, Major: in.Major, Minor: in.Minor,
		Title: title, Body: body, Status: "ready", CreatedAt: ts,
	}, nil
}

// RunPolish marks a polish done (sync helper for unit tests; web uses Jobs in 0.39.9).
func (s *Store) RunPolish(id string) (Polish, error) {
	p, err := s.GetPolish(id)
	if err != nil {
		return Polish{}, err
	}
	if p.Status == "done" {
		return p, nil
	}
	if err := s.CompletePolishJob(id, JobFinished, "done", ""); err != nil {
		return Polish{}, err
	}
	return s.GetPolish(id)
}

func (s *Store) listBuglistItems() ([]BuglistItem, error) {
	rows, err := s.db.Query(`
SELECT id, eng_req_id, major, minor, title, body, status, created_at,
  COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_buglist_items ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []BuglistItem
	for rows.Next() {
		var b BuglistItem
		if err := rows.Scan(&b.ID, &b.EngReqID, &b.Major, &b.Minor, &b.Title, &b.Body, &b.Status, &b.CreatedAt,
			&b.JobID, &b.JobStatus, &b.JobLastEvent); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	if list == nil {
		list = []BuglistItem{}
	}
	return list, rows.Err()
}

// CreateBuglistInput authors a test-fail stub under a milestone bind.
type CreateBuglistInput struct {
	EngReqID string
	Major    int
	Minor    int
	Title    string
	Body     string
}

// CreateBuglistItem inserts an open buglist row (does not change lifecycle).
func (s *Store) CreateBuglistItem(in CreateBuglistInput) (BuglistItem, error) {
	engID := strings.TrimSpace(in.EngReqID)
	if engID == "" {
		return BuglistItem{}, fmt.Errorf("eng_req_id required")
	}
	title := strings.TrimSpace(in.Title)
	body := strings.TrimSpace(in.Body)
	if title == "" && body == "" {
		return BuglistItem{}, fmt.Errorf("title or body required")
	}
	if title == "" {
		title = truncateRunes(body, 48)
	}
	ts := nowRFC3339()
	id := fmt.Sprintf("bug_%d", time.Now().UnixNano())
	_, err := s.db.Exec(`
INSERT INTO delivery_buglist_items(id, eng_req_id, major, minor, title, body, status, created_at)
VALUES(?,?,?,?,?,?,?,?)`,
		id, engID, in.Major, in.Minor, title, body, "open", ts)
	if err != nil {
		return BuglistItem{}, err
	}
	return BuglistItem{
		ID: id, EngReqID: engID, Major: in.Major, Minor: in.Minor,
		Title: title, Body: body, Status: "open", CreatedAt: ts,
	}, nil
}

func (s *Store) listTestCases() ([]TestCase, error) {
	rows, err := s.db.Query(`
SELECT id, eng_req_id, major, minor, title, body, status, created_at
FROM delivery_test_cases ORDER BY created_at ASC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []TestCase
	for rows.Next() {
		var c TestCase
		if err := rows.Scan(&c.ID, &c.EngReqID, &c.Major, &c.Minor, &c.Title, &c.Body, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	if list == nil {
		list = []TestCase{}
	}
	return list, rows.Err()
}

func (s *Store) listTestCasesForBind(engID string, major, minor int) ([]TestCase, error) {
	rows, err := s.db.Query(`
SELECT id, eng_req_id, major, minor, title, body, status, created_at
FROM delivery_test_cases WHERE eng_req_id=? AND major=? AND minor=?
ORDER BY created_at ASC, id`, engID, major, minor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []TestCase
	for rows.Next() {
		var c TestCase
		if err := rows.Scan(&c.ID, &c.EngReqID, &c.Major, &c.Minor, &c.Title, &c.Body, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	if list == nil {
		list = []TestCase{}
	}
	return list, rows.Err()
}

func (s *Store) listBuglistForBind(engID string, major, minor int) ([]BuglistItem, error) {
	rows, err := s.db.Query(`
SELECT id, eng_req_id, major, minor, title, body, status, created_at,
  COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_buglist_items WHERE eng_req_id=? AND major=? AND minor=?
ORDER BY created_at DESC, id`, engID, major, minor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []BuglistItem
	for rows.Next() {
		var b BuglistItem
		if err := rows.Scan(&b.ID, &b.EngReqID, &b.Major, &b.Minor, &b.Title, &b.Body, &b.Status, &b.CreatedAt,
			&b.JobID, &b.JobStatus, &b.JobLastEvent); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	if list == nil {
		list = []BuglistItem{}
	}
	return list, rows.Err()
}

func (s *Store) findMilestoneByBind(engID string, major, minor int) (Milestone, error) {
	id := fmt.Sprintf("ms_%s_%d_%d", engID, major, minor)
	return s.getMilestone(id)
}

// FindMilestoneByBind resolves ms_{eng}_{major}_{minor}.
func (s *Store) FindMilestoneByBind(engID string, major, minor int) (Milestone, error) {
	return s.findMilestoneByBind(engID, major, minor)
}

// RunTestCasesAllPass marks every case passed without spawning bugs (real-job success fallback).
func (s *Store) RunTestCasesAllPass(engID string, major, minor int) (RunTestCasesResult, error) {
	var out RunTestCasesResult
	engID = strings.TrimSpace(engID)
	if engID == "" {
		return out, fmt.Errorf("eng_req_id required")
	}
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	for _, c := range cases {
		_, err := s.db.Exec(`UPDATE delivery_test_cases SET status=? WHERE id=?`, "passed", c.ID)
		if err != nil {
			return out, err
		}
		c.Status = "passed"
		out.Cases = append(out.Cases, c)
	}
	out.Bugs = []BuglistItem{}
	return out, nil
}

// TestCaseDraft is one case in a batch create.
type TestCaseDraft struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// CreateTestCasesInput batch-creates cases under a bind.
type CreateTestCasesInput struct {
	EngReqID string
	Major    int
	Minor    int
	Cases    []TestCaseDraft
}

// CreateTestCases inserts ready cases for a milestone bind.
func (s *Store) CreateTestCases(in CreateTestCasesInput) ([]TestCase, error) {
	engID := strings.TrimSpace(in.EngReqID)
	if engID == "" {
		return nil, fmt.Errorf("eng_req_id required")
	}
	if len(in.Cases) == 0 {
		return nil, fmt.Errorf("cases required")
	}
	var out []TestCase
	ts := nowRFC3339()
	for i, d := range in.Cases {
		title := strings.TrimSpace(d.Title)
		body := strings.TrimSpace(d.Body)
		if title == "" && body == "" {
			continue
		}
		if title == "" {
			title = truncateRunes(body, 48)
		}
		id := fmt.Sprintf("tc_%d_%d", time.Now().UnixNano(), i)
		_, err := s.db.Exec(`
INSERT INTO delivery_test_cases(id, eng_req_id, major, minor, title, body, status, created_at)
VALUES(?,?,?,?,?,?,?,?)`,
			id, engID, in.Major, in.Minor, title, body, "ready", ts)
		if err != nil {
			return nil, err
		}
		out = append(out, TestCase{
			ID: id, EngReqID: engID, Major: in.Major, Minor: in.Minor,
			Title: title, Body: body, Status: "ready", CreatedAt: ts,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("cases required")
	}
	return out, nil
}

// RunTestCasesResult is the mock Worker write/run outcome.
type RunTestCasesResult struct {
	Cases []TestCase    `json:"cases"`
	Bugs  []BuglistItem `json:"bugs"`
}

// RunTestCases mocks Worker writing/running cases; odd-index cases fail and spawn bugs.
func (s *Store) RunTestCases(engID string, major, minor int) (RunTestCasesResult, error) {
	var out RunTestCasesResult
	engID = strings.TrimSpace(engID)
	if engID == "" {
		return out, fmt.Errorf("eng_req_id required")
	}
	ms, err := s.findMilestoneByBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(ms.ExecuteWorkerID) == "" {
		return out, fmt.Errorf("execute_worker_id required to run tests")
	}
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	if len(cases) == 0 {
		return out, fmt.Errorf("no test cases; draft cases first")
	}
	existingBugs, err := s.listBuglistForBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	openTitles := map[string]bool{}
	for _, b := range existingBugs {
		if b.Status == "open" || b.Status == "fixing" {
			openTitles[b.Title] = true
		}
	}
	for i, c := range cases {
		st := "passed"
		if i%2 == 1 {
			st = "failed"
		}
		_, err := s.db.Exec(`UPDATE delivery_test_cases SET status=? WHERE id=?`, st, c.ID)
		if err != nil {
			return out, err
		}
		c.Status = st
		out.Cases = append(out.Cases, c)
		if st == "failed" {
			bugTitle := "Fail: " + c.Title
			if openTitles[bugTitle] {
				continue
			}
			b, err := s.CreateBuglistItem(CreateBuglistInput{
				EngReqID: engID,
				Major:    major,
				Minor:    minor,
				Title:    bugTitle,
				Body:     "Mock run failed for case: " + c.Title + "\n" + c.Body,
			})
			if err != nil {
				return out, err
			}
			openTitles[bugTitle] = true
			out.Bugs = append(out.Bugs, b)
		}
	}
	if out.Bugs == nil {
		out.Bugs = []BuglistItem{}
	}
	return out, nil
}

// FixBuglistItem marks a bug verified (sync helper for unit tests; web uses Jobs in 0.39.8).
func (s *Store) FixBuglistItem(id string) (BuglistItem, error) {
	b, err := s.GetBuglistItem(id)
	if err != nil {
		return BuglistItem{}, err
	}
	if b.Status != "open" && b.Status != "fixing" {
		return BuglistItem{}, fmt.Errorf("fix requires status open or fixing (got %s)", b.Status)
	}
	if err := s.CompleteBugFixJob(id, JobFinished, "verified", ""); err != nil {
		return BuglistItem{}, err
	}
	return s.GetBuglistItem(id)
}

// RegressBuglistResult is mock regression outcome for a bind.
type RegressBuglistResult struct {
	Bugs  []BuglistItem `json:"bugs"`
	Cases []TestCase    `json:"cases"`
}

// RegressBuglist mocks regression: verified → closed; failed cases → passed when no open bugs left after.
func (s *Store) RegressBuglist(engID string, major, minor int) (RegressBuglistResult, error) {
	var out RegressBuglistResult
	engID = strings.TrimSpace(engID)
	if engID == "" {
		return out, fmt.Errorf("eng_req_id required")
	}
	bugs, err := s.listBuglistForBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	hasVerified := false
	for _, b := range bugs {
		if b.Status == "verified" {
			hasVerified = true
			break
		}
	}
	if !hasVerified {
		return out, fmt.Errorf("no verified bugs to regress")
	}
	for _, b := range bugs {
		switch b.Status {
		case "verified":
			_, err := s.db.Exec(`UPDATE delivery_buglist_items SET status=? WHERE id=?`, "closed", b.ID)
			if err != nil {
				return out, err
			}
			b.Status = "closed"
		case "open", "fixing":
			// stay open — regress not clean yet
		}
		out.Bugs = append(out.Bugs, b)
	}
	// Mark failed cases passed when no open/fixing remain.
	stillOpen := false
	for _, b := range out.Bugs {
		if b.Status == "open" || b.Status == "fixing" {
			stillOpen = true
			break
		}
	}
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	for _, c := range cases {
		if !stillOpen && c.Status == "failed" {
			_, err := s.db.Exec(`UPDATE delivery_test_cases SET status=? WHERE id=?`, "passed", c.ID)
			if err != nil {
				return out, err
			}
			c.Status = "passed"
		}
		out.Cases = append(out.Cases, c)
	}
	if out.Bugs == nil {
		out.Bugs = []BuglistItem{}
	}
	if out.Cases == nil {
		out.Cases = []TestCase{}
	}
	return out, nil
}

func (s *Store) assertPassCampaignClean(engID string, major, minor int) error {
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return err
	}
	if len(cases) == 0 {
		return fmt.Errorf("pass_test requires ≥1 test case and a clean run")
	}
	for _, c := range cases {
		if c.Status != "passed" {
			return fmt.Errorf("pass_test requires all cases passed (case %q is %s)", c.Title, c.Status)
		}
	}
	bugs, err := s.listBuglistForBind(engID, major, minor)
	if err != nil {
		return err
	}
	for _, b := range bugs {
		if b.Status == "open" || b.Status == "fixing" {
			return fmt.Errorf("pass_test blocked: open buglist item %q", b.Title)
		}
	}
	return nil
}

// EnsureSeed ensures default majors; inserts demo eng rows when empty.
func (s *Store) EnsureSeed() error {
	if err := s.ensureDefaultMajors(); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_eng_reqs`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return s.seedDemo()
}

func (s *Store) ensureDefaultMajors() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_majors`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	ts := nowRFC3339()
	_, err := s.db.Exec(`
INSERT INTO delivery_majors(major, title, status, summary, created_at, updated_at) VALUES
(0, '0.x', 'developing', 'Pre-1.0 line — current development toward 1.0.', ?, ?),
(1, '1.0', 'planning', 'First stable generation after 0.x.', ?, ?)`,
		ts, ts, ts, ts)
	return err
}

func (s *Store) seedDemo() error {
	if err := s.ensureDefaultMajors(); err != nil {
		return err
	}
	ts := nowRFC3339()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`
INSERT INTO delivery_eng_reqs(id, worker_id, worker_label, fleet, node, project_rel, title, reqs_rel, status, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"eng_1", "w_flutter_chat", "flutter-chat-local", "local", "dev-node", "flutter-chat-local",
		"Flutter chat delivery", "docs/requirements", "active", ts, ts)
	if err != nil {
		return err
	}
	versions := []struct {
		maj, min, pat                                                 int
		title, status, problem, goals, nonGoals, constraints, success string
	}{
		{0, 33, 1, "Delivery UI shell", LifeAwaitingRoadmap,
			"Need a Worker-scoped, versioned req surface before schema.",
			"UI: bind worker, reqs folder, major.minor.patch browser.",
			"",
			"",
			"Operator can open 0.33.1 and edit fields."},
		{0, 33, 2, "Delivery schema", LifeAwaitingRoadmap,
			"UI mock must become durable store.",
			"SQLite delivery_* + /api/v1/delivery.",
			"",
			"",
			"Reload keeps eng-req versions."},
	}
	for _, v := range versions {
		_, err = tx.Exec(`
INSERT INTO delivery_req_versions(eng_req_id, major, minor, patch, title, status, problem, goals, non_goals, constraints_text, success, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			"eng_1", v.maj, v.min, v.pat, v.title, v.status, v.problem, v.goals, v.nonGoals, v.constraints, v.success, ts)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`
INSERT INTO delivery_designs(id, eng_req_id, major, minor, patch, title, status, chosen_option_id, adr, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"des_demo_1", "eng_1", 0, 33, 1, "Delivery tool layer on anvil-web", "approved", "",
		"UI-first; reqs bind Worker + version M.m.p; schema in 0.33; md is export.", ts)
	if err != nil {
		return err
	}
	demoVers := []struct {
		id, title, gate, arch, class string
		ord                          int
	}{
		{"des_demo_1_v1", "UI shell", "Operator can open Delivery and browse 0.33.",
			"```mermaid\nflowchart LR\n  Web[anvil-web] --> Del[Delivery UI]\n```",
			"delivery.js · MdPreview · delivery_api routes", 1},
		{"des_demo_1_v2", "Schema + API", "SQLite delivery_* survives reload.",
			"```mermaid\nflowchart LR\n  API --> Store\n  Store --> SQLite\n```",
			"delivery.Store · delivery_design_versions", 2},
	}
	for _, v := range demoVers {
		_, err = tx.Exec(`
INSERT INTO delivery_design_versions(id, design_id, sort_order, title, gate, sub_architecture, class_design, status, updated_at)
VALUES(?,?,?,?,?,?,?,?,?)`,
			v.id, "des_demo_1", v.ord, v.title, v.gate, v.arch, v.class, "ready", ts)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`
INSERT INTO delivery_design_version_deps(design_id, version_id, depends_on_id) VALUES(?,?,?)`,
		"des_demo_1", "des_demo_1_v2", "des_demo_1_v1")
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
INSERT INTO delivery_milestones(id, title, status, acceptance, x, y, sort_order) VALUES
('ms_ui','0.33 Delivery UI + schema','in_progress','Worker-bound versioned Requirements UI + SQLite API.',80,120,0),
('ms_export','0.34 Markdown export','planned','Export templates from store.',360,80,1),
('ms_exec','0.39 Execute + gates','planned','Phase jobs + milestone accept → main.',640,140,2)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
INSERT INTO delivery_phases(id, milestone_id, title, status, sort_order) VALUES
('ph_ui_1','ms_ui','Shell + mock data','done',0),
('ph_ui_2','ms_ui','Schema + API wire','in_progress',1),
('ph_ex_1','ms_export','Export templates','planned',0),
('ph_ex_2','ms_exec','Job binding','planned',0)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
INSERT INTO delivery_edges(from_id, to_id) VALUES
('ms_ui','ms_export'),
('ms_export','ms_exec')`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
INSERT INTO delivery_gates(id, kind, ref_type, ref_id, title, status, href, created_at)
VALUES(?,?,?,?,?,?,?,?)`,
		"gate_pending_schema", "req_approve", "req_version", "eng_1:0.33.2",
		"Approve req 0.33.2 (schema)", "pending",
		"#/delivery/requirements/eng_1/0/33/2", ts)
	if err != nil {
		return err
	}
	return tx.Commit()
}
