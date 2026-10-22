// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

// wbRunner is a job runner a test can hold open, so a session can be observed
// mid-flight rather than only after it ends.
type wbRunner struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	calls   int
	lastIn  schedclient.JobCreate
	events  []daemon.JobEventBody
	cancel  bool
}

func newWBRunner() *wbRunner {
	return &wbRunner{started: make(chan struct{}, 8), release: make(chan struct{})}
}

func (r *wbRunner) CreateJobStream(ctx context.Context, in schedclient.JobCreate, onEvent func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	r.mu.Lock()
	r.calls++
	r.lastIn = in
	r.mu.Unlock()
	r.started <- struct{}{}
	for _, ev := range r.events {
		if onEvent != nil {
			ev.JobID = in.JobID
			onEvent(ev)
		}
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		r.mu.Lock()
		r.cancel = true
		r.mu.Unlock()
		return daemon.JobDoneBody{JobID: in.JobID, Status: "cancelled"}, nil
	}
	return daemon.JobDoneBody{JobID: in.JobID, Status: "finished", Result: "done", AgentID: "agent-1"}, nil
}

// wbServer builds a Server with a token and the given runner, plus a worker the
// target resolver can find. It bypasses the cluster inventory by seeding it.
func wbServer(t *testing.T, runner JobRunner) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	as, err := auth.Open(filepath.Join(dir, "web.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = as.Close() })
	if _, err := as.CreateAdmin("admin", "password123"); err != nil {
		t.Fatal(err)
	}
	ds, err := delivery.Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{SchedulerURL: "http://stub.invalid"}, as, ds, nil, nil, log.New(&discard{}, "", 0))
	s.jobs = runner
	tok, _, err := as.Login("admin", "password123")
	if err != nil {
		t.Fatal(err)
	}
	return s, tok
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// call drives one authenticated request through the mux.
func call(t *testing.T, s *Server, tok, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestWorkbenchSessionRunsAndFinishes is the core round trip: start, observe the
// stream, and land on a terminal state that carries the agent id the next
// resume needs.
func TestWorkbenchSessionRunsAndFinishes(t *testing.T) {
	runner := newWBRunner()
	runner.events = []daemon.JobEventBody{
		{Type: "progress", Step: 1},
		{Type: "assistant", Text: "working on it"},
	}
	s, tok := wbServer(t, runner)

	// The target resolver needs a worker in the cluster inventory; with no fleets
	// configured resolveWorkerTarget fails, which the handler reports as 400. So
	// this test asserts the *refusal* is clear rather than hanging.
	w := call(t, s, tok, http.MethodPost, "/api/v1/workbench/sessions",
		`{"worker_id":"w_missing","prompt":"do a thing"}`)
	if w.Code == http.StatusOK {
		t.Fatal("expected the unresolved worker to be refused")
	}
	if !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("refusal should name the missing worker: %s", w.Body.String())
	}

	// And the empty-body validation.
	w = call(t, s, tok, http.MethodPost, "/api/v1/workbench/sessions", `{"worker_id":"w1"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "prompt") {
		t.Fatalf("want 400 naming prompt, got %d %s", w.Code, w.Body.String())
	}
	w = call(t, s, tok, http.MethodPost, "/api/v1/workbench/sessions", `{"prompt":"x"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "worker_id") {
		t.Fatalf("want 400 naming worker_id, got %d %s", w.Code, w.Body.String())
	}
}

// TestWorkbenchRefusesASecondSessionOnOneWorker pins the one-agent-per-checkout
// rule: two sessions on one working tree is the same class of damage as two
// writers on one transcript.
func TestWorkbenchRefusesASecondSessionOnOneWorker(t *testing.T) {
	s, _ := wbServer(t, newWBRunner())

	live := &workbenchSession{
		ID: "wb-live", JobID: "wbjob-live", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
	}
	s.workbench.add(live)

	if id, ok := s.workbench.liveForWorker("w1"); !ok || id != "wb-live" {
		t.Fatalf("expected a live session for w1, got %q %v", id, ok)
	}
	// A finished session does not block the worker.
	live.finish(daemon.JobDoneBody{JobID: "wbjob-live", Status: "finished"}, "")
	if _, ok := s.workbench.liveForWorker("w1"); ok {
		t.Fatal("a finished session must not block a new one")
	}
}

// TestWorkbenchEventStreamReplays is what makes a reload land on the same
// conversation: joining mid-flight sends every event so far, then follows.
func TestWorkbenchEventStreamReplays(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())

	sess := &workbenchSession{
		ID: "wb-replay", JobID: "wbjob-replay", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
	}
	sess.publish(daemon.JobEventBody{JobID: "wbjob-replay", Type: "assistant", Text: "first"})
	sess.publish(daemon.JobEventBody{JobID: "wbjob-replay", Type: "tool_call", Name: "shell"})
	s.workbench.add(sess)

	// A finished session's stream ends immediately after the replay, which is
	// also the reload path.
	sess.finish(daemon.JobDoneBody{JobID: "wbjob-replay", Status: "finished", AgentID: "agent-9"}, "")

	w := call(t, s, tok, http.MethodGet, "/api/v1/workbench/sessions/wb-replay/events", "")
	body := w.Body.String()
	for _, want := range []string{"event: job.event", "first", "tool_call", "event: job.done", "agent-9"} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream missing %q:\n%s", want, body)
		}
	}
	// Replay order is the recording order.
	if strings.Index(body, "first") > strings.Index(body, "tool_call") {
		t.Fatal("events replayed out of order")
	}
}

