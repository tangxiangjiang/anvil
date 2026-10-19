// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"database/sql"
	"fmt"
	"strings"
)

// GetBuglistItem loads one buglist row with job fields.
func (s *Store) GetBuglistItem(id string) (BuglistItem, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return BuglistItem{}, fmt.Errorf("buglist id required")
	}
	var b BuglistItem
	err := s.db.QueryRow(`
SELECT id, eng_req_id, major, minor, title, body, status, created_at,
  COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_buglist_items WHERE id=?`, id).Scan(
		&b.ID, &b.EngReqID, &b.Major, &b.Minor, &b.Title, &b.Body, &b.Status, &b.CreatedAt,
		&b.JobID, &b.JobStatus, &b.JobLastEvent)
	if err != nil {
		if err == sql.ErrNoRows {
			return BuglistItem{}, fmt.Errorf("buglist item not found")
		}
		return BuglistItem{}, err
	}
	return b, nil
}

// GetPolish loads one polish row with job fields.
func (s *Store) GetPolish(id string) (Polish, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Polish{}, fmt.Errorf("polish id required")
	}
	var p Polish
	err := s.db.QueryRow(`
SELECT id, eng_req_id, major, minor, title, body, status, created_at,
  COALESCE(job_id,''), COALESCE(job_status,''), COALESCE(job_last_event,'')
FROM delivery_polishes WHERE id=?`, id).Scan(
		&p.ID, &p.EngReqID, &p.Major, &p.Minor, &p.Title, &p.Body, &p.Status, &p.CreatedAt,
		&p.JobID, &p.JobStatus, &p.JobLastEvent)
	if err != nil {
		if err == sql.ErrNoRows {
			return Polish{}, fmt.Errorf("polish not found")
		}
		return Polish{}, err
	}
	return p, nil
}

// SetCampaignJob records the active Run/Regress job on a milestone.
func (s *Store) SetCampaignJob(msID, jobID, status, kind, event string) error {
	msID = strings.TrimSpace(msID)
	if msID == "" {
		return fmt.Errorf("milestone id required")
	}
	_, err := s.db.Exec(`
UPDATE delivery_milestones SET campaign_job_id=?, campaign_job_status=?, campaign_job_kind=?, campaign_job_last_event=?
WHERE id=?`,
		strings.TrimSpace(jobID), strings.TrimSpace(status), strings.TrimSpace(kind), truncateRunes(event, 240), msID)
	return err
}

// AppendCampaignJobEvent updates status + last event for the milestone campaign job.
func (s *Store) AppendCampaignJobEvent(msID, status, event string) error {
	msID = strings.TrimSpace(msID)
	if msID == "" {
		return fmt.Errorf("milestone id required")
	}
	_, err := s.db.Exec(`
UPDATE delivery_milestones SET campaign_job_status=?, campaign_job_last_event=? WHERE id=?`,
		strings.TrimSpace(status), truncateRunes(event, 240), msID)
	return err
}

// ClearCampaignJob clears the active campaign job fields after completion.
func (s *Store) ClearCampaignJob(msID, status, event string) error {
	return s.SetCampaignJob(msID, "", status, "", event)
}

// SetBugJob records a Fix job on a buglist item and moves status to fixing.
func (s *Store) SetBugJob(id, jobID, status, event string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("buglist id required")
	}
	_, err := s.db.Exec(`
UPDATE delivery_buglist_items SET job_id=?, job_status=?, job_last_event=?, status=? WHERE id=?`,
		strings.TrimSpace(jobID), strings.TrimSpace(status), truncateRunes(event, 240), "fixing", id)
	return err
}

// AppendBugJobEvent updates bug job status/event without changing lifecycle status.
func (s *Store) AppendBugJobEvent(id, jobStatus, event string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("buglist id required")
	}
	_, err := s.db.Exec(`
UPDATE delivery_buglist_items SET job_status=?, job_last_event=? WHERE id=?`,
		strings.TrimSpace(jobStatus), truncateRunes(event, 240), id)
	return err
}

