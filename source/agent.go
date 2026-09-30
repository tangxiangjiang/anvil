package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/mcp"
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

	store *store.Store

	mu       sync.Mutex
	running  bool
	history  []llm.Message
	complete llm.Completer
	registry *tools.Registry
	mcp      []*mcp.Session
}

// Create validates options and returns a new local agent (no LLM call).
func Create(ctx context.Context, opts Options) (*Agent, error) {
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	reg, sessions, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, err
	}
	st := store.New(cwd)
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
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, newError("persist agent meta", false, err)
	}
	return &Agent{
		id:       agentID,
		opts:     opts,
		cwd:      cwd,
		store:    st,
		complete: completer,
		registry: reg,
		mcp:      sessions,
		history:  nil,
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

// Resume loads a persisted agent by ID from the local JSONL store.
func Resume(ctx context.Context, agentID string, opts Options) (*Agent, error) {
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	reg, sessions, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		return nil, err
	}
	st := store.New(cwd)
	records, err := st.Load(agentID)
	if err != nil {
		for _, s := range sessions {
			_ = s.Close()
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, newError("agent not found: "+agentID, false, nil)
		}
		return nil, newError("load agent store", false, err)
	}
	return &Agent{
		id:       agentID,
		opts:     opts,
		cwd:      cwd,
		store:    st,
		complete: completer,
		registry: reg,
		mcp:      sessions,
		history:  historyFromRecords(records),
	}, nil
}

func buildRegistry(ctx context.Context, cwd string, opts Options) (*tools.Registry, []*mcp.Session, error) {
	reg := tools.NewRegistry(cwd, opts.Local.AllowShellEnabled())
	if opts.Permission != nil {
		reg.SetPermission(func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
			dec, err := opts.Permission(ctx, PermissionRequest{ToolName: name, Args: args})
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
			return nil, nil, newError("tool "+spec.Name+": handler is required", false, nil)
		}
		if err := reg.Register(&tools.FuncTool{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.Parameters,
			Handler:     spec.Handler,
		}); err != nil {
			return nil, nil, newError(err.Error(), false, nil)
		}
	}
	var sessions []*mcp.Session
	for name, cfg := range opts.MCPServers {
		sess, err := mcp.Start(ctx, mcp.ServerConfig{
			Name:    name,
			Command: cfg.Command,
			Args:    cfg.Args,
			Env:     cfg.Env,
		})
		if err != nil {
			for _, s := range sessions {
				_ = s.Close()
			}
			return nil, nil, newError("mcp server "+name, false, err)
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
				return nil, nil, newError(err.Error(), false, nil)
			}
		}
	}
	return reg, sessions, nil
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
	var history []llm.Message
	for _, rec := range records {
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
	_ = opts
	if a == nil {
		return nil, newError("nil agent", false, nil)
	}
	if a.closed {
		return nil, newError("agent is closed", false, nil)
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, newError("another run is already in progress", false, nil)
	}
	a.running = true
	historyCopy := append([]llm.Message(nil), a.history...)
	a.mu.Unlock()

	run := newRun("run-" + id.New())
	go a.executeRun(ctx, run, message, historyCopy)
	return run, nil
}

func (a *Agent) executeRun(ctx context.Context, run *Run, message string, history []llm.Message) {
	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()

	a.persist(store.Record{Type: store.TypeRunStart, RunID: run.id})

	result := runtime.Loop(ctx, a.complete, a.registry, runtime.Config{
		CWD:               a.cwd,
		Model:             a.opts.Model.ID,
		MaxSteps:          a.opts.MaxSteps,
		History:           history,
		UserMsg:           message,
		RunID:             run.id,
		HistoryMaxChars:   a.opts.HistoryMaxChars,
		HistoryKeepRecent: a.opts.HistoryKeepRecent,
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

// Close releases agent resources including MCP servers.
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
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
		opts.MaxSteps = 32
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
