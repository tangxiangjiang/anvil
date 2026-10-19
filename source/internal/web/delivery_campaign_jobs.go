// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

type campaignStartResult struct {
	JobID    string `json:"job_id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	MsID     string `json:"milestone_id,omitempty"`
	BugID    string `json:"bug_id,omitempty"`
	PolishID string `json:"polish_id,omitempty"`
}

func (s *Server) resolveCampaignTarget(workerID string, forceStub bool) (WorkerTarget, bool, error) {
	target, resolveErr := s.resolveWorkerTarget(workerID)
	localStub := false
	if resolveErr != nil {
		allowLocal := forceStub || s.jobs != nil || len(s.cfg.Fleets) == 0
		if !allowLocal {
			return WorkerTarget{}, false, resolveErr
		}
		localStub = s.jobs == nil
		target = WorkerTarget{RepoID: workerID, NodeID: ""}
	}
	return target, localStub, nil
}

func (s *Server) startTestRunJob(engID string, major, minor int, forceStub bool) (campaignStartResult, error) {
	var out campaignStartResult
	if s.delivery == nil {
		return out, fmt.Errorf("delivery store not configured")
	}
	ms, err := s.delivery.AssertCampaignRunnable(engID, major, minor)
	if err != nil {
		return out, err
	}
	target, localStub, err := s.resolveCampaignTarget(ms.ExecuteWorkerID, forceStub)
	if err != nil {
		return out, err
	}
	prompt, err := s.delivery.BuildTestRunPrompt(engID, major, minor)
	if err != nil {
		return out, err
	}
	jobID := fmt.Sprintf("dj_run_%d", time.Now().UnixNano())
	if err := s.delivery.SetCampaignJob(ms.ID, jobID, delivery.JobQueued, delivery.CampaignKindRun, "queued"); err != nil {
		return out, err
	}
	in := schedclient.JobCreate{
		JobID:       jobID,
		RepoID:      target.RepoID,
		Prompt:      prompt,
		NodeID:      target.NodeID,
		ForceStub:   forceStub,
		MilestoneID: ms.ID,
		PhaseID:     "campaign:run",
		EngReqID:    engID,
		Major:       major,
		Minor:       minor,
	}
	hub := s.ensureJobHub()
	hub.ensure(jobID)
	_ = s.delivery.AppendCampaignJobEvent(ms.ID, delivery.JobRunning, "starting")
	if localStub {
		go s.runCampaignLocalStub(ms.ID, jobID, delivery.CampaignKindRun, engID, major, minor, "", true)
	} else {
		go s.runCampaignJob(ms.ID, jobID, delivery.CampaignKindRun, engID, major, minor, "", in)
	}
	out = campaignStartResult{JobID: jobID, Kind: delivery.CampaignKindRun, Status: delivery.JobQueued, MsID: ms.ID}
	return out, nil
}

func (s *Server) startBugFixJob(bugID string, forceStub bool) (campaignStartResult, error) {
	var out campaignStartResult
	if s.delivery == nil {
		return out, fmt.Errorf("delivery store not configured")
	}
	bug, err := s.delivery.GetBuglistItem(bugID)
	if err != nil {
		return out, err
	}
	if bug.Status != "open" && bug.Status != "fixing" {
		return out, fmt.Errorf("fix requires status open or fixing (got %s)", bug.Status)
	}
	if bug.JobStatus == delivery.JobRunning || bug.JobStatus == delivery.JobQueued {
		return out, fmt.Errorf("bug fix job already %s", bug.JobStatus)
	}
	ms, err := s.delivery.FindMilestoneByBind(bug.EngReqID, bug.Major, bug.Minor)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(ms.ExecuteWorkerID) == "" {
		return out, fmt.Errorf("execute_worker_id required to fix")
	}
	target, localStub, err := s.resolveCampaignTarget(ms.ExecuteWorkerID, forceStub)
	if err != nil {
		return out, err
	}
	prompt, err := s.delivery.BuildBugFixPrompt(bugID)
	if err != nil {
		return out, err
	}
	jobID := fmt.Sprintf("dj_fix_%d", time.Now().UnixNano())
	if err := s.delivery.SetBugJob(bugID, jobID, delivery.JobQueued, "queued"); err != nil {
		return out, err
	}
	in := schedclient.JobCreate{
		JobID:       jobID,
		RepoID:      target.RepoID,
		Prompt:      prompt,
		NodeID:      target.NodeID,
		ForceStub:   forceStub,
		MilestoneID: ms.ID,
		PhaseID:     "campaign:fix:" + bugID,
		EngReqID:    bug.EngReqID,
		Major:       bug.Major,
		Minor:       bug.Minor,
	}
	hub := s.ensureJobHub()
	hub.ensure(jobID)
	_ = s.delivery.AppendBugJobEvent(bugID, delivery.JobRunning, "starting")
	if localStub {
		go s.runCampaignLocalStub(ms.ID, jobID, "fix", bug.EngReqID, bug.Major, bug.Minor, bugID, true)
	} else {
		go s.runCampaignJob(ms.ID, jobID, "fix", bug.EngReqID, bug.Major, bug.Minor, bugID, in)
	}
	out = campaignStartResult{JobID: jobID, Kind: "fix", Status: delivery.JobQueued, MsID: ms.ID, BugID: bugID}
	return out, nil
}

func (s *Server) startRegressJob(engID string, major, minor int, forceStub bool) (campaignStartResult, error) {
	var out campaignStartResult
	if s.delivery == nil {
		return out, fmt.Errorf("delivery store not configured")
	}
	ms, err := s.delivery.AssertRegressRunnable(engID, major, minor)
	if err != nil {
		return out, err
	}
	target, localStub, err := s.resolveCampaignTarget(ms.ExecuteWorkerID, forceStub)
	if err != nil {
		return out, err
	}
	prompt, err := s.delivery.BuildRegressPrompt(engID, major, minor)
	if err != nil {
		return out, err
	}
	jobID := fmt.Sprintf("dj_reg_%d", time.Now().UnixNano())
	if err := s.delivery.SetCampaignJob(ms.ID, jobID, delivery.JobQueued, delivery.CampaignKindRegress, "queued"); err != nil {
		return out, err
	}
	in := schedclient.JobCreate{
		JobID:       jobID,
		RepoID:      target.RepoID,
		Prompt:      prompt,
		NodeID:      target.NodeID,
		ForceStub:   forceStub,
		MilestoneID: ms.ID,
		PhaseID:     "campaign:regress",
		EngReqID:    engID,
		Major:       major,
		Minor:       minor,
	}
	hub := s.ensureJobHub()
	hub.ensure(jobID)
	_ = s.delivery.AppendCampaignJobEvent(ms.ID, delivery.JobRunning, "starting")
	if localStub {
		go s.runCampaignLocalStub(ms.ID, jobID, delivery.CampaignKindRegress, engID, major, minor, "", true)
	} else {
		go s.runCampaignJob(ms.ID, jobID, delivery.CampaignKindRegress, engID, major, minor, "", in)
	}
	out = campaignStartResult{JobID: jobID, Kind: delivery.CampaignKindRegress, Status: delivery.JobQueued, MsID: ms.ID}
	return out, nil
}

func (s *Server) startPolishJob(polishID string, forceStub bool) (campaignStartResult, error) {
	var out campaignStartResult
	if s.delivery == nil {
		return out, fmt.Errorf("delivery store not configured")
	}
	p, err := s.delivery.GetPolish(polishID)
	if err != nil {
		return out, err
	}
	if p.Status == "done" {
		out = campaignStartResult{JobID: p.JobID, Kind: "polish", Status: "done", PolishID: polishID}
		return out, nil
	}
	if p.JobStatus == delivery.JobRunning || p.JobStatus == delivery.JobQueued {
		return out, fmt.Errorf("polish job already %s", p.JobStatus)
	}
	ms, err := s.delivery.FindMilestoneByBind(p.EngReqID, p.Major, p.Minor)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(ms.ExecuteWorkerID) == "" {
		return out, fmt.Errorf("execute_worker_id required to run polish")
	}
	target, localStub, err := s.resolveCampaignTarget(ms.ExecuteWorkerID, forceStub)
	if err != nil {
		return out, err
	}
	prompt, err := s.delivery.BuildPolishPrompt(polishID)
	if err != nil {
		return out, err
	}
	jobID := fmt.Sprintf("dj_pol_%d", time.Now().UnixNano())
	if err := s.delivery.SetPolishJob(polishID, jobID, delivery.JobQueued, "queued"); err != nil {
		return out, err
	}
	in := schedclient.JobCreate{
		JobID:       jobID,
		RepoID:      target.RepoID,
		Prompt:      prompt,
		NodeID:      target.NodeID,
		ForceStub:   forceStub,
		MilestoneID: ms.ID,
		PhaseID:     "polish:" + polishID,
		EngReqID:    p.EngReqID,
		Major:       p.Major,
		Minor:       p.Minor,
	}
	hub := s.ensureJobHub()
	hub.ensure(jobID)
	_ = s.delivery.AppendPolishJobEvent(polishID, delivery.JobRunning, "starting")
	if localStub {
		go s.runCampaignLocalStub(ms.ID, jobID, "polish", p.EngReqID, p.Major, p.Minor, polishID, true)
	} else {
		go s.runCampaignJob(ms.ID, jobID, "polish", p.EngReqID, p.Major, p.Minor, polishID, in)
	}
	out = campaignStartResult{JobID: jobID, Kind: "polish", Status: delivery.JobQueued, MsID: ms.ID, PolishID: polishID}
	return out, nil
}

func (s *Server) runCampaignLocalStub(msID, jobID, kind, engID string, major, minor int, refID string, applyMock bool) {
	hub := s.ensureJobHub()
	ev := daemon.JobEventBody{JobID: jobID, Type: "progress", Text: "force_stub local"}
	hub.publishEvent(jobID, ev)
	s.touchCampaignProgress(kind, msID, refID, delivery.JobRunning, ev.Text)
	done := daemon.JobDoneBody{JobID: jobID, Status: "finished", Result: "force_stub ok"}
	hub.publishDone(jobID, done, "")
	s.applyCampaignFinished(kind, msID, engID, major, minor, refID, done, applyMock)
}

func (s *Server) runCampaignJob(msID, jobID, kind, engID string, major, minor int, refID string, in schedclient.JobCreate) {
	hub := s.ensureJobHub()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	runner := s.jobRunner()
	done, err := runner.CreateJobStream(ctx, in, func(ev daemon.JobEventBody) {
		hub.publishEvent(jobID, ev)
		snippet := ev.Text
		if snippet == "" {
			snippet = ev.Content
		}
		if snippet == "" {
			snippet = ev.Type
		}
		s.touchCampaignProgress(kind, msID, refID, delivery.JobRunning, snippet)
	})
	if err != nil {
		hub.publishDone(jobID, daemon.JobDoneBody{JobID: jobID, Status: "error", Error: err.Error()}, err.Error())
		s.applyCampaignError(kind, msID, refID, err.Error())
		return
	}
	hub.publishDone(jobID, done, "")
	st := strings.ToLower(done.Status)
	if st == "finished" {
		s.applyCampaignFinished(kind, msID, engID, major, minor, refID, done, in.ForceStub)
		return
	}
	msg := done.Error
	if msg == "" {
		msg = done.Status
	}
	s.applyCampaignError(kind, msID, refID, msg)
}

func (s *Server) touchCampaignProgress(kind, msID, refID, status, event string) {
	switch kind {
	case delivery.CampaignKindRun, delivery.CampaignKindRegress:
		_ = s.delivery.AppendCampaignJobEvent(msID, status, event)
	case "fix":
		_ = s.delivery.AppendBugJobEvent(refID, status, event)
	case "polish":
		_ = s.delivery.AppendPolishJobEvent(refID, status, event)
	}
}

func (s *Server) applyCampaignFinished(kind, msID, engID string, major, minor int, refID string, done daemon.JobDoneBody, useMockRun bool) {
	switch kind {
	case delivery.CampaignKindRun:
		if useMockRun || s.jobs != nil {
			// Stub / injected runner: keep deterministic odd-index fail semantics.
			_, _ = s.delivery.RunTestCases(engID, major, minor)
		} else {
			// Real worker without structured parse: treat finished as all passed.
			_, _ = s.delivery.RunTestCasesAllPass(engID, major, minor)
		}
		_ = s.delivery.ClearCampaignJob(msID, delivery.JobFinished, truncateEvent(done.Result))
	case delivery.CampaignKindRegress:
		_, _ = s.delivery.RegressBuglist(engID, major, minor)
		_ = s.delivery.ClearCampaignJob(msID, delivery.JobFinished, "regress finished")
	case "fix":
		_ = s.delivery.CompleteBugFixJob(refID, delivery.JobFinished, "verified", done.Result)
	case "polish":
		_ = s.delivery.CompletePolishJob(refID, delivery.JobFinished, "done", done.Result)
	}
}

func (s *Server) applyCampaignError(kind, msID, refID, msg string) {
	switch kind {
	case delivery.CampaignKindRun, delivery.CampaignKindRegress:
		_ = s.delivery.ClearCampaignJob(msID, delivery.JobError, msg)
	case "fix":
		_ = s.delivery.CompleteBugFixJob(refID, delivery.JobError, msg, msg)
	case "polish":
		_ = s.delivery.CompletePolishJob(refID, delivery.JobError, msg, msg)
	}
}

func truncateEvent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 240 {
		return s
	}
	return s[:240]
}
