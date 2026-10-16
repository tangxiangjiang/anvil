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
	"time"

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

	approvals *approvalBroker

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
		in:        in,
		out:       newOutbound(out),
		approvals: newApprovalBroker(),
		cfg:       cfg,
		cwd:       cfg.CWD,
		sessions:  map[string]*session{},
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
	case MethodApprovalResponse:
		return s.handleApprovalResponse(req.Params)
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
	return []string{"streaming", "sessions", "resume", "cancel", "models", "approval", "context", "vision"}
}

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

func (s *Server) handleSessionNew(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionParams](params)
	if err != nil {
		return nil, errInvalidParams("session.new: " + err.Error())
	}
	// The session is reserved before the agent exists: the permission callback
	// needs its identity, and the SDK needs the options before it can hand back
	// an agent.
	sessID := "sess-" + newID()
	mode, rpcErr := permissionMode(p.PermissionMode)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sess := newSession(sessID, s.cwdPath(), mode, p.Vision)
	opts, rpcErr := s.buildOptions(p, sess)
	if rpcErr != nil {
		return nil, rpcErr
	}
	agent, err := anvil.Create(ctx, opts)
	if err != nil {
		return nil, errHost(err)
	}
	sess.attach(agent)
	s.addSession(sess)
	s.logf("session.new id=%s agent=%s model=%s mode=%s", sess.id, agent.AgentID(), opts.Model.ID, mode)
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
	sessID := "sess-" + newID()
	mode, rpcErr := permissionMode(p.PermissionMode)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sess := newSession(sessID, s.cwdPath(), mode, p.Vision)
	opts, rpcErr := s.buildOptions(p, sess)
	if rpcErr != nil {
		return nil, rpcErr
	}
	agent, err := anvil.Resume(ctx, p.AgentID, opts)
	if err != nil {
		return nil, errHost(err)
	}
	sess.attach(agent)
	s.addSession(sess)
	s.logf("session.resume id=%s agent=%s mode=%s", sess.id, agent.AgentID(), mode)
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
	// Release anything blocked on an approval before the agent goes, so the
	// tool call unwinds with a reason instead of by context cancellation alone.
	s.approvals.abortSession(sess.id, "the session was closed")
	if run, _ := sess.activeRun(); run != nil {
		_ = run.Cancel(context.Background())
		// Let the drain goroutine emit the terminal done before the agent goes.
		sess.waitRun(closeRunTimeout)
	}
	if agent := sess.sdk(); agent != nil {
		if err := agent.Close(); err != nil {
			return nil, errHost(err)
		}
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
	agent := sess.sdk()
	if agent == nil {
		return nil, errCode(CodeNoSession, "session is not open: "+p.SessionID)
	}
	// Order is directive prefix, context block, user message: the model reads
	// the request last, immediately after the material it is about.
	message := p.Message
	block, rpcErr := s.composePromptContext(p.Context)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if block != "" {
		message = block + "\n\n" + message
	}
	if sess.mode == ModePlan {
		message = planPromptPrefix + "\n\n" + message
	}

	var sendOpts []anvil.SendOption
	if len(p.Attachments) > 0 {
		if !sess.vision {
			// Named as a setting rather than silently ignored: a run that
			// quietly drops the screenshot looks like a model that cannot see.
			return nil, errInvalidParams(
				"prompt: attachments require a session created with vision: true")
		}
		atts, rpcErr := attachmentsFor(p.Attachments)
		if rpcErr != nil {
			return nil, rpcErr
		}
		sendOpts = append(sendOpts, anvil.WithAttachments(atts...))
	}

	run, err := agent.Send(ctx, message, sendOpts...)
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
		AgentID:   agent.AgentID(),
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
	// Answer any approval still waiting on this run first: the tool call is
	// blocked inside the run, and a cancel that left it waiting would keep the
	// user staring at a prompt for a run that is already gone.
	s.approvals.abortRun(runID, "the run was cancelled")
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
func (s *Server) buildOptions(p SessionParams, sess *session) (anvil.Options, *RPCError) {
	groups := groupsForMode(sess.mode, p.PermissionGroups)
	opts := anvil.Options{
		PermissionGroups: groups,
		Provider:         anvil.Provider(firstNonEmpty(p.Provider, s.cfg.Provider, os.Getenv("ANVIL_PROVIDER"))),
		BaseURL:          firstNonEmpty(p.BaseURL, s.cfg.BaseURL, os.Getenv("ANVIL_BASE_URL")),
		SystemPrompt:     p.SystemPrompt,
		MaxSteps:         p.MaxSteps,
		BuiltinTools:     p.BuiltinTools,
		DisallowedTools:  p.DisallowedTools,
		Skills:           p.Skills,
		Plugins:          p.Plugins,
		Local: anvil.LocalOptions{
			CWD:             s.cwdPath(),
			LoadRules:       p.LoadRules,
			LoadCursorRules: p.LoadCursorRules,
			Store:           p.Store,
		},
		// Off unless the client asks: Vision.Enabled is what makes the SDK
		// accept an attachment, and enabling it also advertises read_image.
		Vision: anvil.VisionOptions{Enabled: p.Vision},
	}
	if len(p.TestCommand) > 0 {
		opts.Test = anvil.TestOptions{Command: p.TestCommand}
	}
	if len(p.BuildCommand) > 0 {
		opts.Build = anvil.BuildOptions{Command: p.BuildCommand}
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
	if sess.mode == ModePlan {
		// Plan mode removes the tools that can change the workspace, so the
		// model produces a plan instead of acting on it. The instruction that
		// goes with it is a prompt prefix; see handlePrompt.
		opts.DisallowedTools = append(append([]string(nil), opts.DisallowedTools...), planDisallowedTools...)
	}
	if sess.mode != ModeBypass {
		opts.Permission = s.permissionHook(sess, approvalTimeout(p.ApprovalTimeoutMS))
	}
	return opts, nil
}

// planDisallowedTools is what plan mode withholds. Everything else stays
// available, and anything gated still round-trips to the client.
var planDisallowedTools = []string{"write", "edit", "shell", "verify"}

// planPromptPrefix is prepended to the user's message in plan mode. It is a
// prompt prefix rather than Options.SystemPrompt because that field *replaces*
// Anvil's default system text, and the host has no way to append to text it
// cannot read.
const planPromptPrefix = "Plan mode is on. Research the task and reply with a concrete, ordered " +
	"plan of the changes you would make — files, functions, and the shape of each edit. " +
	"Do not make changes: the editing, shell, and verification tools are unavailable in this mode."

// permissionMode validates a requested mode and applies the default.
func permissionMode(raw string) (string, *RPCError) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ModeAsk, nil
	case ModeAsk, ModeAcceptEdits, ModePlan, ModeBypass:
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", errInvalidParams("unknown permission_mode: " + raw)
	}
}

