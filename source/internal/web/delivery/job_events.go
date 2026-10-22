// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Job event persistence (p_0.45.12).
//
// Before this, a job's events lived in a 200-frame in-memory ring and the
// database kept exactly one string, job_last_event. A finished job's transcript
// was gone, so "why did this fail" could not be answered from the console at all
// — the only trace was a red badge.
//
// The table is an append-only log with a per-job bound, pruned on write. It grows
// at event rate, and a console that fills the disk it runs on would be a worse
// problem than the one this solves.

const (
	// jobEventKeepPerJob bounds the transcript kept for one job. A long agent run
	// emits thousands of events; the tail is what a person reads, and the count
	// is generous enough that the failure at the end is always in view.
	jobEventKeepPerJob = 2000
	// jobEventKeepPerWorkspace bounds how many jobs keep a transcript. Older jobs
	// lose theirs first; the job row itself stays, so the history list still
	// knows the job happened.
	jobEventKeepPerWorkspace = 500
)

// JobEvent is one persisted stream event.
type JobEvent struct {
	JobID   string `json:"job_id"`
	Seq     int    `json:"seq"`
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`
	Args    string `json:"args,omitempty"`
	Content string `json:"content,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
	Step    int    `json:"step,omitempty"`
	RunID   string `json:"run_id,omitempty"`
	Ts      string `json:"ts"`
}

// JobRecord is the header of a persisted job: what the history list shows.
type JobRecord struct {
	JobID        string `json:"job_id"`
	Kind         string `json:"kind"`
	EngReqID     string `json:"eng_req_id,omitempty"`
	MilestoneID  string `json:"milestone_id,omitempty"`
	PhaseID      string `json:"phase_id,omitempty"`
	WorkerID     string `json:"worker_id,omitempty"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	EventCount   int    `json:"event_count"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
	DurationSecs int    `json:"duration_secs,omitempty"`
}

