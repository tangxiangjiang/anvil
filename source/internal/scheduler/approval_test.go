// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"context"
	"io"
	"log"
	"net"
	"testing"

	"github.com/anvil-dev/anvil/internal/daemon"
)

func TestAnswerJobApprovalRoutesToOwningNodeAndWaitsForAck(t *testing.T) {
	h := NewHub(Config{}, log.New(io.Discard, "", 0))
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	h.nodes["node-1"] = &nodeConn{id: "node-1", conn: left}
	h.jobNode["job-1"] = "node-1"

	go func() {
		env, err := daemon.ReadEnvelope(right)
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		if env.Kind != daemon.KindJobApprovalResponse {
			t.Errorf("kind = %q", env.Kind)
			return
		}
		var body daemon.JobApprovalResponseBody
		_ = daemon.DecodeBody(env, &body)
		if body.JobID != "job-1" || body.SessionID != "wb-1" || body.RequestID != "appr-1" {
			t.Errorf("body = %+v", body)
		}
		result, _ := daemon.MarshalBody(daemon.JobApprovalResultBody{
			OK: true, JobID: body.JobID, RequestID: body.RequestID,
		})
		h.deliverApprovalResult(daemon.Envelope{ID: env.ID, Body: result})
	}()

	res, err := h.AnswerJobApproval(context.Background(), daemon.JobApprovalResponseBody{
		JobID: "job-1", SessionID: "wb-1", RequestID: "appr-1", Decision: daemon.ApprovalAllow,
	})
	if err != nil || !res.OK {
		t.Fatalf("answer = %+v, %v", res, err)
	}
}

func TestAnswerJobApprovalRejectsEndedJob(t *testing.T) {
	h := NewHub(Config{}, log.New(io.Discard, "", 0))
	if _, err := h.AnswerJobApproval(context.Background(), daemon.JobApprovalResponseBody{
		JobID: "ended", RequestID: "appr-1",
	}); err == nil {
		t.Fatal("ended job approval was accepted")
	}
}