// CompleteBugFixJob finishes a fix job: verified on success, open on error.
func (s *Store) CompleteBugFixJob(id, jobStatus, event, _ string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("buglist id required")
	}
	bugStatus := "open"
	if jobStatus == JobFinished {
		bugStatus = "verified"
	}
	_, err := s.db.Exec(`
UPDATE delivery_buglist_items SET job_status=?, job_last_event=?, status=? WHERE id=?`,
		strings.TrimSpace(jobStatus), truncateRunes(event, 240), bugStatus, id)
	return err
}

// SetPolishJob records a polish run job and sets status running.
func (s *Store) SetPolishJob(id, jobID, status, event string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("polish id required")
	}
	_, err := s.db.Exec(`
UPDATE delivery_polishes SET job_id=?, job_status=?, job_last_event=?, status=? WHERE id=?`,
		strings.TrimSpace(jobID), strings.TrimSpace(status), truncateRunes(event, 240), "running", id)
	return err
}

// AppendPolishJobEvent updates polish job event fields.
func (s *Store) AppendPolishJobEvent(id, jobStatus, event string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("polish id required")
	}
	_, err := s.db.Exec(`
UPDATE delivery_polishes SET job_status=?, job_last_event=? WHERE id=?`,
		strings.TrimSpace(jobStatus), truncateRunes(event, 240), id)
	return err
}

// CompletePolishJob marks polish done on finished, ready on error.
func (s *Store) CompletePolishJob(id, jobStatus, event, _ string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("polish id required")
	}
	st := "ready"
	if jobStatus == JobFinished {
		st = "done"
	}
	_, err := s.db.Exec(`
UPDATE delivery_polishes SET job_status=?, job_last_event=?, status=? WHERE id=?`,
		strings.TrimSpace(jobStatus), truncateRunes(event, 240), st, id)
	return err
}

// AssertCampaignRunnable validates worker + cases (and no active campaign job).
func (s *Store) AssertCampaignRunnable(engID string, major, minor int) (Milestone, error) {
	ms, err := s.findMilestoneByBind(engID, major, minor)
	if err != nil {
		return Milestone{}, err
	}
	if strings.TrimSpace(ms.ExecuteWorkerID) == "" {
		return Milestone{}, fmt.Errorf("execute_worker_id required to run tests")
	}
	if ms.CampaignJobStatus == JobRunning || ms.CampaignJobStatus == JobQueued {
		return Milestone{}, fmt.Errorf("campaign job already %s", ms.CampaignJobStatus)
	}
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return Milestone{}, err
	}
	if len(cases) == 0 {
		return Milestone{}, fmt.Errorf("no test cases; draft cases first")
	}
	return ms, nil
}

// AssertRegressRunnable validates verified bugs exist and no active campaign job.
func (s *Store) AssertRegressRunnable(engID string, major, minor int) (Milestone, error) {
	ms, err := s.findMilestoneByBind(engID, major, minor)
	if err != nil {
		return Milestone{}, err
	}
	if strings.TrimSpace(ms.ExecuteWorkerID) == "" {
		return Milestone{}, fmt.Errorf("execute_worker_id required to regress")
	}
	if ms.CampaignJobStatus == JobRunning || ms.CampaignJobStatus == JobQueued {
		return Milestone{}, fmt.Errorf("campaign job already %s", ms.CampaignJobStatus)
	}
	bugs, err := s.listBuglistForBind(engID, major, minor)
	if err != nil {
		return Milestone{}, err
	}
	hasVerified := false
	for _, b := range bugs {
		if b.Status == "verified" {
			hasVerified = true
			break
		}
	}
	if !hasVerified {
		return Milestone{}, fmt.Errorf("no verified bugs to regress")
	}
	return ms, nil
}

// BuildTestRunPrompt composes the Worker prompt for a campaign test run.
func (s *Store) BuildTestRunPrompt(engID string, major, minor int) (string, error) {
	ms, err := s.findMilestoneByBind(engID, major, minor)
	if err != nil {
		return "", err
	}
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return "", err
	}
	project := s.projectRelForBind(engID)
	var b strings.Builder
	fmt.Fprintf(&b, "Milestone %s (%d.%d) — TEST RUN\n", ms.Title, major, minor)
	if project != "" {
		fmt.Fprintf(&b, "Worker checkout: %s\n", project)
	}
	if note := strings.TrimSpace(ms.TestNote); note != "" {
		fmt.Fprintf(&b, "Test note: %s\n", truncateRunes(note, 400))
	}
	b.WriteString("Execute these cases against the Worker checkout. For each case, report passed or failed.\n")
	b.WriteString("When finished, append a trailer:\nANVIL_CAMPAIGN_RESULT:\n")
	b.WriteString(`{"cases":[{"id":"<case_id>","status":"passed|failed"}]}` + "\n\n")
	b.WriteString("Cases:\n")
	for i, c := range cases {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, c.ID, c.Title)
		if c.Body != "" {
			fmt.Fprintf(&b, "   %s\n", truncateRunes(c.Body, 400))
		}
	}
	b.WriteString("\nDo not implement new features; only run/verify these cases.\n")
	return b.String(), nil
}

