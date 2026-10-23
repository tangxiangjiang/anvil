// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const workbenchResumeTTL = 30 * 24 * time.Hour

// WorkbenchSessionRecord is the durable browser-session to SDK-agent mapping.
// AgentID is an opaque local SDK identifier, never a provider credential.
type WorkbenchSessionRecord struct {
	ID        string
	JobID     string
	OwnerID   int64
	WorkerID  string
	AgentID   string
	State     string
	StartedAt string
	UpdatedAt string
}

func (s *Store) ensureWorkbenchSessionTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS delivery_workbench_sessions (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL UNIQUE,
  owner_id INTEGER NOT NULL,
  worker_id TEXT NOT NULL,
  agent_id TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'running',
  resume_claimed INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`)
	return err
}

func (s *Store) BeginWorkbenchSession(rec WorkbenchSessionRecord) error {
	if strings.TrimSpace(rec.ID) == "" || strings.TrimSpace(rec.JobID) == "" || rec.OwnerID <= 0 {
		return fmt.Errorf("session id, job id and owner are required")
	}
	now := nowRFC3339()
	if rec.StartedAt == "" {
		rec.StartedAt = now
	}
	_, err := s.db.Exec(`INSERT INTO delivery_workbench_sessions
(id,job_id,owner_id,worker_id,state,started_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		rec.ID, rec.JobID, rec.OwnerID, rec.WorkerID, "running", rec.StartedAt, now)
	return err
}

func (s *Store) FinishWorkbenchSession(id, agentID, state string) error {
	if state == "" {
		state = "finished"
	}
	_, err := s.db.Exec(`UPDATE delivery_workbench_sessions
SET agent_id=?, state=?, resume_claimed=0, updated_at=? WHERE id=?`,
		agentID, state, nowRFC3339(), id)
	return err
}

func (s *Store) WorkbenchSession(id string) (WorkbenchSessionRecord, error) {
	var out WorkbenchSessionRecord
	err := s.db.QueryRow(`SELECT id,job_id,owner_id,worker_id,agent_id,state,started_at,updated_at
FROM delivery_workbench_sessions WHERE id=?`, id).Scan(
		&out.ID, &out.JobID, &out.OwnerID, &out.WorkerID, &out.AgentID,
		&out.State, &out.StartedAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return out, fmt.Errorf("unknown workbench session")
	}
	return out, err
}

func (s *Store) ListWorkbenchSessions(ownerID int64, limit int) ([]WorkbenchSessionRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,job_id,owner_id,worker_id,agent_id,state,started_at,updated_at
FROM delivery_workbench_sessions WHERE owner_id=? ORDER BY started_at DESC LIMIT ?`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkbenchSessionRecord
	for rows.Next() {
		var rec WorkbenchSessionRecord
		if err := rows.Scan(&rec.ID, &rec.JobID, &rec.OwnerID, &rec.WorkerID,
			&rec.AgentID, &rec.State, &rec.StartedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ClaimWorkbenchResume serializes resume for one agent and enforces owner,
// worker, terminal-state and expiry boundaries. ReleaseWorkbenchResume is called
// when the resumed run reaches a terminal state or fails to start.
func (s *Store) ClaimWorkbenchResume(id string, ownerID int64, workerID string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var rec WorkbenchSessionRecord
	var claimed int
	if err := tx.QueryRow(`SELECT id,job_id,owner_id,worker_id,agent_id,state,started_at,updated_at,resume_claimed
FROM delivery_workbench_sessions WHERE id=?`, id).Scan(
		&rec.ID, &rec.JobID, &rec.OwnerID, &rec.WorkerID, &rec.AgentID,
		&rec.State, &rec.StartedAt, &rec.UpdatedAt, &claimed); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("unknown workbench session")
		}
		return "", err
	}
	if rec.OwnerID != ownerID || rec.WorkerID != workerID {
		return "", fmt.Errorf("resume session is outside this owner or worker scope")
	}
	if rec.State == "running" || claimed != 0 {
		return "", fmt.Errorf("session is already running or being resumed")
	}
	if rec.AgentID == "" {
		return "", fmt.Errorf("session has no resumable agent id")
	}
	updated, err := time.Parse(time.RFC3339, rec.UpdatedAt)
	if err != nil || time.Since(updated) > workbenchResumeTTL {
		return "", fmt.Errorf("resume session has expired")
	}
	res, err := tx.Exec(`UPDATE delivery_workbench_sessions SET resume_claimed=1,updated_at=?
WHERE id=? AND resume_claimed=0`, nowRFC3339(), id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return "", fmt.Errorf("session is already being resumed")
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return rec.AgentID, nil
}

func (s *Store) ReleaseWorkbenchResume(id string) error {
	_, err := s.db.Exec(`UPDATE delivery_workbench_sessions SET resume_claimed=0,updated_at=? WHERE id=?`,
		nowRFC3339(), id)
	return err
}

// FailInterruptedJobs is the web restart protocol. Scheduler ownership and
// approval callbacks are leases and cannot survive this process, so rows left
// running must not be advertised as usable after restart.
func (s *Store) FailInterruptedJobs() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowRFC3339()
	if _, err := tx.Exec(`UPDATE delivery_jobs
SET status='error',error='interrupted by web process restart',finished_at=?
WHERE status IN ('running','queued')`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE delivery_workbench_sessions
SET state='error',resume_claimed=0,updated_at=? WHERE state='running'`, now); err != nil {
		return err
	}
	for _, statement := range []string{
		`UPDATE delivery_phases SET job_status='error',job_last_event='interrupted by web process restart'
		  WHERE job_status IN ('running','queued')`,
		`UPDATE delivery_milestones SET campaign_job_status='error',
		  campaign_job_last_event='interrupted by web process restart',run_state='paused'
		  WHERE campaign_job_status IN ('running','queued')`,
		`UPDATE delivery_buglist_items SET job_status='error',
		  job_last_event='interrupted by web process restart' WHERE job_status IN ('running','queued')`,
		`UPDATE delivery_polishes SET job_status='error',
		  job_last_event='interrupted by web process restart' WHERE job_status IN ('running','queued')`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
