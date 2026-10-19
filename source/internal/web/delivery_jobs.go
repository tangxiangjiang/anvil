// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

// JobRunner creates scheduler jobs for Delivery phases (injectable for tests).
type JobRunner interface {
	CreateJobStream(ctx context.Context, in schedclient.JobCreate, onEvent func(daemon.JobEventBody)) (daemon.JobDoneBody, error)
}

type schedJobRunner struct{ c *schedclient.Client }

func (r schedJobRunner) CreateJobStream(ctx context.Context, in schedclient.JobCreate, onEvent func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	return r.c.CreateJobStream(ctx, in, onEvent)
}

// jobHub fans SSE frames for in-flight delivery jobs.
type jobHub struct {
	mu   sync.Mutex
	jobs map[string]*jobFan
}

type jobFan struct {
	mu     sync.Mutex
	events []daemon.JobEventBody
	done   *daemon.JobDoneBody
	err    string
	subs   map[chan jobFrame]struct{}
}

type jobFrame struct {
	Event string
	Data  any
}

func newJobHub() *jobHub {
	return &jobHub{jobs: map[string]*jobFan{}}
}

func (h *jobHub) ensure(jobID string) *jobFan {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.jobs[jobID]
	if f == nil {
		f = &jobFan{subs: map[chan jobFrame]struct{}{}}
		h.jobs[jobID] = f
	}
	return f
}

