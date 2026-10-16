// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errNoPendingApproval is returned when an approval.response names a request
// that is no longer waiting — already answered, timed out, or aborted.
var errNoPendingApproval = errors.New("no pending approval for that request_id")

// approvalBroker owns every in-flight approval and the per-session allowlist.
//
// A gated tool call runs on the run's goroutine and blocks here until the client
// answers, the run is cancelled, or the timeout expires. Every path out of wait
// deregisters the request, so a late approval.response is rejected rather than
// silently ignored — the client learns that its answer did not land.
type approvalBroker struct {
	mu      sync.Mutex
	pending map[string]*pendingApproval
	// allowed keys are sessionID + "\x00" + tool + "\x00" + scope.
	allowed map[string]bool
}

type pendingApproval struct {
	sessionID string
	runID     string
	// timeout is how long to wait; zero or less waits until the run ends.
	timeout time.Duration
	ch      chan ApprovalResponseParams
	settled bool
}

func newApprovalBroker() *approvalBroker {
	return &approvalBroker{
		pending: map[string]*pendingApproval{},
		allowed: map[string]bool{},
	}
}

func approvalKey(sessionID, tool, scope string) string {
	return sessionID + "\x00" + tool + "\x00" + scope
}

// allowSession remembers an approval for the rest of the session.
func (b *approvalBroker) allowSession(sessionID, tool, scope string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allowed[approvalKey(sessionID, tool, scope)] = true
}

// sessionAllowed reports whether the tool and scope were already approved. A
// blanket approval (empty scope) covers every scope of that tool.
func (b *approvalBroker) sessionAllowed(sessionID, tool, scope string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.allowed[approvalKey(sessionID, tool, "")] {
		return true
	}
	return b.allowed[approvalKey(sessionID, tool, scope)]
}

// begin registers a request. The caller must call wait or cancel afterwards;
// begin alone would leak the entry.
func (b *approvalBroker) begin(sessionID, runID, requestID string, timeout time.Duration) *pendingApproval {
	p := &pendingApproval{
		sessionID: sessionID,
		runID:     runID,
		timeout:   timeout,
		ch:        make(chan ApprovalResponseParams, 1),
	}
	b.mu.Lock()
	b.pending[requestID] = p
	b.mu.Unlock()
	return p
}

// forget deregisters a request. It reports whether this caller was the one that
// settled it, which is how the timeout and cancel paths avoid double-reporting.
func (b *approvalBroker) forget(requestID string, p *pendingApproval) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur, ok := b.pending[requestID]; ok && cur == p {
		delete(b.pending, requestID)
		return true
	}
	return false
}

// settle marks p answered and delivers the response. It reports false when the
// request was already settled, or is no longer registered.
func (b *approvalBroker) settle(requestID string, p *pendingApproval, resp ApprovalResponseParams) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p.settled {
		return false
	}
	if cur, ok := b.pending[requestID]; !ok || cur != p {
		return false
	}
	p.settled = true
	delete(b.pending, requestID)
	// Buffered, so this never blocks while holding the lock.
	p.ch <- resp
	return true
}

// answer resolves a client approval.response.
func (b *approvalBroker) answer(resp ApprovalResponseParams) error {
	b.mu.Lock()
	p, ok := b.pending[resp.RequestID]
	b.mu.Unlock()
	if !ok {
		return errNoPendingApproval
	}
	decision := resp.Decision
	switch decision {
	case DecisionAllow, DecisionAllowSession, DecisionDeny:
	default:
		return errors.New("unknown decision: " + resp.Decision)
	}
	if !b.settle(resp.RequestID, p, resp) {
		return errNoPendingApproval
	}
	return nil
}

// abortSession denies every pending request belonging to a session. It is how
// cancel and session.close release a blocked tool call.
func (b *approvalBroker) abortSession(sessionID, message string) int {
	return b.abort(func(p *pendingApproval) bool { return p.sessionID == sessionID }, message)
}

// abortRun denies every pending request belonging to a run. The runtime
// cancels the run context when it ends, which already releases the callback;
// this makes the release explicit and gives the client a reason.
func (b *approvalBroker) abortRun(runID, message string) int {
	return b.abort(func(p *pendingApproval) bool { return p.runID == runID }, message)
}

// abortAll denies everything pending, for host shutdown.
func (b *approvalBroker) abortAll(message string) int {
	return b.abort(func(*pendingApproval) bool { return true }, message)
}

func (b *approvalBroker) abort(match func(*pendingApproval) bool, message string) int {
	b.mu.Lock()
	var hits []struct {
		id string
		p  *pendingApproval
	}
	for id, p := range b.pending {
		if match(p) {
			hits = append(hits, struct {
				id string
				p  *pendingApproval
			}{id, p})
		}
	}
	b.mu.Unlock()

	n := 0
	for _, h := range hits {
		if b.settle(h.id, h.p, ApprovalResponseParams{
			RequestID: h.id,
			Decision:  DecisionDeny,
			Message:   message,
		}) {
			n++
		}
	}
	return n
}

// wait blocks until the client answers, the timeout expires, or ctx ends. Every
// path returns and deregisters, so a run can never be stranded on an approval
// the user never gave.
func (b *approvalBroker) wait(ctx context.Context, requestID string, p *pendingApproval) ApprovalResponseParams {
	var timeout <-chan time.Time
	if p.timeout > 0 {
		t := time.NewTimer(p.timeout)
		defer t.Stop()
		timeout = t.C
	}

	select {
	case resp := <-p.ch:
		return resp
	case <-timeout:
		return b.expire(requestID, p, "the approval request timed out with no answer")
	case <-ctx.Done():
		return b.expire(requestID, p, "the run ended before the approval was answered")
	}
}

// expire denies the request on behalf of the host. When another path settled it
// in the same instant, their answer wins.
func (b *approvalBroker) expire(requestID string, p *pendingApproval, message string) ApprovalResponseParams {
	denial := ApprovalResponseParams{RequestID: requestID, Decision: DecisionDeny, Message: message}
	if b.settle(requestID, p, denial) {
		return denial
	}
	return <-p.ch
}