// BuildBugFixPrompt composes the Worker prompt for fixing one bug.
func (s *Store) BuildBugFixPrompt(bugID string) (string, error) {
	b, err := s.GetBuglistItem(bugID)
	if err != nil {
		return "", err
	}
	ms, err := s.findMilestoneByBind(b.EngReqID, b.Major, b.Minor)
	if err != nil {
		return "", err
	}
	project := s.projectRelForBind(b.EngReqID)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Milestone %s (%d.%d) — BUG FIX\n", ms.Title, b.Major, b.Minor)
	if project != "" {
		fmt.Fprintf(&sb, "Worker checkout: %s\n", project)
	}
	fmt.Fprintf(&sb, "Bug id: %s\nTitle: %s\n", b.ID, b.Title)
	if b.Body != "" {
		fmt.Fprintf(&sb, "Details:\n%s\n", truncateRunes(b.Body, 1200))
	}
	sb.WriteString("\nImplement a minimal fix on the current branch; commit when done.\n")
	return sb.String(), nil
}

// BuildRegressPrompt composes the Worker prompt for regression after fixes.
func (s *Store) BuildRegressPrompt(engID string, major, minor int) (string, error) {
	ms, err := s.findMilestoneByBind(engID, major, minor)
	if err != nil {
		return "", err
	}
	bugs, err := s.listBuglistForBind(engID, major, minor)
	if err != nil {
		return "", err
	}
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return "", err
	}
	project := s.projectRelForBind(engID)
	var b strings.Builder
	fmt.Fprintf(&b, "Milestone %s (%d.%d) — REGRESSION\n", ms.Title, major, minor)
	if project != "" {
		fmt.Fprintf(&b, "Worker checkout: %s\n", project)
	}
	b.WriteString("Re-run verification for previously failing cases after fixes.\nVerified bugs:\n")
	for _, bug := range bugs {
		if bug.Status != "verified" {
			continue
		}
		fmt.Fprintf(&b, "- [%s] %s\n", bug.ID, bug.Title)
	}
	b.WriteString("\nCases:\n")
	for i, c := range cases {
		fmt.Fprintf(&b, "%d. [%s] %s (%s)\n", i+1, c.ID, c.Title, c.Status)
	}
	b.WriteString("\nConfirm regressions are clean; commit only if a small verify fix is required.\n")
	return b.String(), nil
}

// BuildPolishPrompt composes the Worker prompt for a polish item.
func (s *Store) BuildPolishPrompt(polishID string) (string, error) {
	p, err := s.GetPolish(polishID)
	if err != nil {
		return "", err
	}
	ms, err := s.findMilestoneByBind(p.EngReqID, p.Major, p.Minor)
	if err != nil {
		return "", err
	}
	project := s.projectRelForBind(p.EngReqID)
	var b strings.Builder
	fmt.Fprintf(&b, "Milestone %s (%d.%d) — POLISH\n", ms.Title, p.Major, p.Minor)
	if project != "" {
		fmt.Fprintf(&b, "Worker checkout: %s\n", project)
	}
	fmt.Fprintf(&b, "Polish: %s\n", p.Title)
	if p.Body != "" {
		fmt.Fprintf(&b, "%s\n", truncateRunes(p.Body, 1600))
	}
	b.WriteString("\nApply this polish on the current branch; commit when done.\n")
	return b.String(), nil
}

func (s *Store) projectRelForBind(engID string) string {
	eng, err := s.GetEngReq(engID)
	if err != nil {
		return ""
	}
	if eng.ProjectKey != "" {
		return eng.ProjectKey
	}
	return eng.WorkerMeta.ProjectRel
}
