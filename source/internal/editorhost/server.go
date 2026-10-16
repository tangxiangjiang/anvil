// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anvil-dev/anvil"
)

// maxLine bounds one inbound protocol line (tool args can be large).
const maxLine = 4 << 20

// errShutdown unwinds the serve loop on a clean shutdown request.
var errShutdown = errors.New("editorhost: shutdown")

// Config supplies host-level defaults. Every field may stay empty and be filled
// from the process environment instead.
type Config struct {
	Provider string
	BaseURL  string
	Model    string
	// CWD pre-seeds the workspace root. initialize may still override it.
	CWD string
	// Logf receives host diagnostics. Defaults to stderr.
	Logf func(format string, args ...any)
}

// Server speaks the editor host protocol over one stdio pair.
type Server struct {
	in  io.Reader
	out *outbound

	mu        sync.Mutex
	cfg       Config
	cwd       string
	client    ClientInfo
	inited    bool
	sessions  map[string]*session
	sessionID []string
}

// New builds a server bound to the given streams.
func New(cfg Config, in io.Reader, out io.Writer) *Server {
	if cfg.Logf == nil {
		cfg.Logf = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}
	return &Server{
		in:       in,
		out:      newOutbound(out),
		cfg:      cfg,
		cwd:      cfg.CWD,
		sessions: map[string]*session{},
	}
}

// Serve reads requests until stdin closes, shutdown is requested, or ctx is
// done. It returns nil on a clean end of stream.
func (s *Server) Serve(ctx context.Context) error {
	go s.out.run()

	err := s.serve(ctx)
	// Tear down sessions first so their terminal notifications are queued
	// before the writer is flushed, then wait for the queue to reach the client.
	s.closeAll()
	s.out.flush()

	if err == nil {
		err = s.out.firstErr()
	}
	return err
}

func (s *Server) serve(ctx context.Context) error {
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := s.handleLine(ctx, line); err != nil {
			if errors.Is(err, errShutdown) {
				return nil
			}
			return err
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handleLine(ctx context.Context, line []byte) error {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeError(nil, errCode(CodeParse, "parse error"))
		return nil
	}
	if req.ID == nil {
		return s.handleNotification(ctx, &req)
	}
	if req.Method == MethodShutdown {
		s.writeResult(req.ID, map[string]any{})
		return errShutdown
	}
	result, rpcErr := s.dispatch(ctx, &req)
	if rpcErr != nil {
		s.writeError(req.ID, rpcErr)
		return nil
	}
	s.writeResult(req.ID, result)
	return nil
}

// handleNotification processes ID-less messages. Only initialized and shutdown
// carry meaning; anything else is ignored per JSON-RPC.
func (s *Server) handleNotification(_ context.Context, req *Request) error {
	switch req.Method {
	case MethodInitialized:
		return nil
	case MethodShutdown:
		return errShutdown
	default:
		s.logf("ignoring unknown notification %q", req.Method)
		return nil
	}
}

func (s *Server) dispatch(ctx context.Context, req *Request) (any, *RPCError) {
	if req.Method == MethodInitialize {
		return s.handleInitialize(req.Params)
	}
	if !s.ready() {
		return nil, errCode(CodeInvalidRequest, "initialize must be called before "+req.Method)
	}
	switch req.Method {
	case MethodSessionNew:
		return s.handleSessionNew(ctx, req.Params)
	case MethodSessionResume:
		return s.handleSessionResume(ctx, req.Params)
	case MethodSessionList:
		return s.handleSessionList(ctx, req.Params)
	case MethodSessionClose:
		return s.handleSessionClose(req.Params)
	case MethodPrompt:
		return s.handlePrompt(ctx, req.Params)
	case MethodCancel:
		return s.handleCancel(ctx, req.Params)
	case MethodModelsList:
		return s.handleModelsList(ctx, req.Params)
	default:
		return nil, errCode(CodeMethodNotFound, "unknown method: "+req.Method)
	}
}

// ---------------------------------------------------------------------------
// initialize
// ---------------------------------------------------------------------------

func (s *Server) handleInitialize(params json.RawMessage) (any, *RPCError) {
	p, err := decode[InitializeParams](params)
	if err != nil {
		return nil, errInvalidParams("initialize: " + err.Error())
	}
	if p.Protocol != 0 && p.Protocol != ProtocolVersion {
		return nil, errCode(CodeInvalidParams, fmt.Sprintf(
			"unsupported protocol %d (this host speaks %d)", p.Protocol, ProtocolVersion))
	}
	cwd := strings.TrimSpace(p.CWD)
	if cwd == "" {
		cwd = s.cfg.CWD
	}
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, errCode(CodeInternal, "resolve cwd: "+err.Error())
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, errInvalidParams("cwd: " + err.Error())
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, errInvalidParams("cwd is not a directory: " + abs)
	}

	s.mu.Lock()
	s.cwd = abs
	s.client = p.Client
	s.inited = true
	s.mu.Unlock()

	s.logf("initialized cwd=%s client=%s/%s", abs, p.Client.Name, p.Client.Version)
	return InitializeResult{
		Protocol:     ProtocolVersion,
		Host:         "anvil-host " + Version,
		CWD:          abs,
		Capabilities: capabilities(),
	}, nil
}

