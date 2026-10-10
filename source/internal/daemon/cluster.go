// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// ClusterEdge joins a scheduler and forwards assigns onto the local Server path.
type ClusterEdge struct {
	cfg    Config
	server *Server
	log    *log.Logger

	writeMu sync.Mutex
}

// NewClusterEdge wraps a running server for cluster mode.
func NewClusterEdge(cfg Config, server *Server, logger *log.Logger) *ClusterEdge {
	if logger == nil {
		logger = log.Default()
	}
	return &ClusterEdge{cfg: cfg, server: server, log: logger}
}

// Run joins, heartbeats, and handles cluster.assign until ctx done.
func (e *ClusterEdge) Run(ctx context.Context) error {
	if !e.cfg.Cluster.Enabled {
		<-ctx.Done()
		return nil
	}
	url := e.cfg.Cluster.URL
	if url == "" {
		return fmt.Errorf("cluster.url required when cluster.enabled")
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := e.session(ctx, url)
		if ctx.Err() != nil {
			return nil
		}
		e.log.Printf("cluster session ended: %v; retry in %s", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (e *ClusterEdge) session(ctx context.Context, url string) error {
	conn, err := Dial(url)
	if err != nil {
		return err
	}
	defer conn.Close()

	token := e.cfg.ClusterToken()
	nodeID := e.cfg.Cluster.NodeIDOr(e.cfg.NodeID)
	join, _ := MarshalBody(ClusterJoinBody{
		NodeID:     nodeID,
		Token:      token,
		MaxWorkers: e.cfg.MaxWorkers,
	})
	if err := e.write(conn, Envelope{Kind: KindClusterJoin, Body: join}); err != nil {
		return err
	}
	env, err := ReadEnvelope(conn)
	if err != nil {
		return err
	}
	if env.Kind != KindClusterJoinOK {
		return fmt.Errorf("cluster join rejected: kind=%s", env.Kind)
	}
	var okBody ClusterJoinOKBody
	_ = DecodeBody(env, &okBody)
	e.server.ApplyClusterLLM(okBody.LLM)

	sendHB := func() error {
		idle, busy := e.server.Slots().Counts()
		hb, _ := MarshalBody(ClusterHeartbeatBody{
			NodeID:       nodeID,
			Idle:         idle,
			Busy:         busy,
			Max:          e.cfg.MaxWorkers,
			Capabilities: e.cfg.PluginCapabilityLabels(),
		})
		return e.write(conn, Envelope{Kind: KindClusterHeartbeat, Body: hb})
	}
	if err := sendHB(); err != nil {
		return err
	}

	ticker := time.NewTicker(time.Duration(e.cfg.Cluster.HeartbeatSec) * time.Second)
	defer ticker.Stop()

	errCh := make(chan error, 1)
	go func() {
		for {
			msg, err := ReadEnvelope(conn)
			if err != nil {
				errCh <- err
				return
			}
			switch msg.Kind {
			case KindClusterAssign:
				var job JobAssignBody
				_ = DecodeBody(msg, &job)
				go e.forwardAssign(ctx, conn, job)
			case KindClusterConfig:
				var cfgBody ClusterConfigBody
				_ = DecodeBody(msg, &cfgBody)
				e.server.ApplyClusterLLM(cfgBody.LLM)
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case <-ticker.C:
			if err := sendHB(); err != nil {
				return err
			}
		}
	}
}

func (e *ClusterEdge) forwardAssign(ctx context.Context, sched net.Conn, job JobAssignBody) {
	cwd, err := e.cfg.ResolveRepo(job.RepoID)
	if err != nil {
		done, _ := MarshalBody(JobDoneBody{JobID: job.JobID, Status: "error", Error: err.Error()})
		_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: done})
		return
	}
	resolved, err := e.cfg.ResolvePlugins(job.Plugins)
	if err != nil {
		done, _ := MarshalBody(JobDoneBody{JobID: job.JobID, Status: "error", Error: err.Error()})
		_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: done})
		return
	}
	job.Plugins = resolved

	jobCtx, cancel := context.WithCancel(ctx)
	_, err = e.server.Slots().TryAcquire(job.JobID, cancel)
	if err != nil {
		cancel()
		done, _ := MarshalBody(JobDoneBody{JobID: job.JobID, Status: "error", Error: err.Error()})
		_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: done})
		return
	}
	defer e.server.Slots().Release(job.JobID)
	defer cancel()

	doneBody := e.server.runner.Run(jobCtx, job, cwd, func(ev JobEventBody) { _ = ev })
	body, _ := MarshalBody(doneBody)
	_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: body})
}

func (e *ClusterEdge) write(conn net.Conn, env Envelope) error {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	return WriteEnvelope(conn, env)
}

// NodeIDOr returns NodeID or fallback.
func (c ClusterConfig) NodeIDOr(fallback string) string {
	if c.NodeID != "" {
		return c.NodeID
	}
	return fallback
}
