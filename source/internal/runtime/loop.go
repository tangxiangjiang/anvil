// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/redact"
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
	Step       int
	MaxSteps   int // 0 = unlimited
	Code       string
	// Usage is what the run has consumed so far, set on the terminal event
	// (done or error) when the provider reported any counts. It is absent on
	// every other event: a token count is a fact about a completed exchange,
	// not a running estimate, and a per-chunk number would be invented.
	Usage *llm.Usage
	// Rule is the workspace rule that decided a tool call, on tool_result. It is
	// nil when no rule did — including when the call was allowed by a callback or
	// ran ungated.
	Rule *permissions.Decision
	// Items is the model's complete task list, on the `todo` event that follows
	// a successful todo_write call. It is the whole list rather than a change:
	// a client replaces what it shows, and an empty list clears it.
	Items []tools.TodoItem
}

// DefaultMaxSteps is deprecated: MaxSteps 0 means unlimited. Kept as 0 for callers.
const DefaultMaxSteps = 0

// MaxStepsExceededPrefix is the stable prefix of Result.Err when an optional step limit is hit.
const MaxStepsExceededPrefix = "max steps exceeded"

// reasoningFlushChars is how much streamed reasoning the loop accumulates before
// emitting it. It bounds the number of reasoning events a round can produce by
// size rather than by token rate, which matters because the run's event buffer
// drops silently when full and a dropped assistant chunk cannot be recovered.
const reasoningFlushChars = 200

// Result is the terminal run result.
type Result struct {
	ID       string
	Status   string
	Result   string
	Steps    int
	Usage    *llm.Usage
	Err      string
	Code     string
	Messages []llm.Message // full conversation incl. system prompt
}

// Config for a single run.
type Config struct {
	CWD      string
	Model    string
	MaxSteps int // 0 = unlimited (default); optional hard ceiling only
	History  []llm.Message
	UserMsg  string
	// UserParts are optional multimodal parts on the initial user message (0.19+).
	UserParts []llm.ContentPart
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
	// ParallelTools runs same-turn tool_calls concurrently when all are non-mutating (0.9+).
	ParallelTools bool
	// ParallelToolsMax bounds concurrent tool executions (0 = 4).
	ParallelToolsMax int
}

// Emitter receives stream events.
type Emitter func(Event)

