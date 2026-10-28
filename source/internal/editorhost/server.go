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
	// MCPTokenDir is where the host keeps MCP OAuth tokens. Empty means the
	// SDK's default (~/.anvil/mcp/oauth). Tests point it at a temp directory so
	// a sign-in never touches the user's real tokens.
	MCPTokenDir string
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

	// mcpMu guards the runtime registry, which lives apart from sessions: one
	// runtime serves every session that reads the same file layers, and it can
	// exist before any of them (a panel opened first).
	mcpMu sync.Mutex
	mcp   map[string]*hostMCPRuntime
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
		mcp:       map[string]*hostMCPRuntime{},
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
	case MethodSessionTranscript:
		return s.handleSessionTranscript(ctx, req.Params)
	case MethodSessionClose:
		return s.handleSessionClose(req.Params)
	case MethodSessionRename:
		return s.handleSessionRename(ctx, req.Params)
	case MethodSessionDelete:
		return s.handleSessionDelete(ctx, req.Params)
	case MethodSessionFork:
		return s.handleSessionFork(ctx, req.Params)
	case MethodSessionCompact:
		return s.handleSessionCompact(ctx, req.Params)
	case MethodSessionContext:
		return s.handleSessionContext(req.Params)
	case MethodCheckpointList:
		return s.handleCheckpointList(ctx, req.Params)
	case MethodCheckpointRestore:
		return s.handleCheckpointRestore(ctx, req.Params)
	case MethodPrompt:
		return s.handlePrompt(ctx, req.Params)
	case MethodCancel:
		return s.handleCancel(ctx, req.Params)
	case MethodModelsList:
		return s.handleModelsList(ctx, req.Params)
	case MethodSubagentsGet:
		return s.handleSubagentsGet(req.Params)
	case MethodSubagentsUpsert:
		return s.handleSubagentsUpsert(req.Params)
	case MethodSubagentsRemove:
		return s.handleSubagentsRemove(req.Params)
	case MethodSkillsList:
		return s.handleSkillsList(ctx, req.Params)
	case MethodRulesList:
		return s.handleRulesList(ctx, req.Params)
	case MethodMCPList:
		return s.handleMCPList(ctx, req.Params)
	case MethodMCPAdd:
		return s.handleMCPAdd(ctx, req.Params)
	case MethodMCPUpdate:
		return s.handleMCPUpdate(ctx, req.Params)
	case MethodMCPRemove:
		return s.handleMCPRemove(ctx, req.Params)
	case MethodMCPSetEnabled:
		return s.handleMCPSetEnabled(ctx, req.Params)
	case MethodMCPReconnect:
		return s.handleMCPReconnect(ctx, req.Params)
	case MethodMCPAuthenticate:
		return s.handleMCPAuthenticate(ctx, req.Params)
	case MethodMCPSignOut:
		return s.handleMCPSignOut(ctx, req.Params)
	case MethodPermissionsGet:
		return s.handlePermissionsGet(ctx, req.Params)
	case MethodPermissionsAdd:
		return s.handlePermissionsAdd(ctx, req.Params)
	case MethodPermissionsSet:
		return s.handlePermissionsSet(ctx, req.Params)
	case MethodTasksList:
		return s.handleTasksList(req.Params)
	case MethodTasksOutput:
		return s.handleTasksOutput(req.Params)
	case MethodTasksCancel:
		return s.handleTasksCancel(req.Params)
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
	return []string{"streaming", "sessions", "resume", "cancel", "models", "approval", "context", "vision", "plan", "skills", "rules", "permissions", "mcp", "mcp.runtime", "subagents", "tasks"}
}

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

func (s *Server) handleSessionNew(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionParams](params)
	if err != nil {
		return nil, errInvalidParams("session.new: " + err.Error())
	}
	// The flag is resolved before the mode because the mode resolution reads the
	// same file: a session that is not loading .anvil/permissions.json has not
	// read its declaration either.
	loadPermissions := boolOr(p.LoadPermissions, true)
	// The session is reserved before the agent exists: the permission callback
	// needs its identity, and the SDK needs the options before it can hand back
	// an agent.
	sessID := "sess-" + newID()
	mode, source, refused, rpcErr := s.resolveMode(p.PermissionMode, loadPermissions)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sess := newSession(sessID, s.cwdPath(), mode, p.Vision)
	sess.loadPermissions = loadPermissions
	sess.modeSource = source
	sess.modeRefused = refused
	opts, rpcErr := s.buildOptions(ctx, p, sess)
	if rpcErr != nil {
		return nil, rpcErr
	}
	agent, err := anvil.Create(ctx, opts)
	if err != nil {
		return nil, errHost(err)
	}
	sess.attach(agent)
	s.addSession(sess)
	s.logf("session.new id=%s agent=%s model=%s mode=%s source=%s", sess.id, agent.AgentID(), opts.Model.ID, mode, source)
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
	// Resume takes the same road to a mode as new does: an agent's transcript does
	// not record the mode it ran under, and a workspace that declares a starting
	// mode means it for the next session as much as for the first.
	loadPermissions := boolOr(p.LoadPermissions, true)
	mode, source, refused, rpcErr := s.resolveMode(p.PermissionMode, loadPermissions)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sessID := "sess-" + newID()
	sess := newSession(sessID, s.cwdPath(), mode, p.Vision)
	sess.loadPermissions = loadPermissions
	sess.modeSource = source
	sess.modeRefused = refused
	opts, rpcErr := s.buildOptions(ctx, p, sess)
	if rpcErr != nil {
		return nil, rpcErr
	}
	agent, err := anvil.Resume(ctx, p.AgentID, opts)
	if err != nil {
		return nil, errHost(err)
	}
	sess.attach(agent)
	s.addSession(sess)
	s.logf("session.resume id=%s agent=%s mode=%s source=%s", sess.id, agent.AgentID(), mode, source)
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
			Title:      in.Title,
			LastStatus: in.LastStatus,
			LastRunID:  in.LastRunID,
			Runs:       in.Runs,

			ParentAgentID:    in.ParentAgentID,
			ParentRunID:      in.ParentRunID,
			ParentToolCallID: in.ParentToolCallID,
			Depth:            in.Depth,
			Subagent:         in.Kind == "subagent",
			TaskTitle:        in.TaskTitle,
		}
		if !in.CreatedAt.IsZero() {
			entry.CreatedAt = in.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if !in.UpdatedAt.IsZero() {
			entry.UpdatedAt = in.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		// One projection for every usage on the wire, so a field added to the
		// shape cannot appear in `done` and be missing from the list.
		entry.Usage = usageWire(in.Usage)
		out.Sessions = append(out.Sessions, entry)
	}
	if p.Limit > 0 && len(out.Sessions) > p.Limit {
		out.Sessions = out.Sessions[:p.Limit]
	}
	return out, nil
}

// handleSessionRename names a stored session, as an appended `meta` record.
func (s *Server) handleSessionRename(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionRenameParams](params)
	if err != nil {
		return nil, errInvalidParams("session.rename: " + err.Error())
	}
	if p.AgentID == "" {
		return nil, errInvalidParams("session.rename: agent_id is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, errInvalidParams("session.rename: title is required")
	}
	opts := anvil.ListOptions{Local: anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store}}
	if err := anvil.RenameAgent(ctx, p.AgentID, p.Title, opts); err != nil {
		return nil, errHost(err)
	}
	s.logf("session.rename agent=%s", p.AgentID)
	return SessionRenameResult{AgentID: p.AgentID, Title: strings.TrimSpace(p.Title)}, nil
}