// groupsForMode resolves the effective gated group set.
//
// The mode picks the base set; an explicit permission_groups overrides it.
// accept_edits then drops the write and edit selectors, which is how it stops
// asking about in-workspace edits — a composition of public SDK options rather
// than a special case inside the callback.
func groupsForMode(mode string, requested []string) []string {
	if mode == ModeBypass {
		return nil
	}
	groups := requested
	if len(groups) == 0 {
		groups = DefaultPermissionGroups
	}
	if mode != ModeAcceptEdits {
		return append([]string(nil), groups...)
	}
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if editSelectors[strings.ToLower(strings.TrimSpace(g))] {
			continue
		}
		out = append(out, g)
	}
	return out
}

// editSelectors are the selectors accept_edits withholds from the gate: the two
// groups that only change workspace files, plus the tool names they expand to,
// in case a client named a tool directly.
var editSelectors = map[string]bool{
	"write": true, "edit": true,
	"write_file": true, "edit_file": true, "apply_patch": true,
}

// approvalTimeout resolves the per-session wait. Zero means the default;
// negative means wait until the run ends, however long that is.
func approvalTimeout(ms int) time.Duration {
	switch {
	case ms == 0:
		return DefaultApprovalTimeout
	case ms < 0:
		return 0
	default:
		return time.Duration(ms) * time.Millisecond
	}
}