// Loop runs the tool-calling agent loop.
// Convergence: model stops calling tools, loop guard fires, optional MaxSteps, or cancel.
func Loop(ctx context.Context, completer llm.Completer, registry *tools.Registry, cfg Config, emit Emitter) Result {
	runID := cfg.RunID
	if runID == "" {
		runID = "run-" + id.New()
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
	user := llm.Message{Role: llm.RoleUser, Content: cfg.UserMsg, Parts: cfg.UserParts}
	messages = append(messages, user)
	sink(user)

	// The provider rejects the whole request when an assistant turn's tool_calls
	// are not answered, and a stored history can reach that state without this
	// process ever having produced it: a run killed before its tool results
	// landed, or two runs interleaved into one store file. Healing here — before
	// the first request and before compression reads the history — is what keeps
	// one interruption from poisoning every later turn in the session.
	messages = HealToolPairs(messages)

	if cfg.HistoryMaxChars > 0 {
		cr, err := compress.Apply(ctx, completer, cfg.Model, messages, compress.Options{
			MaxChars:   cfg.HistoryMaxChars,
			KeepRecent: cfg.HistoryKeepRecent,
		})
		if err == nil && cr.DidCompress {
			messages = cr.Messages
			sink(llm.Message{Role: llm.RoleUser, Name: compress.SummaryMarker, Content: cr.Summary})
			for _, m := range cr.Recent {
				sink(m)
			}
		}
	}

	var usageAcc *llm.Usage
	toolDefs := registry.Defs()
	guard := newLoopGuard()

	for step := 0; ; step++ {
		if cfg.MaxSteps > 0 && step >= cfg.MaxSteps {
			msg := fmt.Sprintf("%s (%d)", MaxStepsExceededPrefix, cfg.MaxSteps)
			emit(Event{Type: "error", Text: msg, Code: CodeMaxSteps, Usage: usageAcc})
			return Result{ID: runID, Status: "error", Steps: cfg.MaxSteps, Usage: usageAcc, Err: msg, Code: CodeMaxSteps, Messages: messages}
		}
		if err := ctx.Err(); err != nil {
			return cancelledResult(ctx, runID, step, usageAcc, messages, err, emit)
		}

		emit(Event{Type: "progress", Step: step + 1, MaxSteps: cfg.MaxSteps})

		streamed := false
		var resp *llm.Response
		var err error
		req := llm.Request{Model: cfg.Model, Messages: messages, Tools: toolDefs}
		if sc, ok := completer.(llm.StreamCompleter); ok {
			// Reasoning is accumulated and flushed in batches rather than per
			// chunk. A reasoner emits several times the tokens of its answer,
			// and Run.emit drops events silently once its buffer fills -- a lost
			// assistant chunk is permanent, because the transcript is
			// append-only and never rebuilt. Bounding the reasoning volume at
			// the source is what keeps the answer's own chunks from being the
			// ones dropped. Text is never held back: it is what the user is
			// reading.
			var reasoning strings.Builder
			flushReasoning := func() {
				if reasoning.Len() == 0 {
					return
				}
				emit(Event{Type: "reasoning", Text: reasoning.String()})
				reasoning.Reset()
			}
			resp, err = sc.CompleteStream(ctx, req, func(d llm.Delta) {
				if d.Reasoning != "" {
					reasoning.WriteString(d.Reasoning)
					if reasoning.Len() >= reasoningFlushChars {
						flushReasoning()
					}
				}
				if d.Text == "" {
					return
				}
				// Reasoning goes first, so a collapsed Thinking block is never
				// rendered below the answer it explains. streamed stays keyed to
				// text alone: setting it for a reasoning delta would suppress the
				// non-streaming fallback below and lose the whole answer.
				flushReasoning()
				streamed = true
				emit(Event{Type: "assistant", Text: d.Text})
			})
			flushReasoning() // the round boundary
		} else {
			resp, err = completer.Complete(ctx, req)
		}
		if err != nil {
			if ctx.Err() != nil {
				return cancelledResult(ctx, runID, step, usageAcc, messages, ctx.Err(), emit)
			}
			msg := err.Error()
			emit(Event{Type: "error", Text: msg, Code: CodeProvider, Usage: usageAcc})
			return Result{ID: runID, Status: "error", Steps: step, Usage: usageAcc, Err: msg, Code: CodeProvider, Messages: messages}
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
			emit(Event{Type: "done", Text: assistant.Content, Usage: usageAcc})
			return Result{
				ID:       runID,
				Status:   "finished",
				Result:   assistant.Content,
				Steps:    step + 1,
				Usage:    usageAcc,
				Messages: messages,
			}
		}

		var results []tools.Result
		messages, results = runToolCalls(ctx, registry, assistant.ToolCalls, cfg, messages, sink, emit)
		if parts := registry.TakeVisionParts(); len(parts) > 0 {
			inj := llm.Message{
				Role:    llm.RoleUser,
				Content: "(attached images from read_image)",
				Parts:   parts,
			}
			messages = append(messages, inj)
			sink(inj)
		}

		switch action, reason := guard.Observe(assistant.ToolCalls, results); action {
		case loopWarn:
			inj := llm.Message{Role: llm.RoleUser, Content: loopWarnMessage(reason)}
			messages = append(messages, inj)
			sink(inj)
			emit(Event{Type: "assistant", Text: inj.Content, Code: CodeLoopDetected})
		case loopStop:
			msg := fmt.Sprintf("%s: %s", LoopDetectedPrefix, reason)
			emit(Event{Type: "error", Text: msg, Code: CodeLoopDetected, Usage: usageAcc})
			return Result{
				ID: runID, Status: "error", Steps: step + 1, Usage: usageAcc,
				Err: msg, Code: CodeLoopDetected, Messages: messages,
			}
		}
	}
}

func runToolCalls(
	ctx context.Context,
	registry *tools.Registry,
	calls []llm.ToolCall,
	cfg Config,
	messages []llm.Message,
	sink func(llm.Message),
	emit Emitter,
) ([]llm.Message, []tools.Result) {
	for _, tc := range calls {
		emit(Event{
			Type:       "tool_call",
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Args:       tc.Args,
		})
	}

	results := make([]tools.Result, len(calls))

	parallel := cfg.ParallelTools && len(calls) > 1 && !batchHasMutating(calls)
	if !parallel {
		for i, tc := range calls {
			results[i] = registry.Execute(ctx, tc.Name, tc.Args)
		}
	} else {
		maxConc := cfg.ParallelToolsMax
		if maxConc <= 0 {
			maxConc = 4
		}
		sem := make(chan struct{}, maxConc)
		var wg sync.WaitGroup
		for i, tc := range calls {
			i, tc := i, tc
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[i] = registry.Execute(ctx, tc.Name, tc.Args)
			}()
		}
		wg.Wait()
	}

	for i, tc := range calls {
		content := redact.String(results[i].Content)
		emit(Event{
			Type:       "tool_result",
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Content:    content,
			IsError:    results[i].IsError,
			// The rule travels with the result rather than with the call: which
			// rule decides a call is only known once it has been decided, and the
			// card it explains is the one keyed by this call id.
			Rule: results[i].Rule,
		})
		// A checklist is a statement about the session, not about this call, so
		// it is its own event rather than a field on the result: a client draws
		// one block, replaced on each call, not one block per card.
		if tc.Name == tools.TodoToolName && !results[i].IsError {
			emit(Event{Type: "todo", Items: results[i].Todos})
		}
		toolMsg := llm.Message{
			Role:       llm.RoleTool,
			Content:    content,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		}
		messages = append(messages, toolMsg)
		sink(toolMsg)
	}
	return messages, results
}