// handleSessionDelete forgets a stored session.
//
// A live session holding the same agent is not closed here: closing is the
// client's decision, and a tab that is still open would write the file again on
// its next prompt. The client closes the owning tab first, which is what makes
// the deletion stick.
func (s *Server) handleSessionDelete(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionDeleteParams](params)
	if err != nil {
		return nil, errInvalidParams("session.delete: " + err.Error())
	}
	if p.AgentID == "" {
		return nil, errInvalidParams("session.delete: agent_id is required")
	}
	opts := anvil.ListOptions{
		Local:       anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		Checkpoints: &anvil.CheckpointOptions{},
	}
	if err := anvil.DeleteAgent(ctx, p.AgentID, opts); err != nil {
		return nil, errHost(err)
	}
	s.logf("session.delete agent=%s", p.AgentID)
	return SessionDeleteResult{AgentID: p.AgentID, Deleted: true}, nil
}

// handleSessionFork branches a stored session at a run boundary.
//
// The fork is not opened here. The client opens it in a tab, which is the step
// that turns an id into a conversation — and a client forking from the sidebar
// has no tab to open it in yet, so it is returned rather than resumed.
func (s *Server) handleSessionFork(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionForkParams](params)
	if err != nil {
		return nil, errInvalidParams("session.fork: " + err.Error())
	}
	if p.AgentID == "" {
		return nil, errInvalidParams("session.fork: agent_id is required")
	}
	if p.RunID == "" {
		return nil, errInvalidParams("session.fork: run_id is required")
	}
	opts := anvil.ListOptions{Local: anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store}}
	newID, err := anvil.ForkAgent(ctx, p.AgentID, p.RunID, opts)
	if err != nil {
		return nil, errHost(err)
	}
	s.logf("session.fork agent=%s run=%s fork=%s", p.AgentID, p.RunID, newID)
	return SessionForkResult{AgentID: newID}, nil
}

// ---------------------------------------------------------------------------
// checkpoint.list / checkpoint.restore
// ---------------------------------------------------------------------------

// handleCheckpointList lists the runs of a session that captured pre-images.
//
// An empty list is the normal answer for a session whose runs changed nothing,
// and for one that ran before checkpoints existed; it is not an error. The
// client shows the restore action only when there is a run to restore.
func (s *Server) handleCheckpointList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	_ = ctx
	p, err := decode[CheckpointListParams](params)
	if err != nil {
		return nil, errInvalidParams("checkpoint.list: " + err.Error())
	}
	if p.AgentID == "" {
		return nil, errInvalidParams("checkpoint.list: agent_id is required")
	}
	runs, err := anvil.ListCheckpoints(s.cwdPath(), p.AgentID, anvil.CheckpointOptions{})
	if err != nil {
		return nil, errHost(err)
	}
	out := CheckpointListResult{Runs: make([]CheckpointRun, 0, len(runs))}
	for _, run := range runs {
		files := make([]CheckpointFile, 0, len(run.Files))
		for _, file := range run.Files {
			files = append(files, CheckpointFile{Path: file.Path, Existed: file.Existed, Size: file.Size})
		}
		out.Runs = append(out.Runs, CheckpointRun{
			RunID:     run.RunID,
			CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339),
			Files:     files,
		})
	}
	return out, nil
}

// handleCheckpointRestore puts one run's files back on disk.
//
// The SDK resolves every captured path through the workspace sandbox, so a
// manifest that names its way out is refused rather than followed. The result
// is what was actually written or deleted, not what was asked for: the client
// reports that rather than restating the manifest.
func (s *Server) handleCheckpointRestore(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	_ = ctx
	p, err := decode[CheckpointRestoreParams](params)
	if err != nil {
		return nil, errInvalidParams("checkpoint.restore: " + err.Error())
	}
	if p.AgentID == "" {
		return nil, errInvalidParams("checkpoint.restore: agent_id is required")
	}
	if p.RunID == "" {
		return nil, errInvalidParams("checkpoint.restore: run_id is required")
	}
	restored, err := anvil.RestoreCheckpoint(s.cwdPath(), p.AgentID, p.RunID, anvil.CheckpointOptions{})
	if err != nil {
		return nil, errHost(err)
	}
	out := CheckpointRestoreResult{RunID: p.RunID, Files: make([]RestoredFile, 0, len(restored))}
	for _, file := range restored {
		out.Files = append(out.Files, RestoredFile{
			Path:    file.Path,
			Deleted: file.Deleted,
			Skipped: file.Skipped,
			Reason:  file.Reason,
			Warning: file.Warning,
		})
	}
	s.logf("checkpoint.restore agent=%s run=%s files=%d", p.AgentID, p.RunID, len(out.Files))
	return out, nil
}

