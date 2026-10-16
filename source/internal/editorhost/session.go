// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"sync"
	"time"

	"github.com/anvil-dev/anvil"
)

// closeRunTimeout bounds how long session.close waits for an in-flight run to
// drain before dropping the agent anyway.
const closeRunTimeout = 5 * time.Second

// session pairs a host-level session id with a live SDK agent and its single
// active run. At most one run per session is allowed, matching the SDK rule in
// doc/design/v1.0/01-architecture.md.
type session struct {
	id  string
	cwd string
	// mode is the permission mode the session was created with, after defaults.
	mode string
	// vision is whether the session opted into image attachments. It has to be
	// decided at option-build time — Options.Vision.Enabled is what makes the
	// SDK accept an attachment — so it is recorded here rather than read back
	// from the agent when a prompt arrives.
	vision bool

	mu      sync.Mutex
	agent   *anvil.Agent
	run     *anvil.Run
	runID   string
	runDone chan struct{}
}

// newSession reserves an id before the agent exists. The permission callback
// needs the session's identity at option-build time, and the SDK needs the
// options before it can hand back an agent, so the agent is attached afterwards.
func newSession(id, cwd, mode string, vision bool) *session {
	return &session{id: id, cwd: cwd, mode: mode, vision: vision}
}

func (s *session) attach(agent *anvil.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agent = agent
}

// sdk returns the live agent, or nil when attach has not run or the session was
// closed.
func (s *session) sdk() *anvil.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agent
}

// activeRunID reports the in-flight run's id, for correlating an approval.
func (s *session) activeRunID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runID
}

// beginRun claims the session for a run. It reports the previous run's id when
// the session is already busy.
func (s *session) beginRun(run *anvil.Run) (busyWith string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil {
		return s.runID, false
	}
	s.run = run
	s.runID = run.ID()
	s.runDone = make(chan struct{})
	return "", true
}

// endRun releases the session and wakes anything waiting in waitRun.
func (s *session) endRun(run *anvil.Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != run {
		return
	}
	s.run = nil
	s.runID = ""
	if s.runDone != nil {
		close(s.runDone)
		s.runDone = nil
	}
}

// activeRun returns the in-flight run, or nil when idle.
func (s *session) activeRun() (*anvil.Run, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run, s.runID
}

// waitRun blocks until the current run drains, or the timeout elapses.
func (s *session) waitRun(timeout time.Duration) bool {
	s.mu.Lock()
	done := s.runDone
	s.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// projectEvent maps one public SDK event onto the wire shape. It reports false
// for events the protocol does not carry.
//
// anvil.EventDone is deliberately not forwarded: the runtime emits it carrying
// the final assistant text, which has already reached the client as `assistant`
// deltas (streaming path) or as a single `assistant` event (non-streaming
// path). The authoritative terminal signal is the `done` notification built
// from anvil.RunResult, which is emitted exactly once for every prompt.
func projectEvent(sessionID, runID string, ev anvil.Event) (EventParams, bool) {
	switch ev.Type {
	case anvil.EventProgress, anvil.EventAssistant, anvil.EventReasoning,
		anvil.EventToolCall, anvil.EventToolResult, anvil.EventError:
	default:
		return EventParams{}, false
	}
	return EventParams{
		SessionID:  sessionID,
		RunID:      runID,
		Type:       string(ev.Type),
		Text:       ev.Text,
		ToolCallID: ev.ToolCallID,
		Name:       ev.Name,
		Args:       ev.Args,
		Content:    ev.Content,
		IsError:    ev.IsError,
		Step:       ev.Step,
		MaxSteps:   ev.MaxSteps,
		Code:       string(ev.Code),
	}, true
}

// projectDone builds the terminal envelope from anvil.RunResult. res may be nil
// when Wait itself failed, in which case runErr carries the reason.
func projectDone(sessionID, runID string, res *anvil.RunResult, runErr error) DoneParams {
	out := DoneParams{SessionID: sessionID, RunID: runID, Status: string(anvil.StatusError)}
	if res != nil {
		if res.ID != "" {
			out.RunID = res.ID
		}
		if res.Status != "" {
			out.Status = string(res.Status)
		}
		out.Result = res.Result
		out.Steps = res.Steps
		out.Err = res.Err
		out.Code = string(res.Code)
		if res.Usage != nil {
			out.Usage = &Usage{
				InputTokens:  res.Usage.InputTokens,
				OutputTokens: res.Usage.OutputTokens,
				TotalTokens:  res.Usage.TotalTokens,
			}
		}
	}
	if out.Err == "" && runErr != nil {
		out.Err = runErr.Error()
	}
	return out
}