func batchHasMutating(calls []llm.ToolCall) bool {
	for _, tc := range calls {
		if tools.IsMutating(tc.Name) {
			return true
		}
	}
	return false
}

// IsMaxStepsExceeded reports whether errText is a MaxSteps limit failure.
func IsMaxStepsExceeded(errText string) bool {
	return strings.HasPrefix(errText, MaxStepsExceededPrefix)
}

// IsMaxWallTime reports whether errText is an Options.MaxWallTime failure.
func IsMaxWallTime(errText string) bool {
	return errText == ErrMaxWallTime.Error()
}

// IsContextDeadline reports whether errText is a parent context deadline failure.
func IsContextDeadline(errText string) bool {
	return errText == context.DeadlineExceeded.Error() || errText == "context deadline exceeded"
}

func cancelledResult(ctx context.Context, runID string, steps int, usage *llm.Usage, messages []llm.Message, err error, emit Emitter) Result {
	code, msg := classifyCancel(ctx, err)
	emit(Event{Type: "error", Text: msg, Code: code, Usage: usage})
	return Result{ID: runID, Status: "cancelled", Steps: steps, Usage: usage, Err: msg, Code: code, Messages: messages}
}

func classifyCancel(ctx context.Context, err error) (code, msg string) {
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, ErrMaxWallTime), errors.Is(err, ErrMaxWallTime):
		return CodeMaxWallTime, ErrMaxWallTime.Error()
	case errors.Is(cause, ErrCancelled):
		return CodeCancelled, ErrCancelled.Error()
	case errors.Is(cause, context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return CodeContextDeadline, context.DeadlineExceeded.Error()
	case errors.Is(cause, context.Canceled), errors.Is(err, context.Canceled):
		return CodeCancelled, ErrCancelled.Error()
	default:
		if err != nil {
			return CodeCancelled, err.Error()
		}
		return CodeCancelled, ErrCancelled.Error()
	}
}