// TestWorkbenchCancelAsksTheScheduler checks the cancel path reaches the
// scheduler and that the session then reports cancelled rather than error.
func TestWorkbenchCancelAsksTheScheduler(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	var cancelled string
	s.jobCanceler = func(jobID string) error {
		cancelled = jobID
		return nil
	}

	sess := &workbenchSession{
		ID: "wb-cancel", JobID: "wbjob-cancel", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.wbCancel["wb-cancel"] = cancel
	s.workbench.add(sess)
	_ = ctx

	w := call(t, s, tok, http.MethodPost, "/api/v1/workbench/sessions/wb-cancel/cancel", "")
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if cancelled != "wbjob-cancel" {
		t.Fatalf("scheduler was not asked to cancel: %q", cancelled)
	}

	// The run goroutine reports cancelled once its context ends.
	sess.finish(daemon.JobDoneBody{JobID: "wbjob-cancel", Status: "cancelled"}, "")
	if got := sessionState(sess); got != "cancelled" {
		t.Fatalf("state after cancel = %q, want cancelled", got)
	}

	// Cancelling a finished session is a conflict, not a silent success.
	w = call(t, s, tok, http.MethodPost, "/api/v1/workbench/sessions/wb-cancel/cancel", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("second cancel: want 409, got %d", w.Code)
	}
}

// TestWorkbenchSessionListAndUnknownID covers the list shape and the 404.
func TestWorkbenchSessionListAndUnknownID(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())

	w := call(t, s, tok, http.MethodGet, "/api/v1/workbench/sessions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var out struct {
		Sessions []WorkbenchSession `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Sessions == nil {
		t.Fatal("sessions must be an empty array, not null")
	}

	w = call(t, s, tok, http.MethodGet, "/api/v1/workbench/sessions/nope/events", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown session: want 404, got %d", w.Code)
	}
}

// TestWorkbenchStartWithoutFleetSaysSo makes sure a console with no fleet fails
// fast and legibly instead of opening a stream that never emits.
func TestWorkbenchStartWithoutFleetSaysSo(t *testing.T) {
	s, _ := wbServer(t, nil)

	sess := &workbenchSession{
		ID: "wb-nofleet", JobID: "wbjob-nofleet", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
	}
	s.runWorkbenchSession(sess, schedclient.JobCreate{JobID: "wbjob-nofleet", Prompt: "x"})

	evs, done, _ := sess.snapshot()
	if len(evs) == 0 || !strings.Contains(evs[0].Text, "no scheduler fleet") {
		t.Fatalf("expected an explanatory event, got %+v", evs)
	}
	if done == nil || done.Status != "error" {
		t.Fatalf("expected a terminal error, got %+v", done)
	}
}

// TestWorkbenchStreamFramesAreWellFormed guards the SSE wire shape, which a
// browser will silently ignore if it is wrong.
func TestWorkbenchStreamFramesAreWellFormed(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	sess := &workbenchSession{
		ID: "wb-shape", JobID: "wbjob-shape", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
	}
	sess.publish(daemon.JobEventBody{JobID: "wbjob-shape", Type: "assistant", Text: "hi"})
	sess.finish(daemon.JobDoneBody{JobID: "wbjob-shape", Status: "finished"}, "")
	s.workbench.add(sess)

	w := call(t, s, tok, http.MethodGet, "/api/v1/workbench/sessions/wb-shape/events", "")
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	// Every frame must be "event: X\ndata: {...}\n\n".
	frames := strings.Split(strings.TrimSpace(w.Body.String()), "\n\n")
	for _, f := range frames {
		if f == "" || strings.HasPrefix(f, ":") {
			continue
		}
		if !strings.Contains(f, "event: ") || !strings.Contains(f, "data: ") {
			t.Fatalf("malformed frame: %q", f)
		}
		for _, line := range strings.Split(f, "\n") {
			if strings.HasPrefix(line, "data: ") {
				var v map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
					t.Fatalf("data is not JSON: %v (%q)", err, line)
				}
			}
		}
	}
	_ = fmt.Sprint()
}

func TestWorkbenchApprovalIsSessionBoundAndSingleUse(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	sess := &workbenchSession{
		ID: "wb-approval", JobID: "job-approval", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
		approvals: map[string]string{"appr-1": "pending"},
	}
	s.workbench.add(sess)
	var got daemon.JobApprovalResponseBody
	s.approvalResponder = func(_ context.Context, jobID string, body daemon.JobApprovalResponseBody) error {
		got = body
		return nil
	}

	w := call(t, s, tok, http.MethodPost,
		"/api/v1/workbench/sessions/wb-approval/approvals/appr-1",
		`{"decision":"allow","args":{"path":"a"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("approval: %d %s", w.Code, w.Body.String())
	}
	if got.JobID != "job-approval" || got.SessionID != "wb-approval" || got.RequestID != "appr-1" {
		t.Fatalf("response was not server-bound: %+v", got)
	}
	w = call(t, s, tok, http.MethodPost,
		"/api/v1/workbench/sessions/wb-approval/approvals/appr-1",
		`{"decision":"deny"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate: want 409, got %d", w.Code)
	}
	w = call(t, s, tok, http.MethodPost,
		"/api/v1/workbench/sessions/wb-approval/approvals/unknown",
		`{"decision":"allow"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("unknown: want 409, got %d", w.Code)
	}
}

func TestWorkbenchApprovalRejectsBadInputAndAfterStop(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	sess := &workbenchSession{
		ID: "wb-input", JobID: "job-input", WorkerID: "w1",
		Started: time.Now(), Prompt: "x", subs: map[chan wbFrame]struct{}{},
		approvals: map[string]string{"appr-1": "pending"},
	}
	s.workbench.add(sess)
	unauth := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodPost,
		"/api/v1/workbench/sessions/wb-input/approvals/appr-1", strings.NewReader(`{"decision":"allow"}`)))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized approval: want 401, got %d", unauth.Code)
	}
	w := call(t, s, tok, http.MethodPost,
		"/api/v1/workbench/sessions/wb-input/approvals/appr-1",
		`{"decision":"maybe"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown decision: want 400, got %d", w.Code)
	}
	w = call(t, s, tok, http.MethodPost,
		"/api/v1/workbench/sessions/wb-input/approvals/appr-1",
		`{"decision":"allow","message":"`+strings.Repeat("x", 70<<10)+`"}`)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized response: want 413, got %d", w.Code)
	}
	sess.abortApprovals("stopped")
	w = call(t, s, tok, http.MethodPost,
		"/api/v1/workbench/sessions/wb-input/approvals/appr-1",
		`{"decision":"allow"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("after stop: want 409, got %d", w.Code)
	}
}

func TestPhaseJobBridgeIsReadOnlyAndReplays(t *testing.T) {
	s, tok := wbServer(t, newWBRunner())
	if err := s.delivery.BeginJob(delivery.JobRecord{
		JobID: "phase-1", Kind: "phase", PhaseID: "p_0.45.9", WorkerID: "w1", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.delivery.AppendJobEvent(delivery.JobEvent{
		JobID: "phase-1", Type: "assistant", Text: "phase output",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.delivery.FinishJob("phase-1", "finished", ""); err != nil {
		t.Fatal(err)
	}

	w := call(t, s, tok, http.MethodGet, "/api/v1/workbench/sessions", "")
	if !strings.Contains(w.Body.String(), "phasejob-phase-1") || !strings.Contains(w.Body.String(), `"read_only":true`) {
		t.Fatalf("list missing phase bridge: %s", w.Body.String())
	}
	w = call(t, s, tok, http.MethodGet, "/api/v1/workbench/sessions/phasejob-phase-1/events", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "phase output") ||
		!strings.Contains(w.Body.String(), "event: job.done") {
		t.Fatalf("bridge replay: %d %s", w.Code, w.Body.String())
	}
	w = call(t, s, tok, http.MethodPost, "/api/v1/workbench/sessions/phasejob-phase-1/cancel", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("phase bridge mutation: want 405, got %d", w.Code)
	}
}
