// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/hooks"
	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/mcp"
	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/rules"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/store"
	"github.com/anvil-dev/anvil/internal/tools"
)

// Agent is a durable local agent handle bound to a working directory.
type Agent struct {
	id     string
	opts   Options
	cwd    string
	closed bool

	store store.Backend

	mu           sync.Mutex
	running      bool
	activeRun    *Run
	history      []llm.Message
	complete     llm.Completer
	registry     *tools.Registry
	mcp          []*mcp.Session
	systemPrompt string
	hookFile     *hooks.File
}

// Create validates options and returns a new local agent (no LLM call).
func Create(ctx context.Context, opts Options) (*Agent, error) {
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	opts, err = mergeMCPFromSources(cwd, opts)
	if err != nil {
		return nil, err
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	reg, sessions, hookFile, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, newError("open store", false, err)
	}
	agentID := "agent-" + id.New()
	allowShell := opts.Local.AllowShellEnabled()
	if err := st.Append(agentID, store.Record{
		Type:       store.TypeMeta,
		AgentID:    agentID,
		CWD:        cwd,
		Provider:   string(opts.Provider),
		Model:      opts.Model.ID,
		MaxSteps:   opts.MaxSteps,
		AllowShell: &allowShell,
	}); err != nil {
		_ = st.Close()
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, newError("persist agent meta", false, err)
	}
	sysPrompt, err := buildSystemPrompt(cwd, opts)
	if err != nil {
		_ = st.Close()
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, newError("load project rules", false, err)
	}
	return &Agent{
		id:           agentID,
		opts:         opts,
		cwd:          cwd,
		store:        st,
		complete:     completer,
		registry:     reg,
		mcp:          sessions,
		history:      nil,
		systemPrompt: sysPrompt,
		hookFile:     hookFile,
	}, nil
}

// Prompt creates an agent, sends one message, waits, and closes.
func Prompt(ctx context.Context, message string, opts Options) (*RunResult, error) {
	agent, err := Create(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer agent.Close()

	run, err := agent.Send(ctx, message)
	if err != nil {
		return nil, err
	}
	return run.Wait(ctx)
}

// Resume loads a persisted agent by ID from the local store (jsonl or sqlite).
func Resume(ctx context.Context, agentID string, opts Options) (*Agent, error) {
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	opts, err = mergeMCPFromSources(cwd, opts)
	if err != nil {
		return nil, err
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	reg, sessions, hookFile, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, newError("open store", false, err)
	}
	records, err := st.Load(agentID)
	if err != nil {
		_ = st.Close()
		for _, s := range sessions {
			_ = s.Close()
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, newError("agent not found: "+agentID, false, nil)
		}
		return nil, newError("load agent store", false, err)
	}
	sysPrompt, err := buildSystemPrompt(cwd, opts)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, newError("load project rules", false, err)
	}
	return &Agent{
		id:           agentID,
		opts:         opts,
		cwd:          cwd,
		store:        st,
		complete:     completer,
		registry:     reg,
		mcp:          sessions,
		history:      historyFromRecords(records),
		systemPrompt: sysPrompt,
		hookFile:     hookFile,
	}, nil
}

func buildSystemPrompt(cwd string, opts Options) (string, error) {
	loadProject := opts.Local.LoadRules
	loadCursor := opts.Local.LoadCursorRules || opts.Local.HasSettingSource(SettingSourceProject)
	rulesText := ""
	if loadProject || loadCursor {
		var err error
		rulesText, err = rules.Load(cwd, rules.Options{
			LoadProject: loadProject,
			LoadCursor:  loadCursor,
		})
		if err != nil {
			return "", err
		}
	}
	if opts.SystemPrompt != "" {
		if rulesText == "" {
			return opts.SystemPrompt, nil
		}
		return opts.SystemPrompt + "\n\n# Project rules\n" + rulesText, nil
	}
	return prompt.SystemWithOptions(cwd, rulesText, prompt.Options{
		AllowShell: opts.Local.AllowShellEnabled(),
	}), nil
}