func (s *Store) ensureJobEventTables() error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS delivery_jobs (
  job_id TEXT PRIMARY KEY,
  kind TEXT NOT NULL DEFAULT '',
  eng_req_id TEXT NOT NULL DEFAULT '',
  milestone_id TEXT NOT NULL DEFAULT '',
  phase_id TEXT NOT NULL DEFAULT '',
  worker_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'running',
  error TEXT NOT NULL DEFAULT '',
  event_count INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL DEFAULT '',
  truncated INTEGER NOT NULL DEFAULT 0
)`,
		`CREATE INDEX IF NOT EXISTS delivery_jobs_started ON delivery_jobs(started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS delivery_job_events (
  job_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  type TEXT NOT NULL DEFAULT '',
  text TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  args TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  is_error INTEGER NOT NULL DEFAULT 0,
  step INTEGER NOT NULL DEFAULT 0,
  run_id TEXT NOT NULL DEFAULT '',
  ts TEXT NOT NULL,
  PRIMARY KEY (job_id, seq)
)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// BeginJob records a job so its events have a row to belong to.
func (s *Store) BeginJob(rec JobRecord) error {
	if strings.TrimSpace(rec.JobID) == "" {
		return fmt.Errorf("job_id is required")
	}
	ts := rec.StartedAt
	if ts == "" {
		ts = nowRFC3339()
	}
	if rec.Status == "" {
		rec.Status = "running"
	}
	_, err := s.db.Exec(`
INSERT INTO delivery_jobs(job_id, kind, eng_req_id, milestone_id, phase_id, worker_id, status, started_at)
VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(job_id) DO UPDATE SET
  kind=excluded.kind, eng_req_id=excluded.eng_req_id, milestone_id=excluded.milestone_id,
  phase_id=excluded.phase_id, worker_id=excluded.worker_id, status=excluded.status`,
		strings.TrimSpace(rec.JobID), rec.Kind, rec.EngReqID, rec.MilestoneID, rec.PhaseID, rec.WorkerID, rec.Status, ts)
	return err
}

// AppendJobEvent writes one event and prunes the job's own tail.
//
// Pruning happens here rather than in a background sweep because this is the only
// writer: a bound enforced at the write cannot drift from what was written.
func (s *Store) AppendJobEvent(ev JobEvent) error {
	if strings.TrimSpace(ev.JobID) == "" {
		return fmt.Errorf("job_id is required")
	}
	if ev.Ts == "" {
		ev.Ts = nowRFC3339()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var next int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(seq),0)+1 FROM delivery_job_events WHERE job_id=?`, ev.JobID,
	).Scan(&next); err != nil {
		return err
	}
	if _, err := tx.Exec(`
INSERT INTO delivery_job_events(job_id, seq, type, text, name, args, content, is_error, step, run_id, ts)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		ev.JobID, next, ev.Type, ev.Text, ev.Name, ev.Args, ev.Content, boolInt(ev.IsError), ev.Step, ev.RunID, ev.Ts,
	); err != nil {
		return err
	}
	// Keep the newest jobEventKeepPerJob: the tail is what a person reads.
	if _, err := tx.Exec(`
DELETE FROM delivery_job_events
WHERE job_id=? AND seq <= (
  SELECT COALESCE(MAX(seq),0) - ? FROM delivery_job_events WHERE job_id=?
)`, ev.JobID, jobEventKeepPerJob, ev.JobID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM delivery_job_events WHERE job_id=?`, ev.JobID).Scan(&count); err != nil {
		return err
	}
	truncated := 0
	if count >= jobEventKeepPerJob {
		truncated = 1
	}
	if _, err := tx.Exec(
		`UPDATE delivery_jobs SET event_count=?, truncated=? WHERE job_id=?`,
		count, truncated, ev.JobID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishJob records the terminal state.
func (s *Store) FinishJob(jobID, status, errText string) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("job_id is required")
	}
	ended := nowRFC3339()
	if status == "" {
		status = "finished"
	}
	_, err := s.db.Exec(`
UPDATE delivery_jobs SET status=?, error=?, finished_at=? WHERE job_id=?`,
		status, errText, ended, jobID)
	if err != nil {
		return err
	}
	return s.pruneJobs()
}

// pruneJobs bounds how many jobs keep a transcript.
func (s *Store) pruneJobs() error {
	_, err := s.db.Exec(`
DELETE FROM delivery_job_events WHERE job_id IN (
  SELECT job_id FROM delivery_jobs
  WHERE job_id NOT IN (
    SELECT job_id FROM delivery_jobs ORDER BY started_at DESC LIMIT ?
  )
)`, jobEventKeepPerWorkspace)
	return err
}

// ListJobs returns the history, newest first, optionally filtered.
func (s *Store) ListJobs(filter JobFilter) ([]JobRecord, error) {
	q := `SELECT job_id, kind, eng_req_id, milestone_id, phase_id, worker_id, status, error,
                event_count, started_at, finished_at, truncated
         FROM delivery_jobs WHERE 1=1`
	var args []any
	if f := strings.TrimSpace(filter.MilestoneID); f != "" {
		q += ` AND milestone_id=?`
		args = append(args, f)
	}
	if f := strings.TrimSpace(filter.WorkerID); f != "" {
		q += ` AND worker_id=?`
		args = append(args, f)
	}
	if f := strings.TrimSpace(filter.Status); f != "" {
		q += ` AND status=?`
		args = append(args, f)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobRecord{}
	for rows.Next() {
		var r JobRecord
		var trunc int
		if err := rows.Scan(&r.JobID, &r.Kind, &r.EngReqID, &r.MilestoneID, &r.PhaseID,
			&r.WorkerID, &r.Status, &r.Error, &r.EventCount, &r.StartedAt, &r.FinishedAt, &trunc); err != nil {
			return nil, err
		}
		r.Truncated = trunc == 1
		r.DurationSecs = durationSecs(r.StartedAt, r.FinishedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

// JobFilter narrows a history query.
type JobFilter struct {
	MilestoneID string
	WorkerID    string
	Status      string
	Limit       int
}

// JobEvents returns the persisted transcript for one job, in order.
func (s *Store) JobEvents(jobID string) ([]JobEvent, error) {
	rows, err := s.db.Query(`
SELECT job_id, seq, type, text, name, args, content, is_error, step, run_id, ts
FROM delivery_job_events WHERE job_id=? ORDER BY seq`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobEvent{}
	for rows.Next() {
		var ev JobEvent
		var isErr int
		if err := rows.Scan(&ev.JobID, &ev.Seq, &ev.Type, &ev.Text, &ev.Name, &ev.Args,
			&ev.Content, &isErr, &ev.Step, &ev.RunID, &ev.Ts); err != nil {
			return nil, err
		}
		ev.IsError = isErr == 1
		out = append(out, ev)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// durationSecs is seconds between two RFC3339 stamps, 0 when either is missing.
func durationSecs(start, end string) int {
	if start == "" || end == "" {
		return 0
	}
	a, err1 := time.Parse(time.RFC3339, start)
	b, err2 := time.Parse(time.RFC3339, end)
	if err1 != nil || err2 != nil {
		return 0
	}
	d := int(b.Sub(a).Seconds())
	if d < 0 {
		return 0
	}
	return d
}

func nullable(s string) any {
	if s == "" {
		return sql.NullString{}
	}
	return s
}
