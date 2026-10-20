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
	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

// Workbench is the console's interactive surface: a person types a request, an
// agent runs it on a worker, and the console streams what happens.
//
// Deliberately built on the scheduler job path rather than a new protocol. A
// phase Job started from the Roadmap is the same thing seen from the other end,
// so the two share one event renderer on the client and one stream server-side.
//
// What this does NOT do, because the job path does not carry it: approvals. The
// daemon's JobEventBody has no approval event and no channel to answer one, so a
// gated tool call inside a Worker job is decided by the worker's own options
// (default: allow) rather than by a person in this console. The client states
// that limit instead of showing an approval card that could never be delivered.

// workbenchSession is one interactive session, tracked in memory for the life of
// the process. Persistence is the worker's session store (the agent id in the
// terminal event is enough to resume), not a table here.
type workbenchSession struct {
	ID       string
	JobID    string
	WorkerID string
	Started  time.Time
	Prompt   string

	mu      sync.Mutex
	events  []daemon.JobEventBody
	done    *daemon.JobDoneBody
	errText string
	subs    map[chan wbFrame]struct{}
}

type wbFrame struct {
	Event string
	Data  any
}

const (
	// sessionEventRing bounds the in-memory replay for a live subscriber. It is
	// a ring, not a log: p_0.46.10 adds the persisted event table, and until then
	// a session's transcript exists only while it is running or recent.
	sessionEventRing = 500
	// sessionIdleRetention is how long a finished session stays readable in
	// memory before it is dropped. Long enough to read the result, short enough
	// that a long-lived process does not accumulate them.
	sessionIdleRetention = 30 * time.Minute
)

func (s *workbenchSession) publish(ev daemon.JobEventBody) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	if len(s.events) > sessionEventRing {
		s.events = s.events[len(s.events)-sessionEventRing:]
	}
	subs := s.subsLocked()
	s.mu.Unlock()
	frame := wbFrame{Event: "job.event", Data: ev}
	for _, ch := range subs {
		select {
		case ch <- frame:
		default: // a slow subscriber must not stall the stream
		}
	}
}

func (s *workbenchSession) finish(done daemon.JobDoneBody, errText string) {
	s.mu.Lock()
	s.done = &done
	if errText != "" {
		s.errText = errText
	}
	subs := s.subsLocked()
	s.mu.Unlock()
	frame := wbFrame{Event: "job.done", Data: sessionDoneBody(s, done, errText)}
	for _, ch := range subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (s *workbenchSession) subsLocked() []chan wbFrame {
	out := make([]chan wbFrame, 0, len(s.subs))
	for ch := range s.subs {
		out = append(out, ch)
	}
	return out
}

// snapshot is what a client gets on join: every event so far plus the terminal
// state if the session is over. Replay-then-live is what makes a reload land on
// the same conversation.
func (s *workbenchSession) snapshot() ([]daemon.JobEventBody, *daemon.JobDoneBody, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evs := append([]daemon.JobEventBody(nil), s.events...)
	return evs, s.done, s.errText
}

type workbenchHub struct {
	mu       sync.Mutex
	sessions map[string]*workbenchSession
}

func newWorkbenchHub() *workbenchHub {
	return &workbenchHub{sessions: map[string]*workbenchSession{}}
}

func (h *workbenchHub) add(s *workbenchSession) {
	h.mu.Lock()
	h.sessions[s.ID] = s
	h.mu.Unlock()
}

func (h *workbenchHub) get(id string) (*workbenchSession, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[id]
	return s, ok
}

func (h *workbenchHub) drop(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
}

// byJob finds a session by its job id.
//
// A session id and a job id are different keys: the session table is keyed by
// `wb-…` and the stream by `wbjob-…`. The cancel route only knows the job, so it
// needs this lookup rather than a map hit that would silently miss.
func (h *workbenchHub) byJob(jobID string) (*workbenchSession, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		if s.JobID == jobID {
			return s, true
		}
	}
	return nil, false
}

