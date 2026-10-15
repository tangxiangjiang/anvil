// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"context"
	"net"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
)

// ServeCluster accepts agentd cluster edges until ctx is done.
func (h *Hub) ServeCluster(ctx context.Context) error {
	ln, err := daemon.Listen(h.cfg.ListenCluster)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	h.log.Printf("scheduler cluster listen %s", h.cfg.ListenCluster)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go h.handleClusterConn(ctx, conn)
	}
}

func (h *Hub) handleClusterConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	env, err := daemon.ReadEnvelope(conn)
	if err != nil {
		return
	}
	if env.Kind != daemon.KindClusterJoin {
		return
	}
	var join daemon.ClusterJoinBody
	if err := daemon.DecodeBody(env, &join); err != nil {
		return
	}
	if join.Token == "" || join.Token != h.Token() {
		_ = daemon.WriteEnvelope(conn, daemon.Envelope{Kind: daemon.KindError, Body: mustBody(daemon.ErrorBody{Message: "unauthorized"})})
		return
	}
	if join.NodeID == "" {
		_ = daemon.WriteEnvelope(conn, daemon.Envelope{Kind: daemon.KindError, Body: mustBody(daemon.ErrorBody{Message: "node_id required"})})
		return
	}

	wsID := ""
	if join.WorkspaceToken != "" {
		id, ok := h.ResolveWorkspaceToken(join.WorkspaceToken)
		if !ok {
			_ = daemon.WriteEnvelope(conn, daemon.Envelope{Kind: daemon.KindError, Body: mustBody(daemon.ErrorBody{Message: "unknown workspace_token"})})
			return
		}
		wsID = id
	}
	n := &nodeConn{
		id:          join.NodeID,
		name:        join.Name,
		description: join.Description,
		hostname:    join.Hostname,
		workspaceID: wsID,
		max:         join.MaxWorkers,
		idle:        join.MaxWorkers,
		conn:        conn,
		lastHB:      time.Now(),
	}
	h.registerNode(n)
	defer h.unregisterNode(n.id, conn)

	llm := h.GetLLM()
	okBody, _ := daemon.MarshalBody(daemon.ClusterJoinOKBody{LLM: &llm})
	if err := daemon.WriteEnvelope(conn, daemon.Envelope{Kind: daemon.KindClusterJoinOK, Body: okBody}); err != nil {
		return
	}
	h.log.Printf("cluster joined node=%s workspace=%s max_workers=%d", n.id, n.workspaceID, n.max)

	for {
		if ctx.Err() != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(h.cfg.StaleSec*2) * time.Second))
		msg, err := daemon.ReadEnvelope(conn)
		if err != nil {
			return
		}
		switch msg.Kind {
		case daemon.KindClusterHeartbeat:
			var hb daemon.ClusterHeartbeatBody
			_ = daemon.DecodeBody(msg, &hb)
			if hb.NodeID == "" {
				hb.NodeID = n.id
			}
			h.updateHeartbeat(n.id, hb)
		case daemon.KindClusterEvent:
			var ev daemon.JobEventBody
			_ = daemon.DecodeBody(msg, &ev)
			h.deliverEvent(ev)
		case daemon.KindClusterDone:
			var done daemon.JobDoneBody
			_ = daemon.DecodeBody(msg, &done)
			h.deliverDone(done)
		case daemon.KindClusterWorkerResult:
			h.deliverWorkerResult(msg)
		case daemon.KindClusterSandboxResult:
			h.deliverSandboxResult(msg)
		}
	}
}

func mustBody(v any) []byte {
	b, _ := daemon.MarshalBody(v)
	return b
}