func buildRegistry(ctx context.Context, cwd string, opts Options) (*tools.Registry, []*mcp.Session, *hooks.File, error) {
	reg := tools.NewRegistry(cwd, opts.Local.AllowShellEnabled())

	var hookFile *hooks.File
	if opts.Local.HasSettingSource(SettingSourceProject) {
		hf, _, err := hooks.Load(cwd)
		if err != nil {
			return nil, nil, nil, newError("load hooks", false, err)
		}
		hookFile = hf
	}

	perm := composePermission(opts.Permission, hookFile)
	if perm != nil {
		reg.SetPermission(func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
			dec, err := perm(ctx, PermissionRequest{ToolName: name, Args: args})
			if err != nil {
				return nil, err
			}
			if !dec.Allow {
				msg := dec.Message
				if msg == "" {
					msg = "permission denied for tool " + name
				}
				return nil, fmt.Errorf("%s", msg)
			}
			if len(dec.Args) > 0 {
				return dec.Args, nil
			}
			return args, nil
		})
	}
	for _, spec := range opts.Tools {
		if spec.Handler == nil {
			return nil, nil, nil, newError("tool "+spec.Name+": handler is required", false, nil)
		}
		if err := reg.Register(&tools.FuncTool{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.Parameters,
			Handler:     spec.Handler,
		}); err != nil {
			return nil, nil, nil, newError(err.Error(), false, nil)
		}
	}
	var sessions []*mcp.Session
	for name, cfg := range opts.MCPServers {
		sess, err := mcp.Start(ctx, mcp.ServerConfig{
			Name:      name,
			Transport: cfg.Transport,
			Command:   cfg.Command,
			Args:      cfg.Args,
			Env:       cfg.Env,
			URL:       cfg.URL,
			Headers:   cfg.Headers,
		})
		if err != nil {
			for _, s := range sessions {
				_ = s.Close()
			}
			return nil, nil, nil, newError("mcp server "+name, false, err)
		}
		sessions = append(sessions, sess)
		for _, t := range sess.Tools() {
			toolName := "mcp_" + sanitizeName(name) + "_" + sanitizeName(t.Name)
			mcpName := t.Name
			session := sess
			if err := reg.Register(&tools.FuncTool{
				Name:        toolName,
				Description: fmt.Sprintf("[mcp:%s] %s", name, t.Description),
				Parameters:  t.InputSchema,
				Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
					return session.Call(ctx, mcpName, args)
				},
			}); err != nil {
				for _, s := range sessions {
					_ = s.Close()
				}
				return nil, nil, nil, newError(err.Error(), false, nil)
			}
		}
	}
	applyBuiltinToolFilters(reg, opts)
	return reg, sessions, hookFile, nil
}

func composePermission(user PermissionFunc, hookFile *hooks.File) PermissionFunc {
	if user == nil && hookFile == nil {
		return nil
	}
	return func(ctx context.Context, req PermissionRequest) (PermissionDecision, error) {
		if hookFile != nil {
			switch req.ToolName {
			case "shell":
				var in struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(req.Args, &in)
				if deny, msg := hookFile.CheckShell(in.Command); deny {
					return PermissionDecision{Allow: false, Message: msg}, nil
				}
			case "write_file", "edit_file", "apply_patch":
				if deny, msg := hookFile.CheckMutate(req.ToolName, string(req.Args)); deny {
					return PermissionDecision{Allow: false, Message: msg}, nil
				}
			}
		}
		if user != nil {
			return user(ctx, req)
		}
		return PermissionDecision{Allow: true}, nil
	}
}

func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "tool"
	}
	return string(out)
}

