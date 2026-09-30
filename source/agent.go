package anvil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

// Agent is a durable local agent handle bound to a working directory.
type Agent struct {
	id     string
	opts   Options
	cwd    string
	closed bool

	mu       sync.Mutex
	running  bool
	history  []llm.Message
	complete llm.Completer
	registry *tools.Registry
}

// Create validates options and returns a new local agent (no LLM call).
func Create(ctx context.Context, opts Options) (*Agent, error) {
	_ = ctx
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	return &Agent{
		id:       "agent-" + id.New(),
		opts:     opts,
		cwd:      cwd,
		complete: completer,
		registry: tools.NewRegistry(cwd, opts.Local.AllowShellEnabled()),
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

// Resume loads a persisted agent. Not implemented in 0.1.
func Resume(ctx context.Context, agentID string, opts Options) (*Agent, error) {
	_ = ctx
	_ = agentID
	_ = opts
	return nil, newError("Resume is not available until milestone 0.2", false, nil)
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

	result := runtime.Loop(ctx, a.complete, a.registry, runtime.Config{
		CWD:      a.cwd,
		Model:    a.opts.Model.ID,
		MaxSteps: a.opts.MaxSteps,
		History:  history,
		UserMsg:  message,
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

	// Append turn to in-memory history (user + final assistant text approximation).
	a.mu.Lock()
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: message})
	if result.Status == "finished" && result.Result != "" {
		a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: result.Result})
	}
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
	var err error
	if rr.Status == StatusError {
		err = newError(rr.Err, false, nil)
	}
	run.finish(rr, err)
}

// Close releases agent resources.
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return nil
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
