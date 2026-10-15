// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package workspace persists Clusters Workspace / Project entities (milestone 0.34).
package workspace

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Store is the web-side workspace domain.
type Store struct {
	db *sql.DB
}

// Open migrates cluster_* tables on the shared anvil-web DB.
func Open(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("workspace: nil db")
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS cluster_workspaces (
  id TEXT PRIMARY KEY,
  fleet_id TEXT NOT NULL,
  name TEXT NOT NULL,
  token TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cluster_ws_fleet ON cluster_workspaces(fleet_id);
CREATE TABLE IF NOT EXISTS cluster_projects (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES cluster_workspaces(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  name TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(workspace_id, key)
);
CREATE TABLE IF NOT EXISTS cluster_project_deps (
  workspace_id TEXT NOT NULL,
  from_project_id TEXT NOT NULL,
  to_project_id TEXT NOT NULL,
  PRIMARY KEY (from_project_id, to_project_id)
);
CREATE TABLE IF NOT EXISTS cluster_worker_bindings (
  worker_id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES cluster_projects(id) ON DELETE CASCADE,
  fleet_id TEXT NOT NULL DEFAULT '',
  node_id TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS cluster_worker_deps (
  from_worker_id TEXT NOT NULL,
  to_worker_id TEXT NOT NULL,
  PRIMARY KEY (from_worker_id, to_worker_id)
);
CREATE TABLE IF NOT EXISTS cluster_scheduler_tokens (
  token TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  fleet_id TEXT NOT NULL
);
`)
	return err
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:]))
}

func newToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "ws_" + hex.EncodeToString(b[:])
}

// Workspace is a collaboration domain under a scheduler fleet.
type Workspace struct {
	ID        string `json:"id"`
	FleetID   string `json:"fleet_id"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Project is a logical engineering unit inside a Workspace.
type Project struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	DependsOn   []string `json:"depends_on"` // project ids
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// WorkerBinding links a live agentd worker to a Project.
type WorkerBinding struct {
	WorkerID  string `json:"worker_id"`
	ProjectID string `json:"project_id"`
	FleetID   string `json:"fleet_id"`
	NodeID    string `json:"node_id"`
	UpdatedAt string `json:"updated_at"`
}

// EnsureDefaultWorkspace creates "Default" if the fleet has none.
func (s *Store) EnsureDefaultWorkspace(fleetID, name string) (Workspace, error) {
	list, err := s.ListWorkspaces(fleetID)
	if err != nil {
		return Workspace{}, err
	}
	if len(list) > 0 {
		return list[0], nil
	}
	if name == "" {
		name = "Default"
	}
	return s.CreateWorkspace(fleetID, name, "Auto-created workspace for this scheduler.")
}

// ListWorkspaces returns workspaces for a fleet.
func (s *Store) ListWorkspaces(fleetID string) ([]Workspace, error) {
	rows, err := s.db.Query(`
SELECT id, fleet_id, name, token, summary, created_at, updated_at
FROM cluster_workspaces WHERE fleet_id=? ORDER BY created_at`, fleetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.FleetID, &w.Name, &w.Token, &w.Summary, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, w)
	}
	return list, rows.Err()
}

// CreateWorkspace inserts a workspace and registers its token.
func (s *Store) CreateWorkspace(fleetID, name, summary string) (Workspace, error) {
	fleetID = strings.TrimSpace(fleetID)
	name = strings.TrimSpace(name)
	if fleetID == "" || name == "" {
		return Workspace{}, fmt.Errorf("fleet_id and name required")
	}
	ts := nowRFC3339()
	w := Workspace{
		ID: newID("ws"), FleetID: fleetID, Name: name, Token: newToken(),
		Summary: summary, CreatedAt: ts, UpdatedAt: ts,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Workspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`
INSERT INTO cluster_workspaces(id, fleet_id, name, token, summary, created_at, updated_at)
VALUES(?,?,?,?,?,?,?)`, w.ID, w.FleetID, w.Name, w.Token, w.Summary, w.CreatedAt, w.UpdatedAt)
	if err != nil {
		return Workspace{}, err
	}
	_, err = tx.Exec(`INSERT INTO cluster_scheduler_tokens(token, workspace_id, fleet_id) VALUES(?,?,?)`,
		w.Token, w.ID, w.FleetID)
	if err != nil {
		return Workspace{}, err
	}
	if err := tx.Commit(); err != nil {
		return Workspace{}, err
	}
	return w, nil
}

// GetWorkspace loads one workspace.
func (s *Store) GetWorkspace(id string) (Workspace, error) {
	var w Workspace
	err := s.db.QueryRow(`
SELECT id, fleet_id, name, token, summary, created_at, updated_at
FROM cluster_workspaces WHERE id=?`, id).Scan(
		&w.ID, &w.FleetID, &w.Name, &w.Token, &w.Summary, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace not found")
	}
	return w, nil
}

// LookupToken returns workspace id for a join token.
func (s *Store) LookupToken(token string) (workspaceID, fleetID string, ok bool) {
	err := s.db.QueryRow(`SELECT workspace_id, fleet_id FROM cluster_scheduler_tokens WHERE token=?`, token).
		Scan(&workspaceID, &fleetID)
	return workspaceID, fleetID, err == nil
}

// DeleteWorkspace removes a workspace and cascaded projects.
func (s *Store) DeleteWorkspace(id string) error {
	w, err := s.GetWorkspace(id)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.Exec(`DELETE FROM cluster_scheduler_tokens WHERE workspace_id=?`, id)
	_, _ = tx.Exec(`DELETE FROM cluster_project_deps WHERE workspace_id=?`, id)
	_, err = tx.Exec(`DELETE FROM cluster_workspaces WHERE id=?`, id)
	if err != nil {
		return err
	}
	_ = w
	return tx.Commit()
}

// ListProjects returns projects in a workspace.
func (s *Store) ListProjects(workspaceID string) ([]Project, error) {
	rows, err := s.db.Query(`
SELECT id, workspace_id, key, name, summary, created_at, updated_at
FROM cluster_projects WHERE workspace_id=? ORDER BY name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.Summary, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range list {
		deps, err := s.listProjectDeps(list[i].ID)
		if err != nil {
			return nil, err
		}
		list[i].DependsOn = deps
	}
	return list, nil
}

func (s *Store) listProjectDeps(fromID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT to_project_id FROM cluster_project_deps WHERE from_project_id=?`, fromID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CreateProjectInput creates a project with optional outgoing deps.
type CreateProjectInput struct {
	WorkspaceID string
	Key         string
	Name        string
	Summary     string
	DependsOn   []string
}

// CreateProject inserts a project.
func (s *Store) CreateProject(in CreateProjectInput) (Project, error) {
	wsID := strings.TrimSpace(in.WorkspaceID)
	key := strings.TrimSpace(in.Key)
	name := strings.TrimSpace(in.Name)
	if wsID == "" || name == "" {
		return Project{}, fmt.Errorf("workspace_id and name required")
	}
	if key == "" {
		key = slugKey(name)
	}
	if _, err := s.GetWorkspace(wsID); err != nil {
		return Project{}, err
	}
	ts := nowRFC3339()
	p := Project{
		ID: newID("proj"), WorkspaceID: wsID, Key: key, Name: name,
		Summary: in.Summary, CreatedAt: ts, UpdatedAt: ts,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Project{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`
INSERT INTO cluster_projects(id, workspace_id, key, name, summary, created_at, updated_at)
VALUES(?,?,?,?,?,?,?)`, p.ID, p.WorkspaceID, p.Key, p.Name, p.Summary, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return Project{}, err
	}
	for _, to := range in.DependsOn {
		to = strings.TrimSpace(to)
		if to == "" || to == p.ID {
			continue
		}
		_, err = tx.Exec(`
INSERT OR IGNORE INTO cluster_project_deps(workspace_id, from_project_id, to_project_id)
VALUES(?,?,?)`, wsID, p.ID, to)
		if err != nil {
			return Project{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	deps, _ := s.listProjectDeps(p.ID)
	p.DependsOn = deps
	return p, nil
}

func slugKey(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		if r == ' ' || r == '/' {
			return '-'
		}
		return -1
	}, s)
	if s == "" {
		return newID("key")
	}
	return s
}

// GetProject loads one project.
func (s *Store) GetProject(id string) (Project, error) {
	var p Project
	err := s.db.QueryRow(`
SELECT id, workspace_id, key, name, summary, created_at, updated_at
FROM cluster_projects WHERE id=?`, id).Scan(
		&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.Summary, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Project{}, fmt.Errorf("project not found")
	}
	deps, err := s.listProjectDeps(p.ID)
	if err != nil {
		return Project{}, err
	}
	p.DependsOn = deps
	return p, nil
}

// SetProjectDeps replaces outgoing depends_on for a project.
func (s *Store) SetProjectDeps(projectID string, dependsOn []string) error {
	p, err := s.GetProject(projectID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM cluster_project_deps WHERE from_project_id=?`, projectID); err != nil {
		return err
	}
	for _, to := range dependsOn {
		to = strings.TrimSpace(to)
		if to == "" || to == projectID {
			continue
		}
		_, err = tx.Exec(`
INSERT INTO cluster_project_deps(workspace_id, from_project_id, to_project_id) VALUES(?,?,?)`,
			p.WorkspaceID, projectID, to)
		if err != nil {
			return err
		}
	}
	_, _ = tx.Exec(`UPDATE cluster_projects SET updated_at=? WHERE id=?`, nowRFC3339(), projectID)
	return tx.Commit()
}

// DeleteProject removes a project.
func (s *Store) DeleteProject(id string) error {
	if _, err := s.GetProject(id); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.Exec(`DELETE FROM cluster_project_deps WHERE from_project_id=? OR to_project_id=?`, id, id)
	_, err = tx.Exec(`DELETE FROM cluster_projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// BindWorker attaches a live worker to a project.
func (s *Store) BindWorker(workerID, projectID, fleetID, nodeID string) error {
	if _, err := s.GetProject(projectID); err != nil {
		return err
	}
	_, err := s.db.Exec(`
INSERT INTO cluster_worker_bindings(worker_id, project_id, fleet_id, node_id, updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(worker_id) DO UPDATE SET
  project_id=excluded.project_id, fleet_id=excluded.fleet_id, node_id=excluded.node_id, updated_at=excluded.updated_at`,
		workerID, projectID, fleetID, nodeID, nowRFC3339())
	return err
}

// UnbindWorker removes a worker→project binding.
func (s *Store) UnbindWorker(workerID string) error {
	_, err := s.db.Exec(`DELETE FROM cluster_worker_bindings WHERE worker_id=?`, workerID)
	_, _ = s.db.Exec(`DELETE FROM cluster_worker_deps WHERE from_worker_id=? OR to_worker_id=?`, workerID, workerID)
	return err
}

// ListBindingsForProject returns worker ids bound to a project.
func (s *Store) ListBindingsForProject(projectID string) ([]WorkerBinding, error) {
	rows, err := s.db.Query(`
SELECT worker_id, project_id, fleet_id, node_id, updated_at
FROM cluster_worker_bindings WHERE project_id=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []WorkerBinding
	for rows.Next() {
		var b WorkerBinding
		if err := rows.Scan(&b.WorkerID, &b.ProjectID, &b.FleetID, &b.NodeID, &b.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

// ListBindingsForWorkspace returns all bindings under projects of a workspace.
func (s *Store) ListBindingsForWorkspace(workspaceID string) ([]WorkerBinding, error) {
	rows, err := s.db.Query(`
SELECT b.worker_id, b.project_id, b.fleet_id, b.node_id, b.updated_at
FROM cluster_worker_bindings b
JOIN cluster_projects p ON p.id = b.project_id
WHERE p.workspace_id=?`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []WorkerBinding
	for rows.Next() {
		var b WorkerBinding
		if err := rows.Scan(&b.WorkerID, &b.ProjectID, &b.FleetID, &b.NodeID, &b.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

// SetWorkerDeps replaces outgoing worker depends_on.
func (s *Store) SetWorkerDeps(fromWorkerID string, toWorkerIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM cluster_worker_deps WHERE from_worker_id=?`, fromWorkerID); err != nil {
		return err
	}
	for _, to := range toWorkerIDs {
		to = strings.TrimSpace(to)
		if to == "" || to == fromWorkerID {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO cluster_worker_deps(from_worker_id, to_worker_id) VALUES(?,?)`, fromWorkerID, to); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListWorkerDeps returns workers that fromWorker depends on.
func (s *Store) ListWorkerDeps(fromWorkerID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT to_worker_id FROM cluster_worker_deps WHERE from_worker_id=?`, fromWorkerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GraphPayload is Workspace View data.
type GraphPayload struct {
	Projects []Project         `json:"projects"`
	Edges    []ProjectEdge     `json:"edges"`
	Workers  []WorkerBinding   `json:"workers"`
}

// ProjectEdge is a depends_on edge.
type ProjectEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph builds the project dependency graph for a workspace.
func (s *Store) Graph(workspaceID string) (GraphPayload, error) {
	projs, err := s.ListProjects(workspaceID)
	if err != nil {
		return GraphPayload{}, err
	}
	binds, err := s.ListBindingsForWorkspace(workspaceID)
	if err != nil {
		return GraphPayload{}, err
	}
	var edges []ProjectEdge
	for _, p := range projs {
		for _, to := range p.DependsOn {
			edges = append(edges, ProjectEdge{From: p.ID, To: to})
		}
	}
	return GraphPayload{Projects: projs, Edges: edges, Workers: binds}, nil
}

// AllTokens returns token→workspace for scheduler sync.
func (s *Store) AllTokens() ([]struct{ Token, WorkspaceID, FleetID string }, error) {
	rows, err := s.db.Query(`SELECT token, workspace_id, fleet_id FROM cluster_scheduler_tokens`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []struct{ Token, WorkspaceID, FleetID string }
	for rows.Next() {
		var t struct{ Token, WorkspaceID, FleetID string }
		if err := rows.Scan(&t.Token, &t.WorkspaceID, &t.FleetID); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