func capabilities() []string {
	// "approval" joins this list in p_0.35.3.
	return []string{"streaming", "sessions", "resume", "cancel", "models"}
}

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

func (s *Server) handleSessionNew(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionParams](params)
	if err != nil {
		return nil, errInvalidParams("session.new: " + err.Error())
	}
	opts, rpcErr := s.buildOptions(p)
	if rpcErr != nil {
		return nil, rpcErr
	}
	agent, err := anvil.Create(ctx, opts)
	if err != nil {
		return nil, errHost(err)
	}
	sess := newSession("sess-"+newID(), agent, opts.Local.CWD)
	s.addSession(sess)
	s.logf("session.new id=%s agent=%s model=%s", sess.id, agent.AgentID(), opts.Model.ID)
	return sessionResult(sess, opts), nil
}

func (s *Server) handleSessionResume(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionParams](params)
	if err != nil {
		return nil, errInvalidParams("session.resume: " + err.Error())
	}
	if strings.TrimSpace(p.AgentID) == "" {
		return nil, errInvalidParams("session.resume: agent_id is required")
	}
	opts, rpcErr := s.buildOptions(p)
	if rpcErr != nil {
		return nil, rpcErr
	}
	agent, err := anvil.Resume(ctx, p.AgentID, opts)
	if err != nil {
		return nil, errHost(err)
	}
	sess := newSession("sess-"+newID(), agent, opts.Local.CWD)
	s.addSession(sess)
	s.logf("session.resume id=%s agent=%s", sess.id, agent.AgentID())
	return sessionResult(sess, opts), nil
}