func historyFromRecords(records []store.Record) []llm.Message {
	lastSummary := -1
	for i, rec := range records {
		if rec.Type == store.TypeSummary {
			lastSummary = i
		}
	}
	var history []llm.Message
	if lastSummary >= 0 {
		history = append(history, llm.Message{
			Role:    llm.RoleUser,
			Name:    compress.SummaryMarker,
			Content: records[lastSummary].Content,
		})
	}
	start := 0
	if lastSummary >= 0 {
		start = lastSummary + 1
	}
	for _, rec := range records[start:] {
		if rec.Type != store.TypeMessage {
			continue
		}
		msg := llm.Message{
			Role:       llm.Role(rec.Role),
			Content:    rec.Content,
			ToolCallID: rec.ToolCallID,
			Name:       rec.Name,
		}
		for _, tc := range rec.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
		history = append(history, msg)
	}
	return history
}

// AgentID returns the agent identifier.
func (a *Agent) AgentID() string {
	if a == nil {
		return ""
	}
	return a.id
}

// Send starts a run for the user message.
func (a *Agent) Send(ctx context.Context, message string, opts ...SendOption) (*Run, error) {
	if a == nil {
		return nil, newError("nil agent", false, nil)
	}
	if a.closed {
		return nil, newError("agent is closed", false, nil)
	}
	cfg := sendConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.Force {
		a.mu.Lock()
		prev := a.activeRun
		a.mu.Unlock()
		if prev != nil {
			_ = prev.Cancel(ctx)
			_, _ = prev.Wait(ctx)
		}
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, newError("another run is already in progress", false, nil)
	}
	a.running = true
	historyCopy := append([]llm.Message(nil), a.history...)
	wall := a.opts.MaxWallTime
	runCtx, cancelCause := context.WithCancelCause(ctx)
	run := newRun("run-"+id.New(), cancelCause)
	a.activeRun = run
	a.mu.Unlock()

	go a.executeRun(runCtx, cancelCause, wall, run, message, historyCopy)
	return run, nil
}

// Reload re-reads file-based MCP, rules, and hooks without disposing the agent (0.10+).
// Inline MCPServers / Tools / SystemPrompt from the original Options are kept;
// file layers are refreshed. Fails if a run is in progress.
func (a *Agent) Reload(ctx context.Context) error {
	if a == nil {
		return newError("nil agent", false, nil)
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return newError("cannot reload while a run is in progress", false, nil)
	}
	opts := a.opts
	cwd := a.cwd
	oldSessions := a.mcp
	a.mu.Unlock()

	opts, err := mergeMCPFromSources(cwd, opts)
	if err != nil {
		return err
	}
	reg, sessions, hookFile, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return err
	}
	sysPrompt, err := buildSystemPrompt(cwd, opts)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return newError("load project rules", false, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		for _, s := range sessions {
			_ = s.Close()
		}
		return newError("cannot reload while a run is in progress", false, nil)
	}
	for _, s := range oldSessions {
		_ = s.Close()
	}
	a.opts = opts
	a.registry = reg
	a.mcp = sessions
	a.systemPrompt = sysPrompt
	a.hookFile = hookFile
	return nil
}