// ---------------------------------------------------------------------------
// approval
// ---------------------------------------------------------------------------

// permissionHook is the SDK permission callback for one session. It runs on the
// run's goroutine and blocks until the client answers, the run is cancelled, or
// the timeout expires — a denied call then reaches the model as a tool error.
func (s *Server) permissionHook(sess *session, timeout time.Duration) anvil.PermissionFunc {
	return func(ctx context.Context, req anvil.PermissionRequest) (anvil.PermissionDecision, error) {
		scope := approvalScope(req.Preview)
		if s.approvals.sessionAllowed(sess.id, req.ToolName, scope) {
			return anvil.PermissionDecision{Allow: true}, nil
		}

		requestID := "appr-" + newID()
		runID := sess.activeRunID()
		p := s.approvals.begin(sess.id, runID, requestID, timeout)
		s.notify(NotifyApprovalRequest, ApprovalRequestParams{
			RequestID: requestID,
			SessionID: sess.id,
			RunID:     runID,
			Tool:      req.ToolName,
			Args:      req.Args,
			Preview:   approvalPreview(req.Preview),
			Decisions: []string{DecisionAllow, DecisionAllowSession, DecisionDeny},
			TimeoutMS: int(timeout / time.Millisecond),
		})

		resp := s.approvals.wait(ctx, requestID, p)
		switch resp.Decision {
		case DecisionAllow:
			return anvil.PermissionDecision{Allow: true, Args: resp.Args}, nil
		case DecisionAllowSession:
			s.approvals.allowSession(sess.id, req.ToolName, resp.Scope)
			return anvil.PermissionDecision{Allow: true, Args: resp.Args}, nil
		default:
			msg := resp.Message
			if msg == "" {
				msg = "the user denied this " + req.ToolName + " call"
			}
			return anvil.PermissionDecision{Allow: false, Message: msg}, nil
		}
	}
}

// approvalScope is what a session-wide approval is keyed on. The client sees it
// as preview.path and may echo it back narrowed; the host never interprets it.
func approvalScope(p anvil.ToolPreview) string {
	return p.Path
}

func approvalPreview(p anvil.ToolPreview) ApprovalPreview {
	return ApprovalPreview{
		Kind:      p.Kind,
		Path:      p.Path,
		Diff:      p.Diff,
		Before:    p.Before,
		After:     p.After,
		Truncated: p.Truncated,
		Command:   p.Command,
		Summary:   p.Summary,
	}
}

func (s *Server) handleApprovalResponse(params json.RawMessage) (any, *RPCError) {
	p, err := decode[ApprovalResponseParams](params)
	if err != nil {
		return nil, errInvalidParams("approval.response: " + err.Error())
	}
	if strings.TrimSpace(p.RequestID) == "" {
		return nil, errInvalidParams("approval.response: request_id is required")
	}
	switch p.Decision {
	case DecisionAllow, DecisionAllowSession, DecisionDeny:
	default:
		// A malformed answer is the client's bug, not a stale request id, so it
		// must not be reported as CodeNoApproval — that would read as "too late".
		return nil, errInvalidParams("approval.response: unknown decision: " + p.Decision)
	}
	if err := s.approvals.answer(p); err != nil {
		return nil, errCode(CodeNoApproval, err.Error())
	}
	return map[string]any{}, nil
}

func sessionResult(sess *session, opts anvil.Options) SessionResult {
	return SessionResult{
		SessionID:      sess.id,
		AgentID:        sess.sdk().AgentID(),
		CWD:            opts.Local.CWD,
		Provider:       string(opts.Provider),
		Model:          opts.Model.ID,
		PermissionMode: sess.mode,
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

	// Nothing may be left waiting on an approval the host will never deliver.
	s.approvals.abortAll("the host is shutting down")

	for _, sess := range all {
		if run, runID := sess.activeRun(); run != nil {
			s.approvals.abortRun(runID, "the host is shutting down")
			_ = run.Cancel(context.Background())
			sess.waitRun(closeRunTimeout)
		}
		if agent := sess.sdk(); agent != nil {
			if err := agent.Close(); err != nil {
				s.logf("close session %s: %v", sess.id, err)
			}
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
