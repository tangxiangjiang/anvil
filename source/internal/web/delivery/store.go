// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package delivery is the anvil-web Delivery domain store (milestone 0.33).
package delivery

import (
	"database/sql"
	"fmt"
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
  updated_at TEXT NOT NULL
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
  updated_at TEXT NOT NULL
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
CREATE TABLE IF NOT EXISTS delivery_milestones (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'planned',
  acceptance TEXT NOT NULL DEFAULT '',
  x REAL NOT NULL DEFAULT 0,
  y REAL NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS delivery_phases (
  id TEXT PRIMARY KEY,
  milestone_id TEXT NOT NULL REFERENCES delivery_milestones(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'planned',
  sort_order INTEGER NOT NULL DEFAULT 0
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
	return s.ensureDesignOptionOutOfScope()
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

// EngReq is a Worker-bound engineering requirement.
type EngReq struct {
	ID         string       `json:"id"`
	WorkerID   string       `json:"worker_id"`
	Title      string       `json:"title"`
	ReqsRel    string       `json:"reqs_rel"`
	Status     string       `json:"status"`
	Versions   []ReqVersion `json:"versions"`
	CreatedAt  string       `json:"created_at,omitempty"`
	UpdatedAt  string       `json:"updated_at,omitempty"`
	WorkerMeta WorkerRef    `json:"-"`
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

// Design is bound to an eng-req version.
type Design struct {
	ID             string         `json:"id"`
	EngReqID       string         `json:"eng_req_id"`
	Version        string         `json:"version"`
	Major          int            `json:"major"`
	Minor          int            `json:"minor"`
	Patch          int            `json:"patch"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	ChosenOptionID string         `json:"chosen_option_id"`
	ADR            string         `json:"adr"`
	Options        []DesignOption `json:"options"`
	UpdatedAt      string         `json:"updated_at"`
}

// Phase is a step under a milestone.
type Phase struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Milestone is a roadmap canvas node.
type Milestone struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	Acceptance string  `json:"acceptance"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Phases     []Phase `json:"phases"`
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
	Majors     []MajorPlan `json:"majors"`
	TrashCount int         `json:"trash_count"`
	Workers    []WorkerRef `json:"workers"`
	EngReqs    []EngReq    `json:"eng_reqs"`
	Designs    []Design    `json:"designs"`
	Roadmap    Roadmap     `json:"roadmap"`
	Gates      []Gate      `json:"gates"`
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
SELECT id, worker_id, worker_label, fleet, node, project_rel, title, reqs_rel, status, created_at, updated_at
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

// CreateEngReqInput creates a Worker-bound eng-req (or attaches a milestone if worker already bound).
// Milestone body fields follow doc/roadmaps m_*.md: Goals, Overall acceptance, Non-goals, Linked design, Phase list.
type CreateEngReqInput struct {
	Worker         WorkerRef
	Title          string
	ReqsRel        string
	ID             string // optional
	Major          int    // generation (0.x allowed); ignored when AutoMajor
	AutoMajor      bool   // pick DevelopingMajor
	Minor          int    // milestone; 0 = auto next
	MilestoneTitle string // version title; defaults to Title
	Status         string // planned | in_progress | done | draft (default planned)
	Goals          string
	NonGoals       string // unused in Requirements UI; kept for store compat
	Success        string // Completion criteria (capabilities / features to ship)
	Constraints    string // unused in Requirements UI; kept for store compat
	Phases         []MilestonePhaseInput // req versions under the milestone (patch 1..N)
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

// CreateEngReq inserts eng-req + first milestone version, or adds the next milestone on an existing bind.
// Milestone id is major.minor (patch=1 for the first phase under that milestone).
func (s *Store) CreateEngReq(in CreateEngReqInput) (EngReq, error) {
	w := in.Worker
	w.ID = strings.TrimSpace(w.ID)
	if w.ID == "" {
		return EngReq{}, fmt.Errorf("worker_id required")
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

	existingID, found, err := s.EngIDByWorker(w.ID)
	if err != nil {
		return EngReq{}, err
	}
	if found {
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
		_, _ = s.db.Exec(`UPDATE delivery_eng_reqs SET title=?, reqs_rel=?, worker_label=?, fleet=?, node=?, project_rel=?, updated_at=? WHERE id=?`,
			title, reqs, w.Label, w.Fleet, w.Node, w.ProjectRel, ts, existingID)
		vers, _ := s.listVersions(existingID)
		return EngReq{
			ID: existingID, WorkerID: w.ID, Title: title, ReqsRel: reqs, Status: "active",
			UpdatedAt: ts, WorkerMeta: w, Versions: vers,
		}, nil
	}

	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = fmt.Sprintf("eng_%d", time.Now().UnixNano())
	}
	minor := in.Minor
	if minor <= 0 {
		_, minor, err = s.NextMilestone("", major)
		if err != nil {
			return EngReq{}, err
		}
	}
	ts := nowRFC3339()
	tx, err := s.db.Begin()
	if err != nil {
		return EngReq{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`
INSERT INTO delivery_eng_reqs(id, worker_id, worker_label, fleet, node, project_rel, title, reqs_rel, status, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.ID, w.Label, w.Fleet, w.Node, w.ProjectRel, title, reqs, "active", ts, ts)
	if err != nil {
		return EngReq{}, fmt.Errorf("create eng req: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return EngReq{}, err
	}
	if err := s.insertMilestoneVersions(id, major, minor, msTitle, in); err != nil {
		return EngReq{}, err
	}
	vers, _ := s.listVersions(id)
	return EngReq{
		ID: id, WorkerID: w.ID, Title: title, ReqsRel: reqs, Status: "active",
		CreatedAt: ts, UpdatedAt: ts, WorkerMeta: w, Versions: vers,
	}, nil
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
		PatchN                                                    int
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
	rows, err := s.db.Query(`
SELECT id, eng_req_id, major, minor, patch, title, status, chosen_option_id, adr, updated_at
FROM delivery_designs ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	var list []Design
	for rows.Next() {
		var d Design
		if err := rows.Scan(
			&d.ID, &d.EngReqID, &d.Major, &d.Minor, &d.Patch,
			&d.Title, &d.Status, &d.ChosenOptionID, &d.ADR, &d.UpdatedAt,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		d.Version = fmt.Sprintf("%d.%d.%d", d.Major, d.Minor, d.Patch)
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
	ChosenOptionID *string
	ADR            *string
	Options        []DesignOption // partial update by id when set
}

// PatchDesign updates chosen option, ADR, and/or option card fields.
func (s *Store) PatchDesign(id string, in PatchDesignInput) (Design, error) {
	d, err := s.getDesign(id)
	if err != nil {
		return Design{}, err
	}
	// Approved: options + choice locked; ADR may still be patched (polish notes).
	if d.Status == "approved" && (len(in.Options) > 0 || in.ChosenOptionID != nil) {
		return Design{}, fmt.Errorf("approved design is read-only (options / choice locked)")
	}
	if in.ChosenOptionID != nil {
		d.ChosenOptionID = *in.ChosenOptionID
	}
	if in.ADR != nil {
		d.ADR = *in.ADR
	}
	d.UpdatedAt = nowRFC3339()
	_, err = s.db.Exec(`UPDATE delivery_designs SET chosen_option_id=?, adr=?, updated_at=? WHERE id=?`,
		d.ChosenOptionID, d.ADR, d.UpdatedAt, id)
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
	err := s.db.QueryRow(`
SELECT id, eng_req_id, major, minor, patch, title, status, chosen_option_id, adr, updated_at
FROM delivery_designs WHERE id=?`, id).Scan(
		&d.ID, &d.EngReqID, &d.Major, &d.Minor, &d.Patch,
		&d.Title, &d.Status, &d.ChosenOptionID, &d.ADR, &d.UpdatedAt,
	)
	if err != nil {
		return Design{}, fmt.Errorf("design not found")
	}
	d.Version = fmt.Sprintf("%d.%d.%d", d.Major, d.Minor, d.Patch)
	opts, err := s.listOptions(d.ID)
	if err != nil {
		return Design{}, err
	}
	d.Options = opts
	return d, nil
}

// ApproveDesign marks design approved and advances the bound milestone to awaiting_roadmap.
func (s *Store) ApproveDesign(id string) (Design, error) {
	d, err := s.getDesign(id)
	if err != nil {
		return Design{}, err
	}
	if d.Status == "approved" {
		return d, nil
	}
	if strings.TrimSpace(d.ChosenOptionID) == "" {
		return Design{}, fmt.Errorf("choose an option before approve")
	}
	found := false
	for _, o := range d.Options {
		if o.ID == d.ChosenOptionID {
			found = true
			break
		}
	}
	if !found {
		return Design{}, fmt.Errorf("chosen option not found")
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
	return s.getDesign(id)
}

// CreateDesignInput binds a new draft design to a milestone (major.minor).
type CreateDesignInput struct {
	EngReqID string
	Major    int
	Minor    int
	Title    string
}

// CreateDesign inserts a draft design with two stub option cards derived from the req.
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
	goals := strings.TrimSpace(head.Goals)
	if goals == "" {
		goals = strings.TrimSpace(head.Problem)
	}
	opts := []DesignOption{
		{
			ID: id + "_a", Title: "A — Primary approach",
			Summary: "Ship " + title + " with the straightforward path from the requirement.",
			Tradeoffs: "Faster to execute; fewer moving parts.",
			Risks:     "May need a follow-up redesign if constraints tighten.",
			OutOfScope: strings.TrimSpace(head.NonGoals),
		},
		{
			ID: id + "_b", Title: "B — Alternative approach",
			Summary: "Explore a different cut: " + truncateRunes(goals, 120),
			Tradeoffs: "More flexibility; more design/ADR surface.",
			Risks:     "Scope creep if tradeoffs stay vague.",
			OutOfScope: "Defer non-MVP polish and adjacent systems.",
		},
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Design{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`
INSERT INTO delivery_designs(id, eng_req_id, major, minor, patch, title, status, chosen_option_id, adr, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, engID, in.Major, in.Minor, head.Patch, title, "draft", "",
		"Draft from requirement — pick an option, polish ADR, then Approve.", ts)
	if err != nil {
		return Design{}, err
	}
	for i, o := range opts {
		_, err = tx.Exec(`
INSERT INTO delivery_design_options(id, design_id, title, summary, tradeoffs, risks, out_of_scope, sort_order)
VALUES(?,?,?,?,?,?,?,?)`,
			o.ID, id, o.Title, o.Summary, o.Tradeoffs, o.Risks, o.OutOfScope, i)
		if err != nil {
			return Design{}, err
		}
	}
	if err := tx.Commit(); err != nil {
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

func (s *Store) loadRoadmap() (Roadmap, error) {
	var rm Roadmap
	rows, err := s.db.Query(`
SELECT id, title, status, acceptance, x, y FROM delivery_milestones ORDER BY sort_order, id`)
	if err != nil {
		return rm, err
	}
	for rows.Next() {
		var m Milestone
		if err := rows.Scan(&m.ID, &m.Title, &m.Status, &m.Acceptance, &m.X, &m.Y); err != nil {
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
SELECT id, title, status FROM delivery_phases WHERE milestone_id=? ORDER BY sort_order, id`, msID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Phase
	for rows.Next() {
		var p Phase
		if err := rows.Scan(&p.ID, &p.Title, &p.Status); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
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

// Reset clears all delivery tables and reseeds.
func (s *Store) Reset() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM delivery_gates`,
		`DELETE FROM delivery_edges`,
		`DELETE FROM delivery_phases`,
		`DELETE FROM delivery_milestones`,
		`DELETE FROM delivery_design_options`,
		`DELETE FROM delivery_designs`,
		`DELETE FROM delivery_req_versions`,
		`DELETE FROM delivery_eng_reqs`,
		`DELETE FROM delivery_majors`,
		`DELETE FROM delivery_trash`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.seedDemo()
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
		maj, min, pat int
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
		"des_demo_1", "eng_1", 0, 33, 1, "Delivery tool layer on anvil-web", "approved", "opt_a",
		"UI-first; reqs bind Worker + version M.m.p; schema in 0.33; md is export.", ts)
	if err != nil {
		return err
	}
	opts := []struct {
		id, title, summary, tradeoffs, risks, oos string
		ord                                       int
	}{
		{"opt_a", "A — Worker + version tree", "Eng-req on worker; major/minor/patch maps to generation/milestone/phase.", "Clear execution boundaries.", "Nav depth", "Global unbound req inbox", 0},
		{"opt_b", "B — Flat global req list", "No worker binding.", "Simpler list; weak project isolation.", "Cross-talk between projects", "Per-worker generation plan", 1},
	}
	for _, o := range opts {
		_, err = tx.Exec(`
INSERT INTO delivery_design_options(id, design_id, title, summary, tradeoffs, risks, out_of_scope, sort_order)
VALUES(?,?,?,?,?,?,?,?)`, o.id, "des_demo_1", o.title, o.summary, o.tradeoffs, o.risks, o.oos, o.ord)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`
INSERT INTO delivery_milestones(id, title, status, acceptance, x, y, sort_order) VALUES
('ms_ui','0.33 Delivery UI + schema','in_progress','Worker-bound versioned Requirements UI + SQLite API.',80,120,0),
('ms_export','0.34 Markdown export','planned','Export templates from store.',360,80,1),
('ms_exec','0.37 Execute + gates','planned','Phase jobs + milestone accept → main.',640,140,2)`)
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