// liveForWorker reports whether a worker already has a running session, so the
// UI can refuse a second one instead of starting two agents on one checkout.
func (h *workbenchHub) liveForWorker(workerID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		s.mu.Lock()
		running := s.done == nil
		s.mu.Unlock()
		if running && s.WorkerID == workerID {
			return s.ID, true
		}
	}
	return "", false
}

// collectMature drops finished sessions older than the retention bound.
func (h *workbenchHub) collectMature(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, s := range h.sessions {
		s.mu.Lock()
		done := s.done != nil
		started := s.Started
		s.mu.Unlock()
		if done && now.Sub(started) > sessionIdleRetention {
			delete(h.sessions, id)
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// WorkbenchStartBody is POST /api/v1/workbench/sessions.
type WorkbenchStartBody struct {
	WorkerID string `json:"worker_id"`
	RepoID   string `json:"repo_id,omitempty"`
	NodeID   string `json:"node_id,omitempty"`
	Prompt   string `json:"prompt"`
	ResumeID string `json:"resume_id,omitempty"`
	Model    string `json:"model,omitempty"`
	MaxSteps int    `json:"max_steps,omitempty"`
}

// WorkbenchSession is what the client renders.
type WorkbenchSession struct {
	ID        string `json:"id"`
	JobID     string `json:"job_id"`
	WorkerID  string `json:"worker_id"`
	Prompt    string `json:"prompt"`
	StartedAt string `json:"started_at"`
	// State is running | finished | error | cancelled.
	State string `json:"state"`
	// Approval is false because the job path carries no approval channel; the
	// client says so rather than implying an approval it cannot deliver.
	Approval bool `json:"approval_supported"`
}

func sessionState(s *workbenchSession) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done == nil {
		return "running"
	}
	if s.errText != "" {
		return "error"
	}
	switch s.done.Status {
	case "finished", "error", "cancelled":
		return s.done.Status
	default:
		return s.done.Status
	}
}

func sessionView(s *workbenchSession) WorkbenchSession {
	return WorkbenchSession{
		ID:        s.ID,
		JobID:     s.JobID,
		WorkerID:  s.WorkerID,
		Prompt:    s.Prompt,
		StartedAt: s.Started.UTC().Format(time.RFC3339),
		State:     sessionState(s),
	}
}

// sessionDoneBody is the terminal frame: the SDK's done body plus the agent id the
// next resume needs.
type wbDoneBody struct {
	JobID   string `json:"job_id"`
	AgentID string `json:"agent_id,omitempty"`
	Status  string `json:"status"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
	Steps   int    `json:"steps,omitempty"`
}

func sessionDoneBody(s *workbenchSession, done daemon.JobDoneBody, errText string) wbDoneBody {
	out := wbDoneBody{
		JobID:   done.JobID,
		AgentID: done.AgentID,
		Status:  done.Status,
		Result:  done.Result,
		Error:   done.Error,
		Code:    done.Code,
		Steps:   done.Steps,
	}
	if out.Status == "" {
		out.Status = "finished"
	}
	if out.Error == "" {
		out.Error = errText
	}
	if s != nil && out.JobID == "" {
		out.JobID = s.JobID
	}
	return out
}

func (s *Server) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/workbench"), "/")

	// GET /sessions — the list, newest first.
	if path == "sessions" && r.Method == http.MethodGet {
		if s.workbench == nil {
			writeJSON(w, http.StatusOK, map[string]any{"sessions": []WorkbenchSession{}})
			return
		}
		s.workbench.collectMature(time.Now())
		s.workbench.mu.Lock()
		out := make([]WorkbenchSession, 0, len(s.workbench.sessions))
		for _, sess := range s.workbench.sessions {
			out = append(out, sessionView(sess))
		}
		s.workbench.mu.Unlock()
		// Newest first: an operator opening the list wants the one they just ran.
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j].StartedAt > out[j-1].StartedAt; j-- {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
		return
	}

	// POST /sessions — start one.
	if path == "sessions" && r.Method == http.MethodPost {
		s.handleWorkbenchStart(w, r)
		return
	}

	// /sessions/{id}             — one session's state
	// /sessions/{id}/events      — SSE: replay then live
	// /sessions/{id}/cancel      — stop
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && parts[0] == "sessions" {
		id := parts[1]
		sess, ok := s.workbench.get(id)
		if !ok {
			http.Error(w, "unknown session: "+id, http.StatusNotFound)
			return
		}
		if len(parts) == 2 && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, sessionView(sess))
			return
		}
		if len(parts) == 3 && parts[2] == "events" && r.Method == http.MethodGet {
			s.handleWorkbenchEvents(w, r, sess)
			return
		}
		if len(parts) == 3 && parts[2] == "cancel" && r.Method == http.MethodPost {
			s.handleWorkbenchCancel(w, r, sess)
			return
		}
	}

	http.NotFound(w, r)
}

func (s *Server) handleWorkbenchStart(w http.ResponseWriter, r *http.Request) {
	var in WorkbenchStartBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	workerID := strings.TrimSpace(in.WorkerID)
	if workerID == "" {
		http.Error(w, "worker_id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Prompt) == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	// One agent per worker checkout. Two concurrent writers on one working tree is
	// the same class of problem as two writers on one transcript.
	if live, ok := s.workbench.liveForWorker(workerID); ok {
		http.Error(w, "worker already has a running session: "+live, http.StatusConflict)
		return
	}

	// Resolve the worker to a repo/node through the cluster inventory the
	// delivery picker already uses.
	target, err := s.resolveWorkerTarget(workerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	repoID := strings.TrimSpace(in.RepoID)
	if repoID == "" {
		repoID = target.RepoID
	}
	nodeID := strings.TrimSpace(in.NodeID)
	if nodeID == "" {
		nodeID = target.NodeID
	}

	sess := &workbenchSession{
		ID:       "wb-" + id.New(),
		JobID:    "wbjob-" + id.New(),
		WorkerID: workerID,
		Started:  time.Now(),
		Prompt:   in.Prompt,
		subs:     map[chan wbFrame]struct{}{},
	}
	s.workbench.add(sess)
	s.log.Printf("workbench.session id=%s worker=%s job=%s", sess.ID, workerID, sess.JobID)

	// Persist the job header up front, so its events have a row to belong to and
	// the history list knows about a session that is still running (p_0.46.10).
	if s.delivery != nil {
		_ = s.delivery.BeginJob(delivery.JobRecord{
			JobID:    sess.JobID,
			Kind:     "workbench",
			WorkerID: workerID,
			Status:   "running",
		})
	}

	go s.runWorkbenchSession(sess, schedclient.JobCreate{
		JobID:    sess.JobID,
		RepoID:   repoID,
		Prompt:   in.Prompt,
		ResumeID: in.ResumeID,
		Model:    in.Model,
		MaxSteps: in.MaxSteps,
		NodeID:   nodeID,
		Stream:   true,
	})

	writeJSON(w, http.StatusOK, sessionView(sess))
}

// runWorkbenchSession drives the job and fans its events into the session.
func (s *Server) runWorkbenchSession(sess *workbenchSession, in schedclient.JobCreate) {
	defer func() {
		if rec := recover(); rec != nil {
			sess.finish(daemon.JobDoneBody{JobID: sess.JobID, Status: "error", Error: fmt.Sprint(rec)}, fmt.Sprint(rec))
		}
	}()

	if s.jobs == nil {
		// No fleet configured: say so instead of hanging on a stream that will
		// never open.
		msg := "no scheduler fleet configured; Workbench needs one reachable fleet"
		sess.publish(daemon.JobEventBody{JobID: sess.JobID, Type: "error", Text: msg, IsError: true})
		sess.finish(daemon.JobDoneBody{JobID: sess.JobID, Status: "error", Error: msg}, msg)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.wbCancelMu.Lock()
	if s.wbCancel == nil {
		s.wbCancel = map[string]context.CancelFunc{}
	}
	s.wbCancel[sess.ID] = cancel
	s.wbCancelMu.Unlock()
	defer func() {
		s.wbCancelMu.Lock()
		delete(s.wbCancel, sess.ID)
		s.wbCancelMu.Unlock()
		cancel()
	}()

	done, err := s.jobs.CreateJobStream(ctx, in, func(ev daemon.JobEventBody) {
		if ev.JobID == "" {
			ev.JobID = sess.JobID
		}
		sess.publish(ev)
		// Persist alongside the in-memory ring rather than instead of it: the ring
		// is the fast path for a live subscriber, the table is what makes the run
		// readable after the process that ran it is gone.
		if s.delivery != nil {
			_ = s.delivery.AppendJobEvent(delivery.JobEvent{
				JobID:   ev.JobID,
				Type:    ev.Type,
				Text:    ev.Text,
				Name:    ev.Name,
				Args:    string(ev.Args),
				Content: ev.Content,
				IsError: ev.IsError,
				Step:    ev.Step,
			})
		}
	})
	errText := ""
	if err != nil {
		errText = err.Error()
		if ctx.Err() != nil {
			done = daemon.JobDoneBody{JobID: sess.JobID, Status: "cancelled"}
		} else {
			done = daemon.JobDoneBody{JobID: sess.JobID, Status: "error", Error: errText}
		}
	}
	sess.finish(done, errText)
	if s.delivery != nil {
		status := done.Status
		if status == "" {
			status = "finished"
		}
		_ = s.delivery.FinishJob(sess.JobID, status, done.Error)
	}
}

func (s *Server) handleWorkbenchEvents(w http.ResponseWriter, r *http.Request, sess *workbenchSession) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan wbFrame, 64)
	sess.mu.Lock()
	if sess.subs == nil {
		sess.subs = map[chan wbFrame]struct{}{}
	}
	sess.subs[ch] = struct{}{}
	sess.mu.Unlock()
	defer func() {
		sess.mu.Lock()
		delete(sess.subs, ch)
		sess.mu.Unlock()
	}()

	writeFrame := func(f wbFrame) bool {
		data, err := json.Marshal(f.Data)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", f.Event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Replay first, so a reload lands on the same conversation instead of a
	// stream that starts mid-sentence. A reload attaches to the same session id.
	events, done, errText := sess.snapshot()
	for _, ev := range events {
		if !writeFrame(wbFrame{Event: "job.event", Data: ev}) {
			return
		}
	}
	if done != nil {
		writeFrame(wbFrame{Event: "job.done", Data: sessionDoneBody(sess, *done, errText)})
		return
	}

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f := <-ch:
			if !writeFrame(f) {
				return
			}
			if f.Event == "job.done" {
				return
			}
		case <-ticker.C:
			// A comment frame keeps intermediaries from closing an idle stream.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleWorkbenchCancel(w http.ResponseWriter, r *http.Request, sess *workbenchSession) {
	if _, done, _ := sess.snapshot(); done != nil {
		http.Error(w, "session already finished", http.StatusConflict)
		return
	}
	// One implementation: the job-level cancel decides how to stop a job, and this
	// route is the session-shaped view of the same act. Two would drift, and the
	// operator cannot tell which one is authoritative.
	_ = s.jobsCancel(sess.JobID)
	s.cancelWorkbenchSession(sess)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session_id": sess.ID})
}

// jobsCancel is a seam so tests can observe the scheduler call.
func (s *Server) jobsCancel(jobID string) error {
	if s.jobCanceler != nil {
		return s.jobCanceler(jobID)
	}
	return nil
}

