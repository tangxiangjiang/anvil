// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
)

// Server is the local agentd control server.
type Server struct {
	cfg    Config
	slots  *Slots
	runner JobRunner
	log    *log.Logger

	mu       sync.Mutex
	listener net.Listener
}

// NewServer constructs a server (does not listen yet).
func NewServer(cfg Config, runner JobRunner, logger *log.Logger) (*Server, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if runner == nil {
		runner = EmbeddedRunner{
			PluginDirs: append([]string(nil), cfg.Plugins.SearchPaths...),
			Deny:       append([]string(nil), cfg.Plugins.Deny...),
			LLM:        cfg.LLM,
		}
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		cfg:    cfg,
		slots:  NewSlots(cfg.MaxWorkers),
		runner: runner,
		log:    logger,
	}, nil
}

// Config returns a copy of the config.
func (s *Server) Config() Config { return s.cfg }

// Slots exposes the slot registry (tests).
func (s *Server) Slots() *Slots { return s.slots }

// Serve listens and handles connections until ctx is cancelled or Close.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := Listen(s.cfg.Listen)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	s.log.Printf("agentd listening on %s (max_workers=%d)", s.cfg.Listen, s.cfg.MaxWorkers)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go s.handleConn(ctx, conn)
	}
}

// Close stops the listener.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	w := &connWriter{conn: conn}
	authed := false
	for {
		env, err := ReadEnvelope(conn)
		if err != nil {
			return
		}
		if env.Proto != 0 && env.Proto != ProtoVersion {
			_ = s.writeErr(w, env.ID, fmt.Sprintf("unsupported proto %d", env.Proto), "")
			return
		}
		switch env.Kind {
		case KindHello:
			authed = s.handleHello(w, env)
			if !authed {
				return
			}
		default:
			if !authed {
				_ = s.writeErr(w, env.ID, "not authenticated", "")
				return
			}
			switch env.Kind {
			case KindWorkerList:
				s.handleWorkerList(w, env)
			case KindJobAssign:
				s.handleJobAssign(ctx, w, env)
			case KindJobCancel:
				s.handleJobCancel(w, env)
			default:
				_ = s.writeErr(w, env.ID, "unknown kind "+env.Kind, "")
			}
		}
	}
}

func (s *Server) handleHello(w *connWriter, env Envelope) bool {
	var body HelloBody
	_ = DecodeBody(env, &body)
	want := s.cfg.Token()
	if want == "" {
		_ = w.WriteEnvelope(Envelope{Kind: KindHelloFail, ID: env.ID, Body: mustBody(HelloFailBody{Message: "server token not configured (" + s.cfg.TokenEnv + ")"})})
		return false
	}
	if body.Token != want {
		_ = w.WriteEnvelope(Envelope{Kind: KindHelloFail, ID: env.ID, Body: mustBody(HelloFailBody{Message: "invalid token"})})
		return false
	}
	caps := []string{"job.assign", "job.cancel", "worker.list"}
	caps = append(caps, s.cfg.PluginCapabilityLabels()...)
	ok := HelloOKBody{
		NodeID:       s.cfg.NodeID,
		MaxWorkers:   s.cfg.MaxWorkers,
		Capabilities: caps,
	}
	_ = w.WriteEnvelope(Envelope{Kind: KindHelloOK, ID: env.ID, Body: mustBody(ok)})
	return true
}

func (s *Server) handleWorkerList(w *connWriter, env Envelope) {
	snap := s.slots.Snapshot()
	_ = w.WriteEnvelope(Envelope{Kind: KindWorkerListR, ID: env.ID, Body: mustBody(snap)})
}

func (s *Server) handleJobAssign(parent context.Context, w *connWriter, env Envelope) {
	var job JobAssignBody
	if err := DecodeBody(env, &job); err != nil || job.JobID == "" || job.RepoID == "" {
		_ = s.writeErr(w, env.ID, "invalid job.assign body", job.JobID)
		return
	}
	cwd, err := s.cfg.ResolveRepo(job.RepoID)
	if err != nil {
		_ = s.writeErr(w, env.ID, err.Error(), job.JobID)
		return
	}
	resolvedPlugins, err := s.cfg.ResolvePlugins(job.Plugins)
	if err != nil {
		_ = s.writeErr(w, env.ID, err.Error(), job.JobID)
		return
	}
	job.Plugins = resolvedPlugins

	ctx, cancel := context.WithCancel(parent)
	_, err = s.slots.TryAcquire(job.JobID, cancel)
	if err != nil {
		cancel()
		_ = s.writeErr(w, env.ID, err.Error(), job.JobID)
		return
	}

	go func() {
		defer s.slots.Release(job.JobID)
		defer cancel()
		done := s.runner.Run(ctx, job, cwd, func(ev JobEventBody) {
			_ = w.WriteEnvelope(Envelope{Kind: KindJobEvent, Body: mustBody(ev)})
		})
		_ = w.WriteEnvelope(Envelope{Kind: KindJobDone, Body: mustBody(done)})
	}()
}

func (s *Server) handleJobCancel(w *connWriter, env Envelope) {
	var body JobCancelBody
	_ = DecodeBody(env, &body)
	if body.JobID == "" {
		_ = s.writeErr(w, env.ID, "job_id required", "")
		return
	}
	if !s.slots.CancelJob(body.JobID) {
		_ = s.writeErr(w, env.ID, "no active job "+body.JobID, body.JobID)
		return
	}
	_ = w.WriteEnvelope(Envelope{Kind: KindJobEvent, ID: env.ID, Body: mustBody(JobEventBody{
		JobID: body.JobID,
		Type:  "status",
		Text:  "cancelling",
	})})
}

func (s *Server) writeErr(w *connWriter, id, msg, jobID string) error {
	return w.WriteEnvelope(Envelope{Kind: KindError, ID: id, Body: mustBody(ErrorBody{Message: msg, JobID: jobID})})
}

func mustBody(v any) json.RawMessage {
	b, err := MarshalBody(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