func (h *jobHub) publishEvent(jobID string, ev daemon.JobEventBody) {
	f := h.ensure(jobID)
	f.mu.Lock()
	f.events = append(f.events, ev)
	if len(f.events) > 200 {
		f.events = f.events[len(f.events)-200:]
	}
	subs := make([]chan jobFrame, 0, len(f.subs))
	for ch := range f.subs {
		subs = append(subs, ch)
	}
	f.mu.Unlock()
	frame := jobFrame{Event: "job.event", Data: ev}
	for _, ch := range subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (h *jobHub) publishDone(jobID string, done daemon.JobDoneBody, errMsg string) {
	f := h.ensure(jobID)
	f.mu.Lock()
	f.done = &done
	f.err = errMsg
	subs := make([]chan jobFrame, 0, len(f.subs))
	for ch := range f.subs {
		subs = append(subs, ch)
	}
	f.mu.Unlock()
	var frame jobFrame
	if errMsg != "" {
		frame = jobFrame{Event: "error", Data: map[string]string{"error": errMsg}}
	} else {
		frame = jobFrame{Event: "job.done", Data: done}
	}
	for _, ch := range subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (h *jobHub) subscribe(jobID string) (<-chan jobFrame, func(), *jobFan) {
	f := h.ensure(jobID)
	ch := make(chan jobFrame, 64)
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	// Replay buffered events.
	for _, ev := range f.events {
		ch <- jobFrame{Event: "job.event", Data: ev}
	}
	if f.done != nil {
		ch <- jobFrame{Event: "job.done", Data: *f.done}
	} else if f.err != "" {
		ch <- jobFrame{Event: "error", Data: map[string]string{"error": f.err}}
	}
	f.mu.Unlock()
	unsub := func() {
		f.mu.Lock()
		delete(f.subs, ch)
		f.mu.Unlock()
		close(ch)
	}
	return ch, unsub, f
}

func (s *Server) jobRunner() JobRunner {
	if s.jobs != nil {
		return s.jobs
	}
	return schedJobRunner{c: s.sched}
}

// WorkerTarget is node/repo resolved from inventory for a worker id.
type WorkerTarget struct {
	NodeID string
	RepoID string
	Fleet  string
}

func (s *Server) resolveWorkerTarget(workerID string) (WorkerTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return WorkerTarget{}, fmt.Errorf("worker id required")
	}
	tok := s.cfg.SchedulerToken()
	for _, f := range s.cfg.Fleets {
		cli := &schedclient.Client{BaseURL: f.SchedulerURL, Token: tok, HTTP: &http.Client{Timeout: 12 * time.Second}}
		raw, err := cli.ListNodes(context.Background())
		if err != nil {
			continue
		}
		type nodeRow struct {
			ID      string `json:"id"`
			Workers []struct {
				ID         string `json:"id"`
				RepoID     string `json:"repo_id"`
				ProjectRel string `json:"project_rel"`
			} `json:"workers"`
		}
		var wrap struct {
			Nodes []nodeRow `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			continue
		}
		for _, n := range wrap.Nodes {
			for _, w := range n.Workers {
				if w.ID != workerID {
					continue
				}
				repo := strings.TrimSpace(w.RepoID)
				if repo == "" {
					repo = strings.TrimSpace(w.ProjectRel)
				}
				if repo == "" {
					repo = workerID
				}
				return WorkerTarget{NodeID: n.ID, RepoID: repo, Fleet: f.ID}, nil
			}
		}
	}
	return WorkerTarget{}, fmt.Errorf("worker %q not found in cluster inventory", workerID)
}

func (s *Server) ensureJobHub() *jobHub {
	if s.jobHub == nil {
		s.jobHub = newJobHub()
	}
	return s.jobHub
}

// startMilestonePhaseJob enqueues the next runnable phase job asynchronously.
func (s *Server) startMilestonePhaseJob(msID string, forceStub bool) (delivery.Phase, error) {
	if s.delivery == nil {
		return delivery.Phase{}, fmt.Errorf("delivery store not configured")
	}
	ms, err := s.delivery.GetMilestone(msID)
	if err != nil {
		return delivery.Phase{}, err
	}
	workerID := strings.TrimSpace(ms.ExecuteWorkerID)
	if workerID == "" {
		return delivery.Phase{}, fmt.Errorf("execute_worker_id required")
	}
	ph, err := s.delivery.NextRunnablePhase(msID)
	if err != nil {
		return delivery.Phase{}, err
	}
	if ph.JobStatus == delivery.JobRunning || ph.JobStatus == delivery.JobQueued {
		return ph, nil
	}
	target, resolveErr := s.resolveWorkerTarget(workerID)
	localStub := false
	if resolveErr != nil {
		if s.jobs == nil && !forceStub {
			return delivery.Phase{}, resolveErr
		}
		// Injected runner (tests) or force_stub without inventory.
		localStub = s.jobs == nil && forceStub
		target = WorkerTarget{RepoID: workerID, NodeID: ""}
	}
	prompt, err := s.delivery.BuildPhasePrompt(msID, ph.ID)
	if err != nil {
		return delivery.Phase{}, err
	}
	jobID := fmt.Sprintf("dj_%d", time.Now().UnixNano())
	bind := parseMsBind(msID)
	if err := s.delivery.SetPhaseJob(ph.ID, jobID, delivery.JobQueued, "queued"); err != nil {
		return delivery.Phase{}, err
	}
	ph.JobID = jobID
	ph.JobStatus = delivery.JobQueued
	ph.JobLastEvent = "queued"

	in := schedclient.JobCreate{
		JobID:       jobID,
		RepoID:      target.RepoID,
		Prompt:      prompt,
		NodeID:      target.NodeID,
		ForceStub:   forceStub,
		MilestoneID: msID,
		PhaseID:     ph.ID,
	}
	if bind != nil {
		in.EngReqID = bind.engID
		in.Major = bind.major
		in.Minor = bind.minor
	}

	hub := s.ensureJobHub()
	hub.ensure(jobID)
	_ = s.delivery.AppendPhaseJobEvent(ph.ID, delivery.JobRunning, "starting")

	if localStub {
		go s.runPhaseJobLocalStub(msID, ph.ID, jobID, in.ForceStub)
	} else {
		go s.runPhaseJob(msID, ph.ID, jobID, in)
	}
	return ph, nil
}

func (s *Server) runPhaseJobLocalStub(msID, phaseID, jobID string, advance bool) {
	hub := s.ensureJobHub()
	ev := daemon.JobEventBody{JobID: jobID, Type: "progress", Text: "force_stub local"}
	hub.publishEvent(jobID, ev)
	_ = s.delivery.AppendPhaseJobEvent(phaseID, delivery.JobRunning, ev.Text)
	done := daemon.JobDoneBody{JobID: jobID, Status: "finished", Result: "force_stub ok"}
	hub.publishDone(jobID, done, "")
	_ = s.delivery.CompletePhaseJob(phaseID, delivery.JobFinished, "finished", done.Result)
	if !advance {
		return
	}
	ms, err := s.delivery.GetMilestone(msID)
	if err == nil && ms.RunState == delivery.RunRunning {
		_, _ = s.startMilestonePhaseJob(msID, true)
	}
}

type msBind struct {
	engID string
	major int
	minor int
}

func parseMsBind(msID string) *msBind {
	// Reuse store parser via public helper — duplicate thin parse here.
	id := strings.TrimSpace(msID)
	if !strings.HasPrefix(id, "ms_") {
		return nil
	}
	rest := id[3:]
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
	var major, minor int
	if _, err := fmt.Sscanf(majorStr+" "+minorStr, "%d %d", &major, &minor); err != nil {
		return nil
	}
	return &msBind{engID: engID, major: major, minor: minor}
}

func (s *Server) runPhaseJob(msID, phaseID, jobID string, in schedclient.JobCreate) {
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
		_ = s.delivery.AppendPhaseJobEvent(phaseID, delivery.JobRunning, snippet)
	})
	if err != nil {
		hub.publishDone(jobID, daemon.JobDoneBody{JobID: jobID, Status: "error", Error: err.Error()}, err.Error())
		_ = s.delivery.CompletePhaseJob(phaseID, delivery.JobError, "error", err.Error())
		rs := delivery.RunPaused
		_, _ = s.delivery.PatchMilestone(msID, delivery.PatchMilestoneInput{RunState: &rs})
		return
	}
	hub.publishDone(jobID, done, "")
	st := strings.ToLower(done.Status)
	switch st {
	case "finished":
		_ = s.delivery.CompletePhaseJob(phaseID, delivery.JobFinished, "finished", done.Result)
		// Auto-enqueue next while still running.
		ms, err := s.delivery.GetMilestone(msID)
		if err == nil && ms.RunState == delivery.RunRunning {
			if _, err := s.startMilestonePhaseJob(msID, in.ForceStub); err != nil {
				// No more phases or cannot start — leave running until Stop/SubmitTest.
				if !strings.Contains(err.Error(), "no runnable") {
					_ = s.delivery.AppendPhaseJobEvent(phaseID, delivery.JobFinished, err.Error())
				}
			}
		}
	case "cancelled":
		_ = s.delivery.CompletePhaseJob(phaseID, delivery.JobCancelled, "cancelled", done.Error)
		rs := delivery.RunStopped
		_, _ = s.delivery.PatchMilestone(msID, delivery.PatchMilestoneInput{RunState: &rs})
	default:
		msg := done.Error
		if msg == "" {
			msg = done.Status
		}
		_ = s.delivery.CompletePhaseJob(phaseID, delivery.JobError, "error", msg)
		rs := delivery.RunPaused
		_, _ = s.delivery.PatchMilestone(msID, delivery.PatchMilestoneInput{RunState: &rs})
	}
}

func (s *Server) softStopMilestoneJobs(msID string, runState string) error {
	ph, err := s.delivery.ActivePhaseJob(msID)
	if err == nil && ph.JobID != "" {
		_ = s.delivery.CompletePhaseJob(ph.ID, delivery.JobCancelled, ph.Status, "cancelled locally (soft pause/stop)")
		hub := s.ensureJobHub()
		hub.publishDone(ph.JobID, daemon.JobDoneBody{JobID: ph.JobID, Status: "cancelled"}, "")
	}
	rs := runState
	_, err = s.delivery.PatchMilestone(msID, delivery.PatchMilestoneInput{RunState: &rs})
	return err
}

func (s *Server) handleDeliveryJobEvents(w http.ResponseWriter, r *http.Request, jobID string) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		http.Error(w, "job id required", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub, _ := s.ensureJobHub().subscribe(jobID)
	defer unsub()

	writeFrame := func(event string, data any) error {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case frame, ok := <-ch:
			if !ok {
				return
			}
			if err := writeFrame(frame.Event, frame.Data); err != nil {
				return
			}
			if frame.Event == "job.done" || frame.Event == "error" {
				return
			}
		}
	}
}