func (a *Agent) executeRun(ctx context.Context, cancelCause context.CancelCauseFunc, wall time.Duration, run *Run, message string, history []llm.Message) {
	defer func() {
		cancelCause(nil)
		a.mu.Lock()
		a.running = false
		if a.activeRun == run {
			a.activeRun = nil
		}
		a.mu.Unlock()
	}()

	loopCtx := ctx
	if wall > 0 {
		var wallCancel context.CancelFunc
		loopCtx, wallCancel = context.WithTimeoutCause(ctx, wall, runtime.ErrMaxWallTime)
		defer wallCancel()
	}

	a.persist(store.Record{Type: store.TypeRunStart, RunID: run.id})

	result := runtime.Loop(loopCtx, a.complete, a.registry, runtime.Config{
		CWD:               a.cwd,
		Model:             a.opts.Model.ID,
		MaxSteps:          a.opts.MaxSteps,
		History:           history,
		UserMsg:           message,
		RunID:             run.id,
		HistoryMaxChars:   a.opts.HistoryMaxChars,
		HistoryKeepRecent: a.opts.HistoryKeepRecent,
		SystemPrompt:      a.systemPrompt,
		ParallelTools:     a.opts.ParallelTools,
		ParallelToolsMax:  a.opts.ParallelToolsMax,
		Sink:              func(m llm.Message) { a.persistMessage(run.id, m) },
	}, func(ev runtime.Event) {
		run.emit(Event{
			Type:       EventType(ev.Type),
			Text:       ev.Text,
			ToolCallID: ev.ToolCallID,
			Name:       ev.Name,
			Args:       json.RawMessage(ev.Args),
			Content:    ev.Content,
			IsError:    ev.IsError,
			Step:       ev.Step,
			MaxSteps:   ev.MaxSteps,
			Code:       Code(ev.Code),
		})
	})

	a.mu.Lock()
	a.history = nonSystemMessages(result.Messages)
	a.mu.Unlock()

	rr := &RunResult{
		ID:     result.ID,
		Status: RunStatus(result.Status),
		Result: result.Result,
		Steps:  result.Steps,
		Err:    result.Err,
		Code:   Code(result.Code),
	}
	if result.Usage != nil {
		rr.Usage = &Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens,
		}
	}
	if rr.ID == "" {
		rr.ID = run.id
	} else {
		run.id = rr.ID
	}

	var usage *store.Usage
	if result.Usage != nil {
		usage = &store.Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens,
		}
	}
	a.persist(store.Record{
		Type:   store.TypeRunEnd,
		RunID:  rr.ID,
		Status: string(rr.Status),
		Steps:  rr.Steps,
		Usage:  usage,
	})

	var err error
	if rr.Status == StatusError {
		err = newError(rr.Err, false, nil)
	}
	run.finish(rr, err)
}

func (a *Agent) persistMessage(runID string, m llm.Message) {
	if m.Name == compress.SummaryMarker {
		a.persist(store.Record{
			Type:    store.TypeSummary,
			RunID:   runID,
			Role:    string(m.Role),
			Content: m.Content,
			Name:    m.Name,
		})
		return
	}
	rec := store.Record{
		Type:       store.TypeMessage,
		RunID:      runID,
		Role:       string(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	for _, tc := range m.ToolCalls {
		rec.ToolCalls = append(rec.ToolCalls, store.ToolCall{
			ID:   tc.ID,
			Name: tc.Name,
			Args: tc.Args,
		})
	}
	a.persist(rec)
}

func (a *Agent) persist(rec store.Record) {
	if a.store == nil {
		return
	}
	_ = a.store.Append(a.id, rec)
}

// Close releases agent resources including MCP servers and the store backend.
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
	for _, s := range a.mcp {
		_ = s.Close()
	}
	a.mcp = nil
	return nil
}

func nonSystemMessages(messages []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

func normalizeOptions(opts Options) (Options, string, error) {
	if opts.Provider == "" {
		opts.Provider = ProviderOpenAI
	}
	switch opts.Provider {
	case ProviderOpenAI, ProviderAnthropic:
	default:
		return opts, "", newError("unsupported provider: "+string(opts.Provider), false, nil)
	}
	if opts.APIKey == "" {
		opts.APIKey = os.Getenv("ANVIL_API_KEY")
	}
	if opts.Model.ID == "" {
		return opts, "", newError("model id is required", false, nil)
	}
	cwd := opts.Local.CWD
	if cwd == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return opts, "", newError("resolve cwd", false, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return opts, "", newError("cwd not accessible", false, err)
	}
	if !info.IsDir() {
		return opts, "", newError("cwd is not a directory", false, nil)
	}
	opts.Local.CWD = abs
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = runtime.DefaultMaxSteps
	}
	if opts.HistoryKeepRecent < 0 {
		opts.HistoryKeepRecent = 0
	}
	return opts, abs, nil
}

func newCompleter(opts Options) (llm.Completer, error) {
	cfg := llm.Config{
		APIKey:  opts.APIKey,
		BaseURL: opts.BaseURL,
		Model:   opts.Model.ID,
	}
	switch opts.Provider {
	case ProviderAnthropic:
		return llm.NewAnthropic(cfg), nil
	default:
		return llm.NewOpenAI(cfg), nil
	}
}
