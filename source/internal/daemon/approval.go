// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/id"
)

const (
	ApprovalAllow        = "allow"
	ApprovalAllowSession = "allow_session"
	ApprovalDeny         = "deny"

	defaultJobApprovalTimeout = 5 * time.Minute
	maxApprovalArgsBytes      = 32 << 10
)

var errStaleApproval = errors.New("approval is unknown, already answered, expired, or cancelled")

type jobApprovalWait struct {
	jobID     string
	sessionID string
	tool      string
	scope     string
	ch        chan JobApprovalResponseBody
}

type jobApprovalBroker struct {
	mu      sync.Mutex
	pending map[string]*jobApprovalWait
	allowed map[string]bool
}

func newJobApprovalBroker() *jobApprovalBroker {
	return &jobApprovalBroker{
		pending: map[string]*jobApprovalWait{},
		allowed: map[string]bool{},
	}
}

func approvalAllowKey(sessionID, tool, scope string) string {
	return sessionID + "\x00" + tool + "\x00" + scope
}

func (b *jobApprovalBroker) isAllowed(sessionID, tool, scope string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allowed[approvalAllowKey(sessionID, tool, "")] ||
		b.allowed[approvalAllowKey(sessionID, tool, scope)]
}

func (b *jobApprovalBroker) answer(resp JobApprovalResponseBody) error {
	if strings.TrimSpace(resp.JobID) == "" || strings.TrimSpace(resp.SessionID) == "" ||
		strings.TrimSpace(resp.RequestID) == "" {
		return errors.New("job_id, session_id, and request_id are required")
	}
	switch resp.Decision {
	case ApprovalAllow, ApprovalAllowSession, ApprovalDeny:
	default:
		return errors.New("unknown approval decision")
	}
	if len(resp.Args) > maxApprovalArgsBytes {
		return errors.New("approval args exceed 32 KiB")
	}
	if len(resp.Args) > 0 && !json.Valid(resp.Args) {
		return errors.New("approval args must be valid JSON")
	}

	b.mu.Lock()
	p := b.pending[resp.RequestID]
	if p == nil || p.jobID != resp.JobID || p.sessionID != resp.SessionID {
		b.mu.Unlock()
		return errStaleApproval
	}
	delete(b.pending, resp.RequestID)
	b.mu.Unlock()
	p.ch <- resp
	return nil
}

func (b *jobApprovalBroker) abortJob(jobID, message string) {
	b.mu.Lock()
	var hits []*jobApprovalWait
	for requestID, p := range b.pending {
		if p.jobID == jobID {
			delete(b.pending, requestID)
			hits = append(hits, p)
		}
	}
	b.mu.Unlock()
	for _, p := range hits {
		p.ch <- JobApprovalResponseBody{
			JobID: p.jobID, SessionID: p.sessionID, Decision: ApprovalDeny, Message: message,
			outcomeStatus: "cancelled",
		}
	}
}

func (b *jobApprovalBroker) permission(
	job JobAssignBody,
	emit func(JobEventBody),
) anvil.PermissionFunc {
	return func(ctx context.Context, req anvil.PermissionRequest) (anvil.PermissionDecision, error) {
		scope := req.Preview.Path
		if b.isAllowed(job.ApprovalSessionID, req.ToolName, scope) {
			return anvil.PermissionDecision{Allow: true}, nil
		}

		requestID := "appr-" + id.New()
		timeout := defaultJobApprovalTimeout
		if job.ApprovalTimeoutMS > 0 {
			timeout = time.Duration(job.ApprovalTimeoutMS) * time.Millisecond
		}
		p := &jobApprovalWait{
			jobID: job.JobID, sessionID: job.ApprovalSessionID, tool: req.ToolName,
			scope: scope, ch: make(chan JobApprovalResponseBody, 1),
		}
		b.mu.Lock()
		b.pending[requestID] = p
		b.mu.Unlock()

		expires := time.Now().Add(timeout).UTC()
		emit(JobEventBody{
			JobID: job.JobID, Type: "approval_request",
			Approval: &JobApprovalRequestBody{
				RequestID: requestID, SessionID: job.ApprovalSessionID,
				Tool: req.ToolName, Args: req.Args,
				Preview: JobApprovalPreview{
					Kind: req.Preview.Kind, Path: req.Preview.Path, Diff: req.Preview.Diff,
					Before: req.Preview.Before, After: req.Preview.After,
					Truncated: req.Preview.Truncated, Command: req.Preview.Command,
					Summary: req.Preview.Summary,
				},
				Decisions: []string{ApprovalAllow, ApprovalAllowSession, ApprovalDeny},
				TimeoutMS: int(timeout / time.Millisecond), ExpiresAt: expires.Format(time.RFC3339Nano),
				AgentID: req.AgentID, Depth: req.Depth,
			},
		})

		var resp JobApprovalResponseBody
		status := "answered"
		select {
		case resp = <-p.ch:
			if resp.outcomeStatus != "" {
				status = resp.outcomeStatus
			} else if ctx.Err() != nil {
				status = "cancelled"
			}
		case <-time.After(timeout):
			if b.remove(requestID, p) {
				resp = JobApprovalResponseBody{Decision: ApprovalDeny, Message: "approval request expired"}
				status = "expired"
			} else {
				resp = <-p.ch
			}
		case <-ctx.Done():
			if b.remove(requestID, p) {
				resp = JobApprovalResponseBody{Decision: ApprovalDeny, Message: "run cancelled before approval"}
				status = "cancelled"
			} else {
				resp = <-p.ch
			}
		}

		emit(JobEventBody{JobID: job.JobID, Type: "approval_result", ApprovalResult: &JobApprovalOutcomeBody{
			RequestID: requestID, Status: status, Decision: resp.Decision, Message: resp.Message,
		}})
		if resp.Decision == ApprovalAllowSession {
			b.mu.Lock()
			b.allowed[approvalAllowKey(job.ApprovalSessionID, req.ToolName, resp.Scope)] = true
			b.mu.Unlock()
		}
		if resp.Decision == ApprovalAllow || resp.Decision == ApprovalAllowSession {
			return anvil.PermissionDecision{Allow: true, Args: resp.Args}, nil
		}
		if resp.Message == "" {
			resp.Message = "the user denied this " + req.ToolName + " call"
		}
		return anvil.PermissionDecision{Allow: false, Message: resp.Message}, nil
	}
}

func (b *jobApprovalBroker) remove(requestID string, p *jobApprovalWait) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending[requestID] != p {
		return false
	}
	delete(b.pending, requestID)
	return true
}
