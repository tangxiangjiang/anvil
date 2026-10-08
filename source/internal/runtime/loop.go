package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/tools"
)

// Event mirrors public stream events without importing package anvil.
type Event struct {
	Type       string
	Text       string
	ToolCallID string
	Name       string
	Args       []byte
	Content    string
	IsError    bool
}

// Result is the terminal run result.
type Result struct {
	ID       string
	Status   string
	Result   string
	Steps    int
	Usage    *llm.Usage
	Err      string
	Messages []llm.Message // full conversation incl. system prompt
}

// Config for a single run.
type Config struct {
	CWD      string
	Model    string
	MaxSteps int
	History  []llm.Message
	UserMsg  string
	// RunID, if set, is used as the run identifier; otherwise one is generated.
	RunID string
	// HistoryMaxChars / HistoryKeepRecent enable optional compression (0.3+).
	HistoryMaxChars   int
	HistoryKeepRecent int
	// Sink, if set, receives each conversation message as it is appended
	// (user, assistant, tool) so callers can persist them durably.
	Sink func(llm.Message)
	// SystemPrompt overrides the default prompt.System(cwd) when non-empty.
	SystemPrompt string
}

// Emitter receives stream events.
type Emitter func(Event)

// Loop runs the tool-calling agent loop.
func Loop(ctx context.Context, completer llm.Completer, registry *tools.Registry, cfg Config, emit Emitter) Result {
	runID := cfg.RunID
	if runID == "" {
		runID = "run-" + id.New()
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 32
	}
	if emit == nil {
		emit = func(Event) {}
	}
	sink := cfg.Sink
	if sink == nil {
		sink = func(llm.Message) {}
	}

	messages := make([]llm.Message, 0, len(cfg.History)+2)
	hasSystem := false
	for _, m := range cfg.History {
		if m.Role == llm.RoleSystem {
			hasSystem = true
		}
		messages = append(messages, m)
	}
	if !hasSystem {
		sys := cfg.SystemPrompt
		if sys == "" {
			sys = prompt.System(cfg.CWD)
		}
		messages = append([]llm.Message{{Role: llm.RoleSystem, Content: sys}}, messages...)
	}
	user := llm.Message{Role: llm.RoleUser, Content: cfg.UserMsg}
	messages = append(messages, user)
	sink(user)

	if cfg.HistoryMaxChars > 0 {
		cr, err := compress.Apply(ctx, completer, cfg.Model, messages, compress.Options{
			MaxChars:   cfg.HistoryMaxChars,
			KeepRecent: cfg.HistoryKeepRecent,
		})
		if err == nil && cr.DidCompress {
			messages = cr.Messages
			// Persist the synthetic summary for observability; Resume re-compresses as needed.
			sink(llm.Message{Role: llm.RoleUser, Content: cr.Summary})
		}
	}

	var usageAcc *llm.Usage
	toolDefs := registry.Defs()

	for step := 0; step < cfg.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return cancelledResult(runID, step, usageAcc, messages, err, emit)
		}

		streamed := false
		var resp *llm.Response
		var err error
		req := llm.Request{Model: cfg.Model, Messages: messages, Tools: toolDefs}
		if sc, ok := completer.(llm.StreamCompleter); ok {
			resp, err = sc.CompleteStream(ctx, req, func(d llm.Delta) {
				if d.Text == "" {
					return
				}
				streamed = true
				emit(Event{Type: "assistant", Text: d.Text})
			})
		} else {
			resp, err = completer.Complete(ctx, req)
		}
		if err != nil {
			if ctx.Err() != nil {
				return cancelledResult(runID, step, usageAcc, messages, ctx.Err(), emit)
			}
			msg := err.Error()
			emit(Event{Type: "error", Text: msg})
			return Result{ID: runID, Status: "error", Steps: step, Usage: usageAcc, Err: msg, Messages: messages}
		}
		if resp.Usage != nil {
			if usageAcc == nil {
				usageAcc = &llm.Usage{}
			}
			usageAcc.InputTokens += resp.Usage.InputTokens
			usageAcc.OutputTokens += resp.Usage.OutputTokens
			usageAcc.TotalTokens += resp.Usage.TotalTokens
		}

		assistant := resp.Message
		if assistant.Role == "" {
			assistant.Role = llm.RoleAssistant
		}
		if assistant.Content != "" && !streamed {
			emit(Event{Type: "assistant", Text: assistant.Content})
		}
		messages = append(messages, assistant)
		sink(assistant)

		if len(assistant.ToolCalls) == 0 {
			emit(Event{Type: "done", Text: assistant.Content})
			return Result{
				ID:       runID,
				Status:   "finished",
				Result:   assistant.Content,
				Steps:    step + 1,
				Usage:    usageAcc,
				Messages: messages,
			}
		}

		for _, tc := range assistant.ToolCalls {
			emit(Event{
				Type:       "tool_call",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Args:       tc.Args,
			})
			res := registry.Execute(ctx, tc.Name, tc.Args)
			emit(Event{
				Type:       "tool_result",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    res.Content,
				IsError:    res.IsError,
			})
			toolMsg := llm.Message{
				Role:       llm.RoleTool,
				Content:    res.Content,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			}
			messages = append(messages, toolMsg)
			sink(toolMsg)
		}
	}

	msg := fmt.Sprintf("max steps exceeded (%d)", cfg.MaxSteps)
	emit(Event{Type: "error", Text: msg})
	return Result{ID: runID, Status: "error", Steps: cfg.MaxSteps, Usage: usageAcc, Err: msg, Messages: messages}
}

func cancelledResult(runID string, steps int, usage *llm.Usage, messages []llm.Message, err error, emit Emitter) Result {
	msg := "cancelled"
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "max wall time exceeded"
		} else {
			msg = err.Error()
		}
	}
	emit(Event{Type: "error", Text: msg})
	return Result{ID: runID, Status: "cancelled", Steps: steps, Usage: usage, Err: msg, Messages: messages}
}
