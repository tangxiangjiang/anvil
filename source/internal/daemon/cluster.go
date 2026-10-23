// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
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
	hostname, _ := os.Hostname()
	join, _ := MarshalBody(ClusterJoinBody{
		NodeID:         nodeID,
		Token:          token,
		MaxWorkers:     e.server.Slots().Max(),
		Hostname:       hostname,
		Name:           e.cfg.Name,
		Description:    e.cfg.Description,
		WorkspaceToken: strings.TrimSpace(e.cfg.Cluster.WorkspaceToken),
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
		snap := e.server.WorkerSnapshot()
		caps := e.cfg.PluginCapabilityLabels()
		if strings.TrimSpace(e.cfg.SandboxDir) != "" {
			caps = append(caps, "sandbox.review.read", "sandbox.review.git")
		}
		if e.server.SupportsApprovals() {
			caps = append(caps, "job.approval")
		}
		hb, _ := MarshalBody(ClusterHeartbeatBody{
			NodeID:       nodeID,
			Idle:         snap.Idle,
			Busy:         snap.Busy,
			Max:          snap.MaxWorkers,
			Accepting:    e.server.Accepting(),
			Workers:      snap.Workers,
			Capabilities: caps,
			Hostname:     hostname,
			Name:         e.cfg.Name,
			Description:  e.cfg.Description,
			SandboxDir:   e.cfg.SandboxDir,
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
			case KindJobCancel:
				var body JobCancelBody
				_ = DecodeBody(msg, &body)
				if body.JobID != "" {
					_ = e.server.CancelActiveJob(body.JobID)
				}
			case KindJobApprovalResponse:
				var body JobApprovalResponseBody
				_ = DecodeBody(msg, &body)
				res := e.server.AnswerApproval(body)
				encoded, _ := MarshalBody(res)
				_ = e.write(conn, Envelope{Kind: KindJobApprovalResult, ID: msg.ID, Body: encoded})
			case KindClusterConfig:
				var cfgBody ClusterConfigBody
				_ = DecodeBody(msg, &cfgBody)
				e.server.ApplyClusterLLM(cfgBody.LLM)
			case KindClusterControl:
				var ctl ClusterControlBody
				_ = DecodeBody(msg, &ctl)
				if err := e.server.ApplyClusterControl(&ctl); err != nil {
					e.log.Printf("cluster.control: %v", err)
				}
			case KindClusterWorkerAdd:
				var req ClusterWorkerAddBody
				_ = DecodeBody(msg, &req)
				ws, err := e.server.AddProjectWorker(req.WorkerID, req.Name, req.Description, req.ProjectPath, req.GitURL, req.InitGit, req.Readme, req.WorktreeBase, req.Branch, req.LLM)
				res := ClusterWorkerResultBody{Request: "add", Worker: ws}
				if err != nil {
					res.Error = err.Error()
				} else {
					res.OK = true
				}
				body, _ := MarshalBody(res)
				_ = e.write(conn, Envelope{Kind: KindClusterWorkerResult, ID: msg.ID, Body: body})
			case KindClusterWorkerRemove:
				var req ClusterWorkerRemoveBody
				_ = DecodeBody(msg, &req)
				err := e.server.RemoveProjectWorker(req.WorkerID)
				res := ClusterWorkerResultBody{Request: "remove"}
				if err != nil {
					res.Error = err.Error()
				} else {
					res.OK = true
					res.Worker = WorkerState{ID: req.WorkerID}
				}
				body, _ := MarshalBody(res)
				_ = e.write(conn, Envelope{Kind: KindClusterWorkerResult, ID: msg.ID, Body: body})
			case KindClusterWorkerStart:
				var req ClusterWorkerControlBody
				_ = DecodeBody(msg, &req)
				ws, err := e.server.StartProjectWorker(req.WorkerID)
				res := ClusterWorkerResultBody{Request: "start", Worker: ws}
				if err != nil {
					res.Error = err.Error()
				} else {
					res.OK = true
				}
				body, _ := MarshalBody(res)
				_ = e.write(conn, Envelope{Kind: KindClusterWorkerResult, ID: msg.ID, Body: body})
			case KindClusterWorkerShutdown:
				var req ClusterWorkerControlBody
				_ = DecodeBody(msg, &req)
				ws, err := e.server.ShutdownProjectWorker(req.WorkerID)
				res := ClusterWorkerResultBody{Request: "shutdown", Worker: ws}
				if err != nil {
					res.Error = err.Error()
				} else {
					res.OK = true
				}
				body, _ := MarshalBody(res)
				_ = e.write(conn, Envelope{Kind: KindClusterWorkerResult, ID: msg.ID, Body: body})
			case KindClusterSandboxList:
				var req ClusterSandboxListBody
				_ = DecodeBody(msg, &req)
				var res ClusterSandboxResultBody
				if req.WorkerID != "" {
					res = e.server.ListWorkerSandbox(req.WorkerID, req.Path)
				} else {
					var err error
					res, err = e.server.ListSandbox(req.Path)
					if err != nil {
						res = reviewError("list", "invalid_path", err)
					}
				}
				body, _ := MarshalBody(res)
				_ = e.write(conn, Envelope{Kind: KindClusterSandboxResult, ID: msg.ID, Body: body})
			case KindClusterSandboxMkdir:
				var req ClusterSandboxMkdirBody
				_ = DecodeBody(msg, &req)
				res, err := e.server.MkdirSandbox(req.Path)
				if err != nil {
					res = ClusterSandboxResultBody{Request: "mkdir", Error: err.Error()}
				}
				body, _ := MarshalBody(res)
				_ = e.write(conn, Envelope{Kind: KindClusterSandboxResult, ID: msg.ID, Body: body})
			case KindClusterSandboxRead:
				var req ClusterSandboxReadBody
				_ = DecodeBody(msg, &req)
				body, _ := MarshalBody(e.server.ReadWorkerSandbox(req.WorkerID, req.Path))
				_ = e.write(conn, Envelope{Kind: KindClusterSandboxResult, ID: msg.ID, Body: body})
			case KindClusterGitStatus:
				var req ClusterGitStatusBody
				_ = DecodeBody(msg, &req)
				body, _ := MarshalBody(e.server.GitStatusWorker(req.WorkerID))
				_ = e.write(conn, Envelope{Kind: KindClusterSandboxResult, ID: msg.ID, Body: body})
			case KindClusterGitDiff:
				var req ClusterGitDiffBody
				_ = DecodeBody(msg, &req)
				body, _ := MarshalBody(e.server.GitDiffWorker(req.WorkerID, req.Path, req.Staged))
				_ = e.write(conn, Envelope{Kind: KindClusterSandboxResult, ID: msg.ID, Body: body})
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
	if !e.server.Accepting() {
		done, _ := MarshalBody(JobDoneBody{JobID: job.JobID, Status: "error", Error: "node is draining (not accepting jobs)"})
		_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: done})
		return
	}
	cwd, err := e.server.ResolveJobRepo(job.RepoID)
	if err != nil {
		done, _ := MarshalBody(JobDoneBody{JobID: job.JobID, Status: "error", Error: err.Error()})
		_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: done})
		return
	}
	cfg := e.server.Config()
	resolved, err := cfg.ResolvePlugins(job.Plugins)
	if err != nil {
		done, _ := MarshalBody(JobDoneBody{JobID: job.JobID, Status: "error", Error: err.Error()})
		_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: done})
		return
	}
	job.Plugins = resolved

	jobCtx, cancel := context.WithCancel(ctx)
	workerID, err := e.server.Slots().TryAcquire(job.JobID, cancel)
	if err != nil {
		cancel()
		done, _ := MarshalBody(JobDoneBody{JobID: job.JobID, Status: "error", Error: err.Error()})
		_ = e.write(sched, Envelope{Kind: KindClusterDone, Body: done})
		return
	}
	defer e.server.Slots().Release(job.JobID)
	defer cancel()

	emit := func(ev JobEventBody) {
		ev.JobID = job.JobID
		body, err := MarshalBody(ev)
		if err != nil {
			return
		}
		_ = e.write(sched, Envelope{Kind: KindClusterEvent, Body: body})
	}
	var doneBody JobDoneBody
	if r, ok := e.server.runner.(*EmbeddedRunner); ok {
		doneBody = r.RunOn(jobCtx, job, cwd, e.server.Slots().LLMFor(workerID), emit)
	} else {
		doneBody = e.server.runner.Run(jobCtx, job, cwd, emit)
	}
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