func (s *Server) handleSessionList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionListParams](params)
	if err != nil {
		return nil, errInvalidParams("session.list: " + err.Error())
	}
	infos, err := anvil.List(ctx, anvil.ListOptions{
		Local: anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
	})
	if err != nil {
		return nil, errHost(err)
	}
	out := SessionListResult{Sessions: make([]SessionInfo, 0, len(infos))}
	for _, in := range infos {
		entry := SessionInfo{
			AgentID:    in.ID,
			CWD:        in.CWD,
			Provider:   in.Provider,
			Model:      in.Model,
			LastStatus: in.LastStatus,
			LastRunID:  in.LastRunID,
			Runs:       in.Runs,
		}
		if !in.CreatedAt.IsZero() {
			entry.CreatedAt = in.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if !in.UpdatedAt.IsZero() {
			entry.UpdatedAt = in.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		out.Sessions = append(out.Sessions, entry)
	}
	if p.Limit > 0 && len(out.Sessions) > p.Limit {
		out.Sessions = out.Sessions[:p.Limit]
	}
	return out, nil
}

func (s *Server) handleSessionClose(params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionRefParams](params)
	if err != nil {
		return nil, errInvalidParams("session.close: " + err.Error())
	}
	sess, ok := s.takeSession(p.SessionID)
	if !ok {
		return nil, errCode(CodeNoSession, "unknown session: "+p.SessionID)
	}
	if run, _ := sess.activeRun(); run != nil {
		_ = run.Cancel(context.Background())
		// Let the drain goroutine emit the terminal done before the agent goes.
		sess.waitRun(closeRunTimeout)
	}
	if err := sess.agent.Close(); err != nil {
		return nil, errHost(err)
	}
	s.logf("session.close id=%s", sess.id)
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// prompt / cancel
// ---------------------------------------------------------------------------

func (s *Server) handlePrompt(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[PromptParams](params)
	if err != nil {
		return nil, errInvalidParams("prompt: " + err.Error())
	}
	sess, ok := s.session(p.SessionID)
	if !ok {
		return nil, errCode(CodeNoSession, "unknown session: "+p.SessionID)
	}
	if strings.TrimSpace(p.Message) == "" {
		return nil, errInvalidParams("prompt: message is required")
	}
	if _, runID := sess.activeRun(); runID != "" {
		return nil, errCode(CodeRunInProgress, "session already has an active run: "+runID)
	}
	run, err := sess.agent.Send(ctx, p.Message)
	if err != nil {
		return nil, errHost(err)
	}
	if busy, ok := sess.beginRun(run); !ok {
		_ = run.Cancel(context.Background())
		return nil, errCode(CodeRunInProgress, "session already has an active run: "+busy)
	}
	go s.drain(sess, run)
	return PromptResult{
		SessionID: sess.id,
		RunID:     run.ID(),
		AgentID:   sess.agent.AgentID(),
	}, nil
}

func (s *Server) handleCancel(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[CancelParams](params)
	if err != nil {
		return nil, errInvalidParams("cancel: " + err.Error())
	}
	sess, ok := s.session(p.SessionID)
	if !ok {
		return nil, errCode(CodeNoSession, "unknown session: "+p.SessionID)
	}
	run, runID := sess.activeRun()
	if run == nil {
		return nil, errCode(CodeNoRun, "session has no active run")
	}
	if p.RunID != "" && p.RunID != runID {
		return nil, errCode(CodeNoRun, "session is running "+runID+", not "+p.RunID)
	}
	if err := run.Cancel(ctx); err != nil {
		return nil, errHost(err)
	}
	return map[string]any{}, nil
}

// drain pumps one run: SDK events out as `event` notifications, then the
// terminal `done`. It owns the run until endRun.
func (s *Server) drain(sess *session, run *anvil.Run) {
	runID := run.ID()
	// Run.Stream ignores its context; ranging to close is the documented contract.
	for ev := range run.Stream(context.Background()) {
		if p, ok := projectEvent(sess.id, runID, ev); ok {
			s.notify(NotifyEvent, p)
		}
	}
	res, err := run.Wait(context.Background())
	sess.endRun(run)
	s.notify(NotifyDone, projectDone(sess.id, runID, res, err))
}

// ---------------------------------------------------------------------------
// models
// ---------------------------------------------------------------------------

func (s *Server) handleModelsList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[ModelsParams](params)
	if err != nil {
		return nil, errInvalidParams("models.list: " + err.Error())
	}
	baseURL := strings.TrimRight(firstNonEmpty(p.BaseURL, s.cfg.BaseURL, os.Getenv("ANVIL_BASE_URL")), "/")
	if baseURL == "" {
		return nil, errInvalidParams("models.list: base_url is required (params, host config, or ANVIL_BASE_URL)")
	}
	models, err := anvil.ListModels(ctx, anvil.ListModelsOptions{
		BaseURL: baseURL,
		APIKey:  os.Getenv("ANVIL_API_KEY"),
	})
	if err != nil {
		return nil, errHost(err)
	}
	out := ModelsResult{Models: make([]ModelEntry, 0, len(models))}
	for _, m := range models {
		out.Models = append(out.Models, ModelEntry{
			ID:            m.ID,
			OwnedBy:       m.OwnedBy,
			Provider:      m.Provider,
			UpstreamModel: m.UpstreamModel,
			Vision:        m.Vision,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// option assembly
// ---------------------------------------------------------------------------

// buildOptions maps session params onto public SDK options.
//
// APIKey is intentionally never set here: the SDK reads ANVIL_API_KEY from the
// process environment, which the client populates from SecretStorage. The key
// does not cross the protocol.
func (s *Server) buildOptions(p SessionParams) (anvil.Options, *RPCError) {
	opts := anvil.Options{
		Provider:        anvil.Provider(firstNonEmpty(p.Provider, s.cfg.Provider, os.Getenv("ANVIL_PROVIDER"))),
		BaseURL:         firstNonEmpty(p.BaseURL, s.cfg.BaseURL, os.Getenv("ANVIL_BASE_URL")),
		SystemPrompt:    p.SystemPrompt,
		MaxSteps:        p.MaxSteps,
		BuiltinTools:    p.BuiltinTools,
		DisallowedTools: p.DisallowedTools,
		Skills:          p.Skills,
		Plugins:         p.Plugins,
		Local: anvil.LocalOptions{
			CWD:             s.cwdPath(),
			LoadRules:       p.LoadRules,
			LoadCursorRules: p.LoadCursorRules,
			Store:           p.Store,
		},
	}
	if opts.Provider == "" {
		opts.Provider = anvil.ProviderOpenAI
	}
	model := firstNonEmpty(p.Model, s.cfg.Model, os.Getenv("ANVIL_MODEL"))
	if model == "" {
		return opts, errInvalidParams("model is required (session model, host config, or ANVIL_MODEL)")
	}
	opts.Model = anvil.Model{ID: model}
	for _, src := range p.SettingSources {
		clean := strings.ToLower(strings.TrimSpace(src))
		switch clean {
		case string(anvil.SettingSourceProject), string(anvil.SettingSourceUser):
			opts.Local.SettingSources = append(opts.Local.SettingSources, anvil.SettingSource(clean))
		case "":
		default:
			return opts, errInvalidParams("unknown setting source: " + src)
		}
	}
	return opts, nil
}

func sessionResult(sess *session, opts anvil.Options) SessionResult {
	return SessionResult{
		SessionID: sess.id,
		AgentID:   sess.agent.AgentID(),
		CWD:       opts.Local.CWD,
		Provider:  string(opts.Provider),
		Model:     opts.Model.ID,
	}
}

// ---------------------------------------------------------------------------
// session registry
// ---------------------------------------------------------------------------

func (s *Server) addSession(sess *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.id] = sess
	s.sessionID = append(s.sessionID, sess.id)
}

func (s *Server) session(id string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// takeSession removes a session from the registry.
func (s *Server) takeSession(id string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	delete(s.sessions, id)
	for i, existing := range s.sessionID {
		if existing == id {
			s.sessionID = append(s.sessionID[:i], s.sessionID[i+1:]...)
			break
		}
	}
	return sess, true
}

// closeAll cancels every run and closes every agent. Safe to call twice.
func (s *Server) closeAll() {
	s.mu.Lock()
	all := make([]*session, 0, len(s.sessions))
	for _, id := range s.sessionID {
		if sess, ok := s.sessions[id]; ok {
			all = append(all, sess)
		}
	}
	s.sessions = map[string]*session{}
	s.sessionID = nil
	s.mu.Unlock()

	for _, sess := range all {
		if run, _ := sess.activeRun(); run != nil {
			_ = run.Cancel(context.Background())
			sess.waitRun(closeRunTimeout)
		}
		if err := sess.agent.Close(); err != nil {
			s.logf("close session %s: %v", sess.id, err)
		}
	}
}

func (s *Server) ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inited
}

func (s *Server) cwdPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwd
}

// ---------------------------------------------------------------------------
// output
// ---------------------------------------------------------------------------

func (s *Server) write(v any) {
	s.out.push(v)
}

func (s *Server) writeResult(id *int64, result any) {
	s.write(Response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(id *int64, rpcErr *RPCError) {
	s.write(Response{JSONRPC: "2.0", ID: id, Error: rpcErr})
}

func (s *Server) notify(method string, params any) {
	s.write(Notification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func decode[T any](raw json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b[:])
}
