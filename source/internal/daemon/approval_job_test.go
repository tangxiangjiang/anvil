// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

func TestJobApprovalRoundTripAndDuplicate(t *testing.T) {
	b := newJobApprovalBroker()
	events := make(chan JobEventBody, 4)
	job := JobAssignBody{JobID: "job-1", ApprovalSessionID: "wb-1", ApprovalTimeoutMS: 1000}
	hook := b.permission(job, func(ev JobEventBody) { events <- ev })
	out := make(chan anvil.PermissionDecision, 1)
	go func() {
		dec, _ := hook(context.Background(), anvil.PermissionRequest{
			ToolName: "write_file", Args: json.RawMessage(`{"path":"a"}`),
			Preview: anvil.ToolPreview{Path: "a"},
		})
		out <- dec
	}()
	req := (<-events).Approval
	if req == nil || req.RequestID == "" || req.SessionID != "wb-1" {
		t.Fatalf("bad request: %+v", req)
	}
	resp := JobApprovalResponseBody{
		JobID: "job-1", SessionID: "wb-1", RequestID: req.RequestID,
		Decision: ApprovalAllow, Args: json.RawMessage(`{"path":"b"}`),
	}
	if err := b.answer(resp); err != nil {
		t.Fatal(err)
	}
	if err := b.answer(resp); err != errStaleApproval {
		t.Fatalf("duplicate = %v, want stale", err)
	}
	dec := <-out
	if !dec.Allow || string(dec.Args) != `{"path":"b"}` {
		t.Fatalf("decision = %+v", dec)
	}
	if ev := <-events; ev.ApprovalResult == nil || ev.ApprovalResult.Status != "answered" {
		t.Fatalf("missing answered result: %+v", ev)
	}
}

func TestJobApprovalRejectsCrossSessionAndExpires(t *testing.T) {
	b := newJobApprovalBroker()
	events := make(chan JobEventBody, 4)
	job := JobAssignBody{JobID: "job-1", ApprovalSessionID: "wb-1", ApprovalTimeoutMS: 20}
	hook := b.permission(job, func(ev JobEventBody) { events <- ev })
	done := make(chan anvil.PermissionDecision, 1)
	go func() {
		dec, _ := hook(context.Background(), anvil.PermissionRequest{ToolName: "shell"})
		done <- dec
	}()
	req := (<-events).Approval
	if err := b.answer(JobApprovalResponseBody{
		JobID: "job-1", SessionID: "wb-other", RequestID: req.RequestID, Decision: ApprovalAllow,
	}); err != errStaleApproval {
		t.Fatalf("cross-session = %v", err)
	}
	select {
	case dec := <-done:
		if dec.Allow {
			t.Fatal("expired approval was allowed")
		}
	case <-time.After(time.Second):
		t.Fatal("approval did not expire")
	}
	if ev := <-events; ev.ApprovalResult == nil || ev.ApprovalResult.Status != "expired" {
		t.Fatalf("missing expiry event: %+v", ev)
	}
	if err := b.answer(JobApprovalResponseBody{
		JobID: "job-1", SessionID: "wb-1", RequestID: req.RequestID, Decision: ApprovalAllow,
	}); err != errStaleApproval {
		t.Fatalf("late response = %v", err)
	}
}

func TestJobApprovalAbortAndAllowSession(t *testing.T) {
	b := newJobApprovalBroker()
	events := make(chan JobEventBody, 8)
	job := JobAssignBody{JobID: "job-1", ApprovalSessionID: "wb-1", ApprovalTimeoutMS: 1000}
	hook := b.permission(job, func(ev JobEventBody) { events <- ev })
	done := make(chan anvil.PermissionDecision, 2)
	go func() {
		dec, _ := hook(context.Background(), anvil.PermissionRequest{
			ToolName: "write_file", Preview: anvil.ToolPreview{Path: "a"},
		})
		done <- dec
	}()
	req := (<-events).Approval
	if err := b.answer(JobApprovalResponseBody{
		JobID: "job-1", SessionID: "wb-1", RequestID: req.RequestID,
		Decision: ApprovalAllowSession, Scope: "a",
	}); err != nil {
		t.Fatal(err)
	}
	if dec := <-done; !dec.Allow {
		t.Fatal("allow_session denied")
	}
	<-events // answered outcome
	dec, _ := hook(context.Background(), anvil.PermissionRequest{
		ToolName: "write_file", Preview: anvil.ToolPreview{Path: "a"},
	})
	if !dec.Allow || len(events) != 0 {
		t.Fatal("remembered approval prompted again")
	}

	go func() {
		dec, _ := hook(context.Background(), anvil.PermissionRequest{ToolName: "shell"})
		done <- dec
	}()
	<-events
	b.abortJob("job-1", "stopped")
	if dec := <-done; dec.Allow {
		t.Fatal("aborted approval allowed")
	}
}
