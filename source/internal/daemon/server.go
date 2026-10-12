// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strings"
	"sync"
)

// Server is the local agentd control server.
type Server struct {
	cfg    Config
	slots  *Slots
	runner JobRunner
	log    *log.Logger

	mu        sync.Mutex
	listener  net.Listener
	accepting bool // false = drain (reject new assigns)
	restoring bool // true while hydrating workers.json (skip persist)
}

// NewServer constructs a server (does not listen yet).
func NewServer(cfg Config, runner JobRunner, logger *log.Logger) (*Server, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if runner == nil {
		runner = &EmbeddedRunner{
			PluginDirs: append([]string(nil), cfg.Plugins.SearchPaths...),
			Deny:       append([]string(nil), cfg.Plugins.Deny...),
			LLM:        cfg.LLM,
		}
	}
	if logger == nil {
		logger = log.Default()
	}
	// Start with zero workers; capacity grows only via project worker add (no anonymous slots).
	cfg.MaxWorkers = 0
	s := &Server{
		cfg:       cfg,
		slots:     NewSlots(0),
		runner:    runner,
		log:       logger,
		accepting: true,
	}
	s.restoreProjectWorkers()
	return s, nil
}

// Config returns a copy of the config.
func (s *Server) Config() Config { return s.cfg }

// ResolveJobRepo maps a job repo_id to an absolute project cwd.
// Accepts allowlist ids, worker ids, and sandbox-relative project paths
// (e.g. "flutter-chat-local" when the worker id is a display name).
func (s *Server) ResolveJobRepo(repoID string) (string, error) {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return "", fmt.Errorf("unknown repo_id %q", repoID)
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if cwd, err := cfg.ResolveRepo(repoID); err == nil {
		return cwd, nil
	}
	for _, w := range s.slots.Snapshot().Workers {
		if w.ID == repoID || w.RepoID == repoID {
			if w.ProjectPath != "" {
				return w.ProjectPath, nil
			}
		}
		if w.ProjectRel != "" && (w.ProjectRel == repoID || filepath.Base(w.ProjectRel) == repoID) {
			if w.ProjectPath != "" {
				return w.ProjectPath, nil
			}
		}
		if w.ProjectPath != "" && filepath.Base(w.ProjectPath) == repoID {
			return w.ProjectPath, nil
		}
	}
	return "", fmt.Errorf("unknown repo_id %q", repoID)
}

// ApplyClusterLLM merges scheduler-provisioned llm into the embedded runner.
func (s *Server) ApplyClusterLLM(body *ClusterLLMBody) {
	if s == nil || body == nil {
		return
	}
	if er, ok := s.runner.(*EmbeddedRunner); ok {
		cur := er.llmSnapshot()
		cur.MergeClusterLLM(*body)
		er.SetLLM(cur)
	}
}

// RunnerLLM returns a snapshot of embedded runner llm defaults (tests / debug).
func (s *Server) RunnerLLM() (LLMConfig, bool) {
	if er, ok := s.runner.(*EmbeddedRunner); ok {
		return er.llmSnapshot(), true
	}
	return LLMConfig{}, false
}

// Slots exposes the slot registry (tests).
func (s *Server) Slots() *Slots { return s.slots }

// SeedBoundWorker registers a repo allowlist entry and one idle project slot (tests / bootstrap).
func (s *Server) SeedBoundWorker(id, cwd string) error {
	if id == "" || cwd == "" {
		return fmt.Errorf("id and cwd required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.repoIndexLocked(id) < 0 {
		s.cfg.Repos = append(s.cfg.Repos, RepoEntry{ID: id, CWD: abs})
	}
	s.mu.Unlock()
	if _, err := s.slots.AddBoundWorker(id, id, "", abs, "", id, true); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg.MaxWorkers = s.slots.Max()
	s.mu.Unlock()
	return nil
}

// WorkerSnapshot returns capacity plus sandbox-relative project paths.
func (s *Server) WorkerSnapshot() WorkerListResultBody {
	snap := s.slots.Snapshot()
	for i := range snap.Workers {
		snap.Workers[i] = s.decorateWorker(snap.Workers[i])
	}
	return snap
}

// Accepting reports whether new jobs are accepted (not draining).
func (s *Server) Accepting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepting
}

// ApplyClusterControl applies scale/drain from the scheduler.
func (s *Server) ApplyClusterControl(body *ClusterControlBody) error {
	if s == nil || body == nil {
		return nil
	}
	if body.Accepting != nil {
		s.mu.Lock()
		s.accepting = *body.Accepting
		s.mu.Unlock()
	}
	if body.SetMaxWorkers != nil {
		// Capacity is project-bound only; reject anonymous scale-up.
		if err := s.slots.Resize(*body.SetMaxWorkers); err != nil {
			return err
		}
		s.mu.Lock()
		s.cfg.MaxWorkers = s.slots.Max()
		s.mu.Unlock()
	}
	return nil
}

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
		MaxWorkers:   s.slots.Max(),
		Capabilities: caps,
	}
	_ = w.WriteEnvelope(Envelope{Kind: KindHelloOK, ID: env.ID, Body: mustBody(ok)})
	return true
}

func (s *Server) handleWorkerList(w *connWriter, env Envelope) {
	snap := s.WorkerSnapshot()
	_ = w.WriteEnvelope(Envelope{Kind: KindWorkerListR, ID: env.ID, Body: mustBody(snap)})
}

func (s *Server) handleJobAssign(parent context.Context, w *connWriter, env Envelope) {
	var job JobAssignBody
	if err := DecodeBody(env, &job); err != nil || job.JobID == "" || job.RepoID == "" {
		_ = s.writeErr(w, env.ID, "invalid job.assign body", job.JobID)
		return
	}
	if !s.Accepting() {
		_ = s.writeErr(w, env.ID, "node is draining (not accepting jobs)", job.JobID)
		return
	}
	cwd, err := s.ResolveJobRepo(job.RepoID)
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