// handleSessionTranscript replays a stored conversation.
//
// It resolves the agent against the workspace root, the way session.list does,
// and not against the live sessions: a replay has to work for an agent this
// host has never opened — that is the case it is for, a client that has just
// come back with no session at all.
func (s *Server) handleSessionTranscript(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionTranscriptParams](params)
	if err != nil {
		return nil, errInvalidParams("session.transcript: " + err.Error())
	}
	if p.AgentID == "" {
		return nil, errInvalidParams("session.transcript: agent_id is required")
	}
	result, err := anvil.LoadTranscript(ctx, p.AgentID, anvil.TranscriptOptions{
		Local: anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		Limit: p.Limit,
	})
	if err != nil {
		return nil, errHost(err)
	}
	out := SessionTranscriptResult{
		Entries: make([]TranscriptEntry, 0, len(result.Entries)),
		Dropped: result.Dropped,
	}
	for _, in := range result.Entries {
		entry := TranscriptEntry{
			Kind:       in.Kind,
			RunID:      in.RunID,
			Role:       in.Role,
			Content:    in.Content,
			ToolCallID: in.ToolCallID,
			Name:       in.Name,
			Status:     in.Status,
			Steps:      in.Steps,
			Err:        in.Err,
			Code:       string(in.Code),
		}
		for _, tc := range in.ToolCalls {
			entry.ToolCalls = append(entry.ToolCalls, TranscriptCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
		entry.Usage = usageWire(in.Usage)
		out.Entries = append(out.Entries, entry)
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

// ---------------------------------------------------------------------------
// session.compact / session.context
// ---------------------------------------------------------------------------

// handleSessionCompact summarizes an idle session's older turns on request. It
// is the wire behind /compact.
//
// Refusing a busy session is the same rule prompt follows: compaction rewrites
// the message slice a run is reading. Agent.Compact refuses again under its own
// lock, so this check is about the answer — -32003 says "stop the run first",
// where a raw SDK error would not.
func (s *Server) handleSessionCompact(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionRefParams](params)
	if err != nil {
		return nil, errInvalidParams("session.compact: " + err.Error())
	}
	sess, ok := s.session(p.SessionID)
	if !ok {
		return nil, errCode(CodeNoSession, "unknown session: "+p.SessionID)
	}
	if _, runID := sess.activeRun(); runID != "" {
		return nil, errCode(CodeRunInProgress, "session already has an active run: "+runID)
	}
	agent := sess.sdk()
	if agent == nil {
		return nil, errCode(CodeNoSession, "session is not open: "+p.SessionID)
	}
	res, err := agent.Compact(ctx)
	if err != nil {
		return nil, errHost(err)
	}
	out := SessionCompactResult{
		SessionID:  sess.id,
		Compacted:  res.Compacted,
		Before:     res.Before,
		After:      res.After,
		Summarized: res.Summarized,
		Context:    contextInfo(agent.ContextUsage()),
	}
	if res.Compacted {
		// A manual compaction has no run, so it cannot ride the event stream —
		// every event there is keyed by run_id. It gets its own notification,
		// with the same inner shape, and the client draws the same divider.
		s.notify(NotifyCompact, EventParams{
			SessionID: sess.id,
			Type:      "compact",
			AgentID:   sess.agentIdentity(),
			Compact: &CompactInfo{
				Reason:     "manual",
				Before:     res.Before,
				After:      res.After,
				Summarized: res.Summarized,
				Window:     out.Context.WindowTokens,
			},
		})
	}
	s.logf("session.compact id=%s compacted=%v before=%d after=%d", sess.id, res.Compacted, res.Before, res.After)
	return out, nil
}

// handleSessionContext answers a live session's estimate against its declared
// budget, so a tab that has run nothing can still draw its meter.
func (s *Server) handleSessionContext(params json.RawMessage) (any, *RPCError) {
	p, err := decode[SessionRefParams](params)
	if err != nil {
		return nil, errInvalidParams("session.context: " + err.Error())
	}
	sess, ok := s.session(p.SessionID)
	if !ok {
		return nil, errCode(CodeNoSession, "unknown session: "+p.SessionID)
	}
	agent := sess.sdk()
	if agent == nil {
		return nil, errCode(CodeNoSession, "session is not open: "+p.SessionID)
	}
	return contextInfo(agent.ContextUsage()), nil
}

// drain pumps one run: SDK events out as `event` notifications, then the
// terminal `done`. It owns the run until endRun.
func (s *Server) drain(sess *session, run *anvil.Run) {
	runID := run.ID()
	// Run.Stream ignores its context; ranging to close is the documented contract.
	for ev := range run.Stream(context.Background()) {
		if p, ok := projectEvent(sess.id, runID, ev); ok {
			p.AgentID = sess.agentIdentity()
			s.notify(NotifyEvent, p)
		}
	}
	res, err := run.Wait(context.Background())
	// The terminal done is queued *before* the run is released, and the order is
	// load-bearing: endRun is what wakes waitRun, and the two callers that wait
	// (session.close, and Serve's teardown on shutdown) go straight from that
	// wake-up to closing the agent and, on the shutdown path, to flush — which
	// stops accepting messages and drops anything pushed after it. Released
	// first, a shutdown-cancelled run could therefore lose the very `done` the
	// client is waiting for. push never blocks, so there is nothing to deadlock.
	s.notify(NotifyDone, doneWithContext(sess, projectDone(sess.id, runID, res, err)))
	sess.endRun(run)
}

// doneWithContext appends the post-run estimate to a terminal envelope.
//
// It is read here, after Wait returned, because that is when the agent has
// folded the run — including any compaction it performed — into its history.
// A session whose agent is already gone (shutdown) sends no context rather than
// a zero, which a meter would draw as an empty conversation.
func doneWithContext(sess *session, done DoneParams) DoneParams {
	if agent := sess.sdk(); agent != nil {
		ci := contextInfo(agent.ContextUsage())
		done.Context = &ci
	}
	return done
}

// forwardSubagentEvent projects one child event onto the `event` notification
// with the identity that places it under its spawn card.
//
// It deliberately does not go through drain: the parent's run is blocked on the
// spawn call, and a background child's run is not the session's run at all.
// The session id is still the routing key — one session, one event stream —
// and parent_tool_call_id is what the panel groups by.
func (s *Server) forwardSubagentEvent(sess *session, ev anvil.SubagentEvent) {
	if _, ok := s.session(sess.id); !ok {
		// A closed session's children are cancelled with it; what they emit
		// while unwinding has no reader, and the client asked not to hear it.
		return
	}
	p, ok := projectEvent(sess.id, ev.RunID, ev.Event)
	if !ok {
		return
	}
	p.AgentID = ev.AgentID
	p.ParentToolCallID = ev.ToolCallID
	p.Depth = ev.Depth
	s.notify(NotifyEvent, p)
}

// forwardTask reports one background task changing state, so a panel and a
// status bar can follow the tree without polling. A task that ends after its
// session closed is dropped: the lookup fails, and there is no surface left to
// tell.
func (s *Server) forwardTask(sess *session, info anvil.TaskInfo) {
	if _, ok := s.session(sess.id); !ok {
		return
	}
	s.notify(NotifyTask, TaskNotification{SessionID: sess.id, Task: taskInfo(info)})
}

// taskInfo projects one public task snapshot onto the wire shape.
func taskInfo(t anvil.TaskInfo) TaskInfo {
	out := TaskInfo{
		ID:               t.ID,
		Kind:             string(t.Kind),
		Title:            t.Title,
		Status:           string(t.Status),
		AgentID:          t.AgentID,
		ChildAgentID:     t.ChildAgentID,
		ParentRunID:      t.ParentRunID,
		ParentToolCallID: t.ParentToolCallID,
		StartedAt:        t.StartedAt,
		ExitCode:         t.ExitCode,
	}
	if !t.EndedAt.IsZero() {
		ended := t.EndedAt
		out.EndedAt = &ended
	}
	return out
}

// ---------------------------------------------------------------------------
// tasks.list / tasks.output / tasks.cancel
// ---------------------------------------------------------------------------

// taskAgent resolves a session to the live agent that owns its task table.
// An unknown session and a session whose agent has closed are both "no
// session": the rows live on the agent, so asking after the close is asking
// about work nobody can list.
func (s *Server) taskAgent(sessionID string) (*anvil.Agent, *RPCError) {
	sess, ok := s.session(sessionID)
	if !ok {
		return nil, errCode(CodeNoSession, "unknown session: "+sessionID)
	}
	agent := sess.sdk()
	if agent == nil {
		return nil, errCode(CodeNoSession, "session is not open: "+sessionID)
	}
	return agent, nil
}

func (s *Server) handleTasksList(params json.RawMessage) (any, *RPCError) {
	p, err := decode[TasksListParams](params)
	if err != nil {
		return nil, errInvalidParams("tasks.list: " + err.Error())
	}
	agent, rpcErr := s.taskAgent(p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	infos := agent.Tasks()
	out := TasksListResult{Tasks: make([]TaskInfo, 0, len(infos))}
	for _, info := range infos {
		out.Tasks = append(out.Tasks, taskInfo(info))
	}
	return out, nil
}

func (s *Server) handleTasksOutput(params json.RawMessage) (any, *RPCError) {
	p, err := decode[TasksOutputParams](params)
	if err != nil {
		return nil, errInvalidParams("tasks.output: " + err.Error())
	}
	if strings.TrimSpace(p.TaskID) == "" {
		return nil, errInvalidParams("tasks.output: task_id is required")
	}
	agent, rpcErr := s.taskAgent(p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	info, err := agent.Task(p.TaskID)
	if err != nil {
		return nil, errCode(CodeNoTask, err.Error())
	}
	chunk, err := agent.TaskOutput(p.TaskID, p.Offset, p.Limit)
	if err != nil {
		return nil, errCode(CodeNoTask, err.Error())
	}
	return TasksOutputResult{
		Status:      string(info.Status),
		ExitCode:    info.ExitCode,
		Output:      chunk.Text,
		NextOffset:  int(chunk.NextOffset),
		FirstOffset: int(chunk.FirstOffset),
		Truncated:   chunk.Truncated,
	}, nil
}

func (s *Server) handleTasksCancel(params json.RawMessage) (any, *RPCError) {
	p, err := decode[TasksCancelParams](params)
	if err != nil {
		return nil, errInvalidParams("tasks.cancel: " + err.Error())
	}
	if strings.TrimSpace(p.TaskID) == "" {
		return nil, errInvalidParams("tasks.cancel: task_id is required")
	}
	agent, rpcErr := s.taskAgent(p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	info, err := agent.CancelTask(p.TaskID)
	if err != nil {
		return nil, errCode(CodeNoTask, err.Error())
	}
	return TasksCancelResult{Task: taskInfo(info)}, nil
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

func (s *Server) handleSubagentsGet(params json.RawMessage) (any, *RPCError) {
	if _, err := decode[struct{}](params); err != nil {
		return nil, errInvalidParams("subagents.get: " + err.Error())
	}
	return subagentsResult(anvil.LoadSubagentProfiles(s.cwdPath())), nil
}

func (s *Server) handleSubagentsUpsert(params json.RawMessage) (any, *RPCError) {
	p, err := decode[SubagentsUpsertParams](params)
	if err != nil {
		return nil, errInvalidParams("subagents.upsert: " + err.Error())
	}
	state, saveErr := anvil.UpsertSubagentProfile(s.cwdPath(), profileFromWire(p.Profile))
	if saveErr != nil {
		return nil, errInvalidParams("subagents.upsert: " + saveErr.Error())
	}
	return subagentsResult(state), nil
}

func (s *Server) handleSubagentsRemove(params json.RawMessage) (any, *RPCError) {
	p, err := decode[SubagentsRemoveParams](params)
	if err != nil {
		return nil, errInvalidParams("subagents.remove: " + err.Error())
	}
	if strings.TrimSpace(p.ID) == "" {
		return nil, errInvalidParams("subagents.remove: id is required")
	}
	state, saveErr := anvil.RemoveSubagentProfile(s.cwdPath(), p.ID)
	if saveErr != nil {
		return nil, errInvalidParams("subagents.remove: " + saveErr.Error())
	}
	return subagentsResult(state), nil
}

func profileFromWire(p SubagentProfileEntry) anvil.SubagentProfile {
	return anvil.SubagentProfile{
		ID: p.ID, Description: p.Description, Instructions: p.Instructions,
		Model: p.Model, Tools: p.Tools, MaxSteps: p.MaxSteps,
	}
}

func subagentsResult(state anvil.SubagentProfileState) SubagentsResult {
	result := SubagentsResult{
		Path: state.Path, Exists: state.Exists, Problem: state.Problem,
		Profiles: []SubagentProfileEntry{}, ToolGroups: anvil.SubagentToolGroups(),
		DefaultTools: []string{"read", "search", "skills", "git", "vision"},
	}
	for _, p := range state.Profiles {
		result.Profiles = append(result.Profiles, SubagentProfileEntry{
			ID: p.ID, Description: p.Description, Instructions: p.Instructions,
			Model: p.Model, Tools: p.Tools, MaxSteps: p.MaxSteps,
		})
	}
	return result
}

// ---------------------------------------------------------------------------
// skills / rules
// ---------------------------------------------------------------------------

// handleSkillsList reports the skill packages the workspace offers.
//
// The listing is wider than any one session's selection on purpose: an editor
// has to show what is available before a session exists. What a session
// actually injects is still only what it named, plus whatever .anvil/skills.json
// marks always-on.
func (s *Server) handleSkillsList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[SkillsParams](params)
	if err != nil {
		return nil, errInvalidParams("skills.list: " + err.Error())
	}
	found, err := anvil.ListSkills(ctx, anvil.SkillListOptions{
		CWD:       s.cwdPath(),
		SkillDirs: p.SkillDirs,
		ScanUser:  p.ScanUser,
	})
	if err != nil {
		return nil, errHost(err)
	}
	out := SkillsResult{Skills: make([]SkillEntry, 0, len(found))}
	for _, sk := range found {
		out.Skills = append(out.Skills, SkillEntry{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Path:        sk.Path,
			Always:      sk.Always,
		})
	}
	return out, nil
}

// handleRulesList reports which rule files apply, and how much of each one
// reached the prompt. It is the answer to "why is the model ignoring my
// AGENTS.md", which is otherwise invisible from the client side.
func (s *Server) handleRulesList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[RulesParams](params)
	if err != nil {
		return nil, errInvalidParams("rules.list: " + err.Error())
	}
	found, err := anvil.ListRuleFiles(ctx, anvil.RuleListOptions{
		CWD:         s.cwdPath(),
		LoadProject: p.LoadProject,
		LoadCursor:  p.LoadCursor,
	})
	if err != nil {
		return nil, errHost(err)
	}
	out := RulesResult{Files: make([]RuleFileInfo, 0, len(found))}
	for _, f := range found {
		out.Files = append(out.Files, RuleFileInfo{
			Path:      f.Path,
			Bytes:     f.Bytes,
			Included:  f.Included,
			Truncated: f.Truncated,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// permissions
// ---------------------------------------------------------------------------

// handlePermissionsGet reports the workspace rule file: where it is, whether it
// is there, what it says, and — when it will not compile — where it is broken.
//
// A malformed file is a result, not an error. The client's job with it is to
// open the file at the line and let the user fix it, which it cannot do with an
// error string alone; and the SDK's own refusal to run under a policy it cannot
// read stays where it belongs, in session.new.
func (s *Server) handlePermissionsGet(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	cwd := s.cwdPath()
	path := anvil.PermissionRulesPath(cwd)
	out := PermissionsResult{Path: path, Rules: []RuleInfo{}}

	file, err := anvil.LoadPermissionRuleFile(cwd)
	switch {
	case err != nil:
		out.Exists = true
		out.Problem = ruleProblem(err)
	case file != nil:
		out.Exists = true
		out.Rules = ruleInfos(file.Rules)
		// The declaration is reported as the host would read it, and a declaration
		// it will not honour is reported beside it in words. Filling DefaultMode
		// with "ask" for a file that declared bypass would read as if the file had
		// said ask, which is the one thing it plainly did not.
		if file.DefaultMode != "" {
			if honoured := honouredMode(file.DefaultMode); honoured != "" {
				out.DefaultMode = honoured
			} else {
				out.DefaultModeRefused = refusedMode(file.DefaultMode)
			}
		}
	}

	// The user layer is reported even when the workspace file is broken: the
	// manager is how a person fixes either one, and it needs to see both. The
	// layers are one list in merge order — user first — each rule carrying the
	// file it came from, because a position in the merged list names no line a
	// reader can open.
	if userPath, err := anvil.UserPermissionRulesPath(); err == nil {
		out.UserPath = userPath
		if user, err := anvil.LoadUserPermissionRuleFile(); err != nil {
			out.UserExists = true
			out.UserProblem = ruleProblem(err)
		} else if user != nil {
			out.UserExists = true
			out.Protected = user.Protected
			if len(user.Rules) > 0 {
				out.Rules = append(ruleInfos(user.Rules), out.Rules...)
			}
		}
	}
	return out, nil
}

// handlePermissionsAdd appends one rule the user asked for and reports what that
// did to the call they were looking at.
//
// The whole point of "always allow" is that it covers the call in front of you,
// so when the client names the pending approval the rule was written for, the
// host evaluates the file as it now stands against that very call and answers
// the approval itself — but only if an allow rule is what decides it. A rule
// that loses to a deny, or to an ask, is written all the same (it is a file the
// user asked to change) and reported as not having settled the call, because the
// alternative is a card that disappears while the policy says no.
func (s *Server) handlePermissionsAdd(_ context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[PermissionsAddParams](params)
	if err != nil {
		return nil, errInvalidParams("permissions.add: " + err.Error())
	}
	if strings.TrimSpace(p.Rule.Tool) == "" {
		return nil, errInvalidParams("permissions.add: rule.tool is required")
	}
	source, rpcErr := permissionSource(p.Source, "permissions.add")
	if rpcErr != nil {
		return nil, rpcErr
	}
	// The card's buttons are all "always allow", so an omitted effect means
	// allow. A client editing the file by hand sends the word it wants.
	effect := strings.TrimSpace(p.Rule.Effect)
	if effect == "" {
		effect = anvil.PermissionEffectAllow
	}
	var pending pendingRequest
	if p.RequestID != "" {
		var ok bool
		pending, ok = s.approvals.lookup(p.RequestID)
		if !ok {
			return nil, errCode(CodeNoApproval, errNoPendingApproval.Error())
		}
		sess, ok := s.session(pending.sessionID)
		if !ok {
			return nil, errCode(CodeNoSession, "unknown session: "+pending.sessionID)
		}
		if !sess.loadPermissions {
			return nil, errCode(CodeInvalidRequest,
				"permissions.add: this session was created with load_permissions off, so a rule written here would not apply to the call being approved")
		}
	}

	cwd := s.cwdPath()
	rule := anvil.PermissionRule{
		Effect:  effect,
		Tool:    p.Rule.Tool,
		Pattern: p.Rule.Pattern,
		Reason:  p.Rule.Reason,
	}
	var (
		rules []anvil.PermissionRule
		path  string
		add   = func() error {
			var err error
			if source == RuleSourceUser {
				path, err = anvil.UserPermissionRulesPath()
				if err != nil {
					return err
				}
				rules, err = anvil.AddUserPermissionRule(rule)
			} else {
				path = anvil.PermissionRulesPath(cwd)
				rules, err = anvil.AddPermissionRule(cwd, rule)
			}
			return err
		}
	)
	if err := add(); err != nil {
		var perr *anvil.PermissionRuleError
		if errors.As(err, &perr) {
			// Appending to a file that cannot be read is refused — that is the one
			// thing an append must never do — and the error says exactly where the
			// file is broken so the client can send the user there.
			return nil, errCode(CodeHostError, perr.Error())
		}
		return nil, errHost(err)
	}

	out := PermissionsAddResult{Path: path, Source: source, Rules: ruleInfos(rules)}
	// One-based, the number the appended rule is cited by in its own file.
	out.Index = len(rules)

	if p.RequestID == "" {
		return out, nil
	}
	// Which rule decides the call now is not necessarily the one just written,
	// so both files are read as they now stand and evaluated together. That is
	// the evaluation a session runs, protection included: a workspace allow does
	// not settle a call the user's deny refuses, and no allow settles a call that
	// touches a protected path.
	user, workspace := s.rulesForPending()
	if m := anvil.EvaluatePermissionRulesAcross(cwd, user, workspace, pending.call.tool, pending.call.args); m != nil {
		out.DecidedBy = ruleInfo(m)
		if m.Effect == anvil.PermissionEffectAllow {
			if err := s.approvals.answer(ApprovalResponseParams{
				RequestID: p.RequestID,
				Decision:  DecisionAllow,
			}); err != nil {
				return nil, errCode(CodeNoApproval, err.Error())
			}
			out.Approved = true
		}
	}
	return out, nil
}

// handlePermissionsSet replaces one rule file — the workspace's by default, the
// user's when the request says so — with the list a person approved in the
// manager.
//
// It is a replacement, not an append, so it makes the append's refusals again
// and adds one: every rule must compile, the declared mode must be one a project
// file may declare, and the file on disk must read — replacing a policy this
// build could not parse would discard the very text the user was told to go and
// fix. The write is whole-file and atomic, and the answer is the file read back
// the way permissions.get reads it, so the panel shows what a new session will
// open under rather than merely what it sent.
//
// The two shapes stay separate in both directions. The workspace layer owns
// default_mode and may not carry protected globs; the user layer owns protected
// globs and has no mode. A request that crosses them is refused rather than
// silently dropping the field it names.
func (s *Server) handlePermissionsSet(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[PermissionsSetParams](params)
	if err != nil {
		return nil, errInvalidParams("permissions.set: " + err.Error())
	}
	source, rpcErr := permissionSource(p.Source, "permissions.set")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if source == RuleSourceUser && strings.TrimSpace(p.DefaultMode) != "" {
		return nil, errInvalidParams("permissions.set: default_mode belongs to the workspace file; the user file has no starting mode")
	}
	if source == RuleSourceWorkspace && len(p.Protected) > 0 {
		return nil, errInvalidParams("permissions.set: protected belongs to the user file; a workspace may not declare protected paths")
	}
	rules := make([]anvil.PermissionRule, 0, len(p.Rules))
	for i, r := range p.Rules {
		tool := strings.TrimSpace(r.Tool)
		if tool == "" {
			return nil, errInvalidParams(fmt.Sprintf("permissions.set: rule %d: tool is required", i+1))
		}
		// A row the manager left on its default is an allow; a file written by hand
		// sends the word it wants either way.
		effect := strings.TrimSpace(r.Effect)
		if effect == "" {
			effect = anvil.PermissionEffectAllow
		}
		rules = append(rules, anvil.PermissionRule{
			Effect:  effect,
			Tool:    tool,
			Pattern: r.Pattern,
			Reason:  r.Reason,
		})
	}
	mode := strings.TrimSpace(p.DefaultMode)
	if mode != "" && honouredMode(mode) == "" {
		// One wording for "this declaration will not be honoured", whichever of
		// get, the session result or a save is asking. A save is refused in
		// advance; the sentence reads the same because the rule is the same.
		return nil, errInvalidParams("permissions.set: " + refusedMode(mode))
	}

	cwd := s.cwdPath()
	// Read before writing, both layers. The manager shows the problem and disables
	// Save in this state, so reaching here means a client skipped that step — and
	// the old file still gets the benefit of the doubt. A missing user file is the
	// one exception: it is what "create" is for.
	if source == RuleSourceUser {
		if _, err := anvil.LoadUserPermissionRuleFile(); err != nil {
			return nil, errCode(CodeInvalidRequest, "permissions.set: "+err.Error())
		}
		if err := anvil.SaveUserPermissionRuleFile(rules, p.Protected, p.Create); err != nil {
			var perr *anvil.PermissionRuleError
			if errors.As(err, &perr) {
				return nil, errInvalidParams("permissions.set: " + perr.Error())
			}
			return nil, errHost(err)
		}
		return s.handlePermissionsGet(ctx, nil)
	}
	if _, err := anvil.LoadPermissionRuleFile(cwd); err != nil {
		return nil, errCode(CodeInvalidRequest, "permissions.set: "+err.Error())
	}
	if err := anvil.SavePermissionRuleFile(cwd, rules, mode); err != nil {
		var perr *anvil.PermissionRuleError
		if errors.As(err, &perr) {
			return nil, errInvalidParams("permissions.set: " + perr.Error())
		}
		return nil, errHost(err)
	}
	return s.handlePermissionsGet(ctx, nil)
}

// rulesForPending reads both rule layers as they now stand. A layer that will
// not parse contributes nothing: the write just made it valid, so this only
// happens if somebody is editing the file at the same moment, and the pending
// card then stays for a person to answer.
func (s *Server) rulesForPending() (user, workspace []anvil.PermissionRule) {
	if file, err := anvil.LoadUserPermissionRuleFile(); err == nil && file != nil {
		user = file.Rules
	}
	if file, err := anvil.LoadPermissionRuleFile(s.cwdPath()); err == nil && file != nil {
		workspace = file.Rules
	}
	return user, workspace
}

// permissionSource resolves the layer a request names. Empty means the
// workspace, which is what a client that predates scopes means; anything else
// but the two names is refused rather than guessed.
func permissionSource(source, method string) (string, *RPCError) {
	switch strings.TrimSpace(source) {
	case "":
		return RuleSourceWorkspace, nil
	case RuleSourceWorkspace:
		return RuleSourceWorkspace, nil
	case RuleSourceUser:
		return RuleSourceUser, nil
	default:
		return "", errInvalidParams(method + ": source must be \"" + RuleSourceWorkspace + "\" or \"" + RuleSourceUser + "\"")
	}
}

// ruleInfos names the rules of a set for the wire: where each one sits in its own
// file, which file that is, and how the SDK cites it, in the order they decide.
func ruleInfos(rules []anvil.PermissionRule) []RuleInfo {
	out := make([]RuleInfo, 0, len(rules))
	described := anvil.DescribePermissionRules(rules)
	for i, r := range rules {
		// A rule read from a file carries its position there; one built by hand
		// without a source falls back to its place in this list.
		index := i + 1
		if r.Index > 0 {
			index = r.Index
		}
		info := RuleInfo{
			Index:  index,
			Source: r.Source,
			RuleSpec: RuleSpec{
				Effect:  r.Effect,
				Tool:    r.Tool,
				Pattern: r.Pattern,
				Reason:  r.Reason,
			},
		}
		if i < len(described) {
			info.Describe = described[i]
		}
		out = append(out, info)
	}
	return out
}

// ruleProblem converts a rule-file error into the wire shape, with the place
// when the parser knows one.
func ruleProblem(err error) *RuleProblem {
	var perr *anvil.PermissionRuleError
	if !errors.As(err, &perr) {
		return nil
	}
	return &RuleProblem{Message: perr.Message, Line: perr.Line, Column: perr.Column}
}

// ---------------------------------------------------------------------------
// option assembly
// ---------------------------------------------------------------------------

// buildOptions maps session params onto public SDK options.
//
// APIKey is intentionally never set here: the SDK reads ANVIL_API_KEY from the
// process environment, which the client populates from SecretStorage. The key
// does not cross the protocol.
func (s *Server) buildOptions(ctx context.Context, p SessionParams, sess *session) (anvil.Options, *RPCError) {
	groups := groupsForMode(sess.mode, p.PermissionGroups)
	// loadPermissions was resolved by the caller, before the mode, because the
	// mode resolution reads the same file. It is read here, not recomputed: two
	// computations of one flag is how the option and the mode start disagreeing
	// about whether this session has read the workspace's policy.
	opts := anvil.Options{
		PermissionGroups:  groups,
		Provider:          anvil.Provider(firstNonEmpty(p.Provider, s.cfg.Provider, os.Getenv("ANVIL_PROVIDER"))),
		BaseURL:           firstNonEmpty(p.BaseURL, s.cfg.BaseURL, os.Getenv("ANVIL_BASE_URL")),
		Effort:            anvil.Effort(p.Effort),
		SystemPrompt:      p.SystemPrompt,
		RequireFinishTask: true,
		MaxSteps:          p.MaxSteps,
		BuiltinTools:      p.BuiltinTools,
		DisallowedTools:   p.DisallowedTools,
		Skills:            p.Skills,
		Plugins:           p.Plugins,
		Local: anvil.LocalOptions{
			CWD:             s.cwdPath(),
			LoadRules:       p.LoadRules,
			LoadCursorRules: p.LoadCursorRules,
			// Both default to on: a policy file in the workspace the user just
			// opened is policy they wrote for it. A malformed file therefore fails
			// session.new with the path named, which is the intended trade.
			LoadHooks:       boolOr(p.LoadHooks, true),
			LoadPermissions: sess.loadPermissions,
			Store:           p.Store,
		},
		// Off unless the client asks: Vision.Enabled is what makes the SDK
		// accept an attachment, and enabling it also advertises read_image.
		Vision: anvil.VisionOptions{Enabled: p.Vision},
		// The host always keeps run pre-images: the panel offers "restore this
		// run" for every session it opens, and a session captured only sometimes
		// would offer an undo that silently does nothing. Empty fields are the
		// SDK defaults ({cwd}/.anvil/checkpoints, 100 runs, 30 days).
		Checkpoints: &anvil.CheckpointOptions{},
	}
	// Agent-in-agent is on for the editor unless the client sends subagents:
	// false. The two callbacks name the session rather than the agent: they are
	// installed before the agent exists, and every child in the tree — however
	// deep — reports through the same options.
	if boolOr(p.Subagents, true) {
		profileState := anvil.LoadSubagentProfiles(s.cwdPath())
		opts.Subagents = anvil.SubagentOptions{
			Enabled:  true,
			Profiles: profileState.Profiles,
			OnEvent:  func(ev anvil.SubagentEvent) { s.forwardSubagentEvent(sess, ev) },
			OnTask:   func(info anvil.TaskInfo) { s.forwardTask(sess, info) },
		}
	}
	if len(p.TestCommand) > 0 {
		if strings.TrimSpace(p.TestCommand[0]) == "" {
			return opts, errInvalidParams("test_command: command is required")
		}
		opts.Test = anvil.TestOptions{Command: p.TestCommand}
	}
	if len(p.BuildCommand) > 0 {
		if strings.TrimSpace(p.BuildCommand[0]) == "" {
			return opts, errInvalidParams("build_command: command is required")
		}
		opts.Build = anvil.BuildOptions{Command: p.BuildCommand}
	}
	// Language servers are the client's declaration. Required stays false: a
	// server that cannot start removes tools, it does not refuse the session.
	// The SDK still merges {cwd}/.anvil/lsp.json underneath, so this list adds
	// to what the workspace already declared.
	if p.LSP != nil && p.LSP.Enabled {
		servers := make([]anvil.LSPServerConfig, 0, len(p.LSP.Servers))
		for i, srv := range p.LSP.Servers {
			if strings.TrimSpace(srv.Command) == "" {
				return opts, errInvalidParams(fmt.Sprintf("lsp.servers[%d]: command is required", i))
			}
			if srv.Language == "" && srv.Pattern == "" {
				return opts, errInvalidParams(fmt.Sprintf("lsp.servers[%d]: language or pattern is required", i))
			}
			servers = append(servers, anvil.LSPServerConfig{
				Language: srv.Language,
				Pattern:  srv.Pattern,
				Command:  srv.Command,
				Args:     srv.Args,
			})
		}
		opts.LSP = anvil.LSPOptions{Enabled: true, Servers: servers}
	}
	if opts.Provider == "" {
		opts.Provider = anvil.ProviderOpenAI
	}
	model := firstNonEmpty(p.Model, s.cfg.Model, os.Getenv("ANVIL_MODEL"))
	if model == "" {
		return opts, errInvalidParams("model is required (session model, host config, or ANVIL_MODEL)")
	}
	opts.Model = anvil.Model{ID: model}
	sources, rpcErr := settingSourcesFrom(p.SettingSources)
	if rpcErr != nil {
		return opts, rpcErr
	}
	sources = canonicalSources(sources)
	opts.Local.SettingSources = sources
	// A session that reads a file layer gets the host's shared runtime for that
	// pair, connected before the agent is built so its first turn already has the
	// tools. A failure in Reload is deliberately ignored: an unreachable server is
	// a status the panel reports, not a reason to refuse the session.
	if len(sources) > 0 {
		h := s.mcpRuntimeFor(s.cwdPath(), sources)
		_ = h.reload(ctx)
		opts.MCPRuntime = h.rt
		sess.settingSources = sources
		sess.mcpKey = h.key
	}
	web, rpcErr := webSearchFrom(p.WebSearch)
	if rpcErr != nil {
		return opts, rpcErr
	}
	opts.WebSearch = web
	// The budget is the client's declaration. Absent keeps every field at zero,
	// which is "no window", which is exactly the behaviour before this field
	// existed — an older client cannot start compacting behind the user's back.
	if p.Context != nil {
		opts.Context = anvil.ContextOptions{
			WindowTokens:  p.Context.WindowTokens,
			AutoCompactAt: p.Context.AutoCompactAt,
			KeepRecent:    p.Context.KeepRecent,
		}
	}
	if sess.mode == ModePlan {
		// Plan mode removes the tools that can change the workspace, so the
		// model produces a plan instead of acting on it. The instruction that
		// goes with it is a prompt prefix; see handlePrompt.
		opts.DisallowedTools = append(append([]string(nil), opts.DisallowedTools...), planDisallowedTools...)
	}
	// Which callback a mode installs is decided with which calls it gates; see
	// permissionHookForMode and groupsForMode.
	opts.Permission = s.permissionHookForMode(sess.mode, sess, approvalTimeout(p.ApprovalTimeoutMS))
	return opts, nil
}

// settingSourcesFrom validates the wire's string list against the enum.
//
// buildOptions and mcp.list read the same client field, so they have to accept
// the same values: a source that a session would load but the listing refuses
// would make the card disagree with the conversation it describes.
func settingSourcesFrom(values []string) ([]anvil.SettingSource, *RPCError) {
	out := make([]anvil.SettingSource, 0, len(values))
	for _, src := range values {
		clean := strings.ToLower(strings.TrimSpace(src))
		switch clean {
		case string(anvil.SettingSourceProject), string(anvil.SettingSourceUser):
			out = append(out, anvil.SettingSource(clean))
		case "":
		default:
			return nil, errInvalidParams("unknown setting source: " + src)
		}
	}
	return out, nil
}

// webSearchKeyEnv is where the SDK looks for the search credential. It is a
// constant rather than a client field: which environment variable a credential
// lives in is part of the spawn contract, and a session that could rename it
// would be a session that could point the host at anything in its environment.
const webSearchKeyEnv = "ANVIL_WEB_SEARCH_API_KEY"

// webSearchFrom turns the session's request field into SDK options.
//
// Presence is the switch, so nil is off and the tools are never registered. An
// endpoint is required for the http provider here rather than left to
// session.new's SDK error: the message names the setting the user has to fill
// in, which the SDK's "BaseURL is required" does not.
func webSearchFrom(p *WebSearchParams) (anvil.WebSearchOptions, *RPCError) {
	if p == nil {
		return anvil.WebSearchOptions{}, nil
	}
	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	switch provider {
	case "", "http":
		if strings.TrimSpace(p.BaseURL) == "" {
			return anvil.WebSearchOptions{}, errInvalidParams("web_search.base_url is required when the provider is http")
		}
	case "mcp":
	default:
		return anvil.WebSearchOptions{}, errInvalidParams("unknown web_search.provider: " + p.Provider)
	}
	if p.MaxResults < 0 {
		return anvil.WebSearchOptions{}, errInvalidParams("web_search.max_results cannot be negative")
	}
	return anvil.WebSearchOptions{
		Enabled:    true,
		Provider:   provider,
		BaseURL:    p.BaseURL,
		APIKeyEnv:  webSearchKeyEnv,
		MaxResults: p.MaxResults,
		// The phase is search and fetch: a session that says yes to the web
		// tools gets both. AllowPrivateURLs stays false — a client setting is
		// not the place to relax a fetch policy.
		AllowFetchURL: true,
	}, nil
}

// boolOr reads a tri-state request field: absent means the default the host
// chose, and only an explicit false turns it off.
func boolOr(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
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

// permissionMode validates a requested mode.
//
// An empty request is not an error and not ModeAsk: it means the client has no
// opinion, and what fills that in is resolveMode, which knows about the
// workspace. A client that names a word the ladder does not have is a client
// bug, and it is refused rather than quietly downgraded to ask.
func permissionMode(raw string) (string, *RPCError) {
	clean := strings.ToLower(strings.TrimSpace(raw))
	if clean == "" {
		return "", nil
	}
	for _, m := range PermissionModes {
		if clean == m {
			return clean, nil
		}
	}
	return "", errInvalidParams("unknown permission_mode: " + raw)
}

// resolveMode decides which mode a new session runs in, and says where that came
// from.
//
//	the mode the client sent
//	else the one the workspace's .anvil/permissions.json declares
//	else ask
//
// The middle step is the whole point of the declaration: a client with no
// opinion sends nothing and does not have to know the file exists, so a team's
// "edits are fine here, nothing runs without a prompt" is committed rather than
// remembered per machine. It is skipped entirely when the session is not reading
// that file: a session with load_permissions off has read none of it, and its
// declaration no more than its rules.
//
// A declaration the host will not honour is refused, not obeyed and not
// silently dropped: bypass, because a mode that approves everything without
// showing it is a decision a person makes for a session rather than one a
// repository makes for whoever opens it; and any word the ladder does not have.
// Either way the session starts in ask and the refusal comes back in words. A
// workspace nobody can open because of one line in a file is the worse failure.
//
// A file that will not parse is not reported here. That refusal belongs to the
// SDK's own load, which fails session.new naming the file and the place; saying
// it twice, in two different words, is how the two start disagreeing.
func (s *Server) resolveMode(requested string, loadPermissions bool) (mode, source, refused string, rpcErr *RPCError) {
	if requested != "" {
		mode, rpcErr = permissionMode(requested)
		return mode, ModeSourceRequest, "", rpcErr
	}
	if loadPermissions {
		if file, err := anvil.LoadPermissionRuleFile(s.cwdPath()); err == nil && file != nil && file.DefaultMode != "" {
			if honouredMode(file.DefaultMode) != "" {
				return file.DefaultMode, ModeSourceFile, "", nil
			}
			return ModeAsk, ModeSourceDefault, refusedMode(file.DefaultMode), nil
		}
	}
	return ModeAsk, ModeSourceDefault, "", nil
}

// honouredMode is the declaration as the host will read it: the mode when the
// ladder has it and a project file may declare it, empty otherwise. It is what
// makes "the file says something the host will do" one question in one place, so
// the session result and permissions.get cannot answer it differently.
func honouredMode(declared string) string {
	if declared == "" || declared == ModeBypass {
		return ""
	}
	for _, m := range PermissionModes {
		if declared == m {
			return declared
		}
	}
	return ""
}

// refusedMode is the sentence for a declaration the host will not honour. One
// wording, one place: the session result and permissions.get describe the same
// refusal, and a client shows this rather than composing its own.
func refusedMode(declared string) string {
	if declared == ModeBypass {
		return `the workspace's .anvil/permissions.json declares defaultMode "bypass", which a project file may not grant: a mode that approves everything without showing it is a choice a person makes for a session, not one a repository makes for whoever opens it`
	}
	return fmt.Sprintf("the workspace's .anvil/permissions.json declares defaultMode %q, which is not a permission mode", declared)
}

// permissionHookForMode is the callback a mode installs, and it sits beside
// groupsForMode on purpose. A mode answers two questions — what to gate, and who
// answers when something is gated — and a mode added to one of those lists and
// forgotten in the other would quietly behave like ModeAsk. The default branch is
// the ordinary hook rather than a panic because that is also the honest reading of
// "this mode asks"; TestEveryModeResolvesToAHook is what keeps the two lists
// agreed.
func (s *Server) permissionHookForMode(mode string, sess *session, timeout time.Duration) anvil.PermissionFunc {
	switch mode {
	case ModeBypass:
		return s.bypassHook(sess, timeout)
	case ModeAuto:
		return s.autoHook(sess, timeout)
	case ModeDontAsk:
		return s.dontAskHook(sess)
	default:
		return s.permissionHook(sess, timeout)
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
		call := pendingCall{tool: req.ToolName, args: req.Args}
		p := s.approvals.begin(sess.id, runID, requestID, call, timeout)
		s.notify(NotifyApprovalRequest, ApprovalRequestParams{
			RequestID: requestID,
			SessionID: sess.id,
			RunID:     runID,
			Tool:      req.ToolName,
			Args:      req.Args,
			Preview:   approvalPreview(req.Preview),
			Decisions: []string{DecisionAllow, DecisionAllowSession, DecisionDeny},
			Rule:      ruleInfo(req.Rule),
			Protected: req.Protected,
			// Only offered when the session actually reads the rule file: a rule
			// written into a file this session ignores would be a button that lies.
			Remember:  s.rememberOptions(sess, call),
			TimeoutMS: int(timeout / time.Millisecond),
			// The child that asked, when one did. run_id stays the session's
			// active run, so session.cancel still withdraws the prompts that
			// belong to the turn; a background child asking while the session is
			// idle carries an empty run_id and is withdrawn by session.close or
			// by cancelling its task.
			AgentID: req.AgentID,
			Depth:   req.Depth,
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

// bypassHook answers yes to the mode's gate without showing anybody, and opens the
// card for a rule that asked for one.
//
// The two statements are not the same kind of thing. The mode is about everything
// a session might do; a rule is a written statement about one tool, and its "ask"
// effect has no other meaning than to interrupt. A blanket "do not ask me" is
// therefore no answer to `git push:*` in particular, so the narrower statement
// wins — which is also how Claude Code resolves it, and this feature is meant to
// behave like the tool people already know.
func (s *Server) bypassHook(sess *session, timeout time.Duration) anvil.PermissionFunc {
	ask := s.permissionHook(sess, timeout)
	return func(ctx context.Context, req anvil.PermissionRequest) (anvil.PermissionDecision, error) {
		if req.Rule != nil && req.Rule.Effect == anvil.PermissionEffectAsk {
			return ask(ctx, req)
		}
		return anvil.PermissionDecision{Allow: true}, nil
	}
}

// autoHook is what ModeAuto installs: it answers the calls the SDK can prove
// harmless, and asks about the rest.
//
// The classifier is anvil.AutoApproveCall, and it sits in the SDK for the same
// reason the rule matcher does: it reads the facts a rule is matched on, so the
// explanation for an auto-approved call and for a rule-approved call cannot
// disagree. A rule of effect "ask" is a written statement about this one tool,
// so it opens the card even here — the same precedence bypass uses. A rule of
// effect "allow" never reaches this function at all, because the registry
// decides it first, and a "deny" never reaches it either.
//
// Everything not proved harmless goes to the ordinary hook rather than a
// refusal: auto is a mode that skips some asking, not one that answers no.
func (s *Server) autoHook(sess *session, timeout time.Duration) anvil.PermissionFunc {
	ask := s.permissionHook(sess, timeout)
	return func(ctx context.Context, req anvil.PermissionRequest) (anvil.PermissionDecision, error) {
		if req.Rule != nil && req.Rule.Effect == anvil.PermissionEffectAsk {
			return ask(ctx, req)
		}
		if anvil.AutoApproveCall(req.ToolName, req.Args) {
			return anvil.PermissionDecision{Allow: true}, nil
		}
		return ask(ctx, req)
	}
}

// dontAskHook is what ModeDontAsk installs: the session has a channel to ask
// through, and its answer is always no.
//
// A callback that always refuses, rather than no callback at all, because
// Registry.Execute is explicit about what no callback means: *groups alone open no
// gate; with no PermissionFunc installed there is nobody to ask, and Execute keeps
// running every tool.* Absence is not refusal — it is bypass. Anything the
// workspace's rules allow never reaches this function at all, because a rule
// decides in the registry, before the callback, so what arrives here is exactly
// what nobody has pre-approved.
func (s *Server) dontAskHook(sess *session) anvil.PermissionFunc {
	return func(_ context.Context, req anvil.PermissionRequest) (anvil.PermissionDecision, error) {
		// "Always allow for this session" is a pre-approval a person gave, and a
		// mode is not allowed to overrule an answer somebody already made.
		if s.approvals.sessionAllowed(sess.id, req.ToolName, approvalScope(req.Preview)) {
			return anvil.PermissionDecision{Allow: true}, nil
		}
		return anvil.PermissionDecision{Allow: false, Message: dontAskMessage(req)}, nil
	}
}

// dontAskMessage tells the model what refused the call and what would change it.
//
// The model cannot see the mode, so a refusal that only said "denied" would leave
// it retrying the same call; and when a rule is what wanted a person, the message
// quotes the rule in the words the SDK uses everywhere else, so a refusal, a card
// and a rule listing all cite a rule the same way.
func dontAskMessage(req anvil.PermissionRequest) string {
	if req.Rule != nil {
		return req.ToolName + " denied: " + req.Rule.Describe() +
			" asks for confirmation and this session runs in dont-ask mode"
	}
	return req.ToolName + " denied: this session runs in dont-ask mode, and only calls the workspace's rules allow may run"
}

// approvalScope is what a session-wide approval is keyed on. The client sees it
// as preview.path and may echo it back narrowed; the host never interprets it.
func approvalScope(p anvil.ToolPreview) string {
	return p.Path
}

// rememberOptions is what an approval card may offer as "always allow" for this
// call.
//
// The choices come from the SDK, which proves each one against the call itself —
// a rule that would not decide it is not offered, including one covering a
// protected path, which an allow may never do. The host adds one condition of its
// own: nothing is offered by a session that does not read the rule files, because
// writing a rule there would change nothing about the call in front of the user.
func (s *Server) rememberOptions(sess *session, call pendingCall) []RememberOption {
	if !sess.loadPermissions {
		return nil
	}
	options := anvil.PermissionRuleOptionsIn(s.cwdPath(), call.tool, call.args)
	out := make([]RememberOption, 0, len(options))
	for _, o := range options {
		out = append(out, RememberOption{
			Kind: o.Kind,
			Rule: RuleSpec{Effect: o.Rule.Effect, Tool: o.Rule.Tool, Pattern: o.Rule.Pattern, Reason: o.Rule.Reason},
		})
	}
	return out
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
		SessionID:             sess.id,
		AgentID:               sess.sdk().AgentID(),
		CWD:                   opts.Local.CWD,
		Provider:              string(opts.Provider),
		Model:                 opts.Model.ID,
		PermissionMode:        sess.mode,
		PermissionModeSource:  sess.modeSource,
		PermissionModeRefused: sess.modeRefused,
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
	// The runtimes outlive individual sessions but not the host: they own the
	// processes every agent borrowed. Closed last, after the agents that call
	// into them are gone.
	s.closeMCPRuntimes()
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
