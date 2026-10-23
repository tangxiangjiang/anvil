// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	// Usage is authoritative on terminal events and an estimate on streamed
	// events when UsageEstimated is true.
	Usage *llm.Usage
	// UsageEstimated distinguishes the live request/chunk estimate from a
	// provider's terminal accounting.
	UsageEstimated bool
	// Rule is the workspace rule that decided a tool call, on tool_result. It is
	// nil when no rule did — including when the call was allowed by a callback or
	// ran ungated.
	Rule *permissions.Decision
	// Protected are the paths a tool call touched that an allow rule may not
	// cover, on tool_result (0.46+). It is a fact about the call, so it is set
	// even when no rule decided it.
	Protected []string
	// Items is the model's complete task list, on the `todo` event that follows
	// a successful todo_write call. It is the whole list rather than a change:
	// a client replaces what it shows, and an empty list clears it.
	Items []tools.TodoItem
	// Compact is what a history compaction folded away, on the `compact` event
	// (0.43+). It is nil on every other event. A client draws it as a divider;
	// the turns themselves are still in the store, so a replay shows them too.
	Compact *CompactInfo
	// LastRequestAt is when the most recent model request completed, on the
	// terminal event (0.43+). Zero when the run never reached the provider. A
	// client computes prompt-cache remaining life from it; the runtime does not
	// know any provider's TTL and does not guess one.
	LastRequestAt time.Time
}

// CompactInfo is one history compaction's numbers.
type CompactInfo struct {
	Reason     string // "auto" or "manual"
	Before     int    // estimated tokens before
	After      int    // estimated tokens after
	Summarized int    // messages folded into the summary
	Window     int    // the declared context window, 0 when none
}

// ContextBudget is the declared context window compaction works against.
type ContextBudget struct {
	WindowTokens  int     // 0 = no budget; compaction is off
	AutoCompactAt float64 // fraction of the window; 0 or out of range means 0.8
	KeepRecent    int     // messages kept verbatim; 0 means 8
}

// threshold is the estimated token count at which compaction triggers.
func (b ContextBudget) threshold() int {
	if b.WindowTokens <= 0 {
		return 0
	}
	at := b.AutoCompactAt
	if at <= 0 || at > 1 {
		at = 0.8
	}
	n := int(float64(b.WindowTokens) * at)
	if n < 1 {
		n = 1
	}
	return n
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

// EmptyResponseError is returned when a provider twice produces neither text
// nor tool calls. An empty turn is not a successful agent completion.
const EmptyResponseError = "model returned an empty response twice; no tool call or final answer was produced"

// UnproductiveResponseError is returned when a provider twice stops after
// announcing work it has not performed.
const UnproductiveResponseError = "model stopped after only announcing future work twice; no tool call or concrete result was produced"

const continueTaskPrompt = "The preceding assistant message only announced future work and did not perform it. Continue the user's task now. Use tools to inspect or modify the workspace as needed; do not stop at a plan or progress update."

// CompletionEvidenceError is returned when a requested side effect was never
// observed even though the model claimed to be finished.
const CompletionEvidenceError = "model claimed completion without performing the requested workspace operation"

// BackgroundDelegationError is returned when a parent agent twice tries to
// finish without joining and integrating the background subagents it started.
const BackgroundDelegationError = "model attempted to finish without completing the background subagent handoff"

type completionRequirement int

const (
	requireNoSideEffect completionRequirement = iota
	requireMutation
	requireCommit
)

type completionEvidence struct {
	mutation bool
	commit   bool
}

// backgroundComposition is the parent side of a background delegation. Starting
// a child is not completion: the parent must read every terminal result, then
// pass one explicit composition checkpoint before its final answer is accepted.
type backgroundComposition struct {
	tasks        map[string]bool // task id -> terminal output read
	checkpointed bool
}

func (b *backgroundComposition) observe(calls []llm.ToolCall, results []tools.Result) {
	if b.tasks == nil {
		b.tasks = make(map[string]bool)
	}
	for i, call := range calls {
		if i >= len(results) || results[i].IsError {
			continue
		}
		switch call.Name {
		case "spawn_agent":
			var in struct {
				Background bool `json:"background"`
			}
			if json.Unmarshal(call.Args, &in) != nil || !in.Background {
				continue
			}
			if fields := strings.Fields(results[i].Content); len(fields) > 0 {
				b.tasks[fields[0]] = false
				b.checkpointed = false
			}
		case "task_output":
			var in struct {
				TaskID string `json:"task_id"`
			}
			if json.Unmarshal(call.Args, &in) != nil {
				continue
			}
			if _, owned := b.tasks[in.TaskID]; !owned {
				continue
			}
			fields := strings.Fields(results[i].Content)
			if len(fields) >= 2 && fields[0] == in.TaskID && fields[1] != "running" {
				b.tasks[in.TaskID] = true
			}
		}
	}
}

func (b *backgroundComposition) pending() bool {
	for _, read := range b.tasks {
		if !read {
			return true
		}
	}
	return false
}

func (b *backgroundComposition) needsCheckpoint() bool {
	return len(b.tasks) > 0 && !b.pending() && !b.checkpointed
}

func completionRequirementFor(message string) completionRequirement {
	if strings.HasPrefix(strings.TrimSpace(message), "Plan mode is on.") {
		return requireNoSideEffect
	}
	text := strings.ToLower(strings.TrimSpace(visibleRequest(message)))
	for _, negated := range []string{"不要提交", "不需要提交", "先别提交", "do not commit", "don't commit", "without committing"} {
		if strings.Contains(text, negated) {
			return requireNoSideEffect
		}
	}
	commitRequested := strings.HasPrefix(text, "提交") ||
		strings.HasPrefix(text, "commit ") ||
		strings.Contains(text, "帮我提交") ||
		strings.Contains(text, "请提交") ||
		strings.Contains(text, "提交一下") ||
		strings.Contains(text, "提交下") ||
		(strings.Contains(text, "把") && strings.Contains(text, "提交")) ||
		strings.Contains(text, "please commit")
	if commitRequested {
		return requireCommit
	}
	for _, marker := range []string{
		"帮我修", "帮我改", "帮我加", "帮我删", "帮我实现", "帮我安装",
		"请修", "请改", "请加", "请删", "请实现", "请安装",
		"继续实现", "继续修改", "继续修复",
	} {
		if strings.Contains(text, marker) {
			return requireMutation
		}
	}
	if chineseMutationRequested(text) {
		return requireMutation
	}
	for _, marker := range []string{
		"please fix ", "please implement ", "please change ", "please update ",
		"can you fix ", "can you implement ", "can you change ", "can you update ",
	} {
		if strings.HasPrefix(text, marker) {
			return requireMutation
		}
	}
	return requireNoSideEffect
}

// chineseMutationRequested covers the short imperative phrasing commonly used
// in an ongoing Chinese conversation ("调一下", "这个改掉", "做成这样").
// Question-shaped requests are excluded unless they explicitly ask the agent
// for help, so "这个怎么改？" remains an explanation request.
func chineseMutationRequested(text string) bool {
	explicitRequest := false
	for _, cue := range []string{"帮我", "帮忙", "麻烦", "请你", "能不能", "可以帮", "给我", "替我"} {
		if strings.Contains(text, cue) {
			explicitRequest = true
			break
		}
	}
	if !explicitRequest {
		for _, cue := range []string{"为什么", "为何", "怎么", "如何", "什么原因", "是不是", "是否", "哪种", "哪个"} {
			if strings.Contains(text, cue) {
				return false
			}
		}
		trimmed := strings.TrimSpace(text)
		if strings.HasSuffix(trimmed, "？") || strings.HasSuffix(trimmed, "?") ||
			strings.HasSuffix(trimmed, "吗") || strings.HasSuffix(trimmed, "吗。") {
			return false
		}
	}
	for _, marker := range []string{
		"调一下", "调整一下", "调整下",
		"改一下", "改下", "改掉", "改成",
		"修一下", "修下", "修复一下", "修复下",
		"加一下", "加下", "加上", "补一下", "补下", "补上",
		"删一下", "删下", "删掉", "移除",
		"更新一下", "更新下", "实现一下", "实现下",
		"安装一下", "安装下", "做成", "换成",
		"需要修改", "需要改", "需要调整", "需要修复", "需要更新",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func visibleRequest(message string) string {
	if end := strings.LastIndex(message, "</context>\n\n"); end >= 0 {
		return message[end+len("</context>\n\n"):]
	}
	return message
}

func (r completionRequirement) satisfiedBy(e completionEvidence) bool {
	switch r {
	case requireCommit:
		return e.commit
	case requireMutation:
		return e.mutation
	default:
		return true
	}
}

func (r completionRequirement) correctivePrompt() string {
	switch r {
	case requireCommit:
		return "The user requested a git commit, but no successful git commit command has run. Continue now: inspect the changes, stage the intended files, create the commit, and verify it. Do not claim completion before the commit succeeds."
	case requireMutation:
		return "The user requested a workspace change, but no successful write or mutating command has run. Continue now and make the requested change with tools. Do not claim completion before a mutation succeeds."
	default:
		return continueTaskPrompt
	}
}

func observeCompletionEvidence(e *completionEvidence, calls []llm.ToolCall, results []tools.Result) {
	for i, call := range calls {
		if i >= len(results) || results[i].IsError {
			continue
		}
		switch call.Name {
		case "write_file", "edit_file", "apply_patch":
			e.mutation = true
		case "shell":
			command := shellCommand(call.Args)
			if shellPerformsCommit(command) {
				e.commit = true
				e.mutation = true
			} else if shellPerformsMutation(command) {
				e.mutation = true
			}
		}
	}
}

func shellCommand(args []byte) string {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(args, &in) != nil {
		return ""
	}
	return strings.ToLower(in.Command)
}

func shellPerformsCommit(command string) bool {
	fields := strings.Fields(command)
	for i, field := range fields {
		if strings.Trim(field, "&;|()\"'") != "git" {
			continue
		}
		for j := i + 1; j < len(fields) && j <= i+8; j++ {
			token := strings.Trim(fields[j], "&;|()\"'")
			if token == "commit" {
				return true
			}
			if token == "git" || token == "&&" || token == ";" || token == "|" {
				break
			}
		}
	}
	return false
}

func shellPerformsMutation(command string) bool {
	for _, marker := range []string{
		"git add ", "git mv ", "git rm ", "git restore ", "git checkout ",
		"mkdir ", "rmdir ", "rm ", "mv ", "cp ", "del ", "copy ", "move ",
		"set-content ", "add-content ", "new-item ", "remove-item ", "move-item ", "copy-item ",
		"npm install", "pnpm install", "yarn add", "pip install", "--install-extension",
	} {
		if strings.Contains(command, marker) {
			return true
		}
	}
	return false
}

// looksLikeDeferredAction catches short placeholder answers that promise an
// imminent tool action but end the request instead. Keep this deliberately
// narrow: ordinary explanations and completed-work summaries must remain valid
// final answers.
func looksLikeDeferredAction(content string) bool {
	text := strings.ToLower(strings.TrimSpace(content))
	if text == "" || len(text) > 800 || strings.Contains(text, "```") {
		return false
	}
	for _, completed := range []string{
		"已完成", "已经完成", "已修复", "已修改", "已提交",
		"completed", "finished", "fixed", "implemented", "committed",
	} {
		if strings.Contains(text, completed) {
			return false
		}
	}
	for _, marker := range []string{
		"我先确认", "我先检查", "我先查看", "我先读取", "我先核对", "我先梳理",
		"我会先确认", "我会先检查", "我会先查看", "我会先读取", "我会先核对",
		"我会按现有", "接下来我会", "下面我会", "现在我会",
		"i'll first inspect", "i'll first check", "i'll inspect", "i'll check",
		"i will first inspect", "i will first check", "i will inspect", "i will check",
		"let me inspect", "let me check", "let me read", "next i'll", "next i will",
	} {
		if index := strings.Index(text, marker); index >= 0 && index < 240 {
			return true
		}
	}
	// Cover ordinary future-tense action announcements without enumerating
	// every sentence ("我会排除…并提交", "I will update…"). The prefix must be
	// near the start and the verb must describe an operation, not advice.
	for _, future := range []string{"我会", "我将", "i'll", "i will", "we'll", "we will"} {
		index := strings.Index(text, future)
		if index < 0 || index >= 180 {
			continue
		}
		tail := text[index+len(future):]
		for _, action := range []string{
			"确认", "检查", "查看", "读取", "核对", "梳理", "排除", "提交",
			"修改", "实现", "处理", "修复", "运行", "执行", "创建", "删除",
			"更新", "添加", "写入", "构建", "测试",
			"inspect", "check", "read", "exclude", "commit", "modify", "implement",
			"fix", "run", "execute", "create", "delete", "update", "add", "write",
			"build", "test",
		} {
			if strings.Contains(tail, action) {
				return true
			}
		}
	}
	return false
}

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
	// LastRequestAt is when the most recent model request completed; zero when
	// none did.
	LastRequestAt time.Time
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
	// Context is the token budget (0.43+). When its window is set, the loop
	// compacts before the run and again between steps, keeping a tail that
	// starts at a user message. It composes with HistoryMaxChars: whichever
	// budget triggers first does the work.
	Context ContextBudget
	// PromptCache asks the adapter to place provider cache breakpoints (0.43+).
	// Only Anthropic acts on it.
	PromptCache bool
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
	messages = compactBudget(ctx, completer, cfg, messages, sink, emit)

	var usageAcc *llm.Usage
	var lastRequestAt time.Time
	toolDefs := registry.Defs()
	guard := newLoopGuard()
	retryReason := ""
	transientStart := -1
	requirement := completionRequirementFor(cfg.UserMsg)
	var evidence completionEvidence
	var background backgroundComposition
	completionCorrections := 0

	for step := 0; ; step++ {
		if cfg.MaxSteps > 0 && step >= cfg.MaxSteps {
			msg := fmt.Sprintf("%s (%d)", MaxStepsExceededPrefix, cfg.MaxSteps)
			emit(Event{Type: "error", Text: msg, Code: CodeMaxSteps, Usage: usageAcc, LastRequestAt: lastRequestAt})
			return Result{ID: runID, Status: "error", Steps: cfg.MaxSteps, Usage: usageAcc, Err: msg, Code: CodeMaxSteps, Messages: messages, LastRequestAt: lastRequestAt}
		}
		if err := ctx.Err(); err != nil {
			return cancelledResult(ctx, runID, step, usageAcc, messages, err, lastRequestAt, emit)
		}
		// A long tool-calling run crosses the budget between steps, not only at
		// its start. The check is before the request is built, so the estimate it
		// compares is exactly the one about to be sent. Step 0 is skipped: the
		// pre-loop check already ran on this exact slice, and repeating it can
		// only compact twice — the second summary would fold the first.
		if step > 0 && retryReason == "" {
			messages = compactBudget(ctx, completer, cfg, messages, sink, emit)
		}

		streamed := false
		var streamedText strings.Builder
		var resp *llm.Response
		var err error
		req := llm.Request{Model: cfg.Model, Messages: messages, Tools: toolDefs, PromptCache: cfg.PromptCache}
		roundInput := compress.Estimate(req.Messages)
		liveUsage := func(roundOutput int) *llm.Usage {
			u := &llm.Usage{InputTokens: roundInput, OutputTokens: roundOutput}
			if usageAcc != nil {
				u.InputTokens += usageAcc.InputTokens
				u.OutputTokens += usageAcc.OutputTokens
			}
			u.TotalTokens = u.InputTokens + u.OutputTokens
			return u
		}
		emit(Event{
			Type: "progress", Step: step + 1, MaxSteps: cfg.MaxSteps,
			Usage: liveUsage(0), UsageEstimated: true,
		})
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
			var streamedOutput strings.Builder
			flushReasoning := func() {
				if reasoning.Len() == 0 {
					return
				}
				emit(Event{
					Type: "reasoning", Text: reasoning.String(),
					Usage:          liveUsage(compress.EstimateText(streamedOutput.String())),
					UsageEstimated: true,
				})
				reasoning.Reset()
			}
			resp, err = sc.CompleteStream(ctx, req, func(d llm.Delta) {
				if d.Reasoning != "" {
					reasoning.WriteString(d.Reasoning)
					streamedOutput.WriteString(d.Reasoning)
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
				streamedText.WriteString(d.Text)
				streamedOutput.WriteString(d.Text)
				emit(Event{
					Type: "assistant", Text: d.Text,
					Usage:          liveUsage(compress.EstimateText(streamedOutput.String())),
					UsageEstimated: true,
				})
			})
			flushReasoning() // the round boundary
		} else {
			resp, err = completer.Complete(ctx, req)
		}
		// A corrective retry sees the rejected assistant turn and an internal
		// nudge, but neither belongs to the durable conversation or Result.
		if transientStart >= 0 {
			messages = messages[:transientStart]
			transientStart = -1
		}
		if err != nil {
			if ctx.Err() != nil {
				return cancelledResult(ctx, runID, step, usageAcc, messages, ctx.Err(), lastRequestAt, emit)
			}
			msg := err.Error()
			emit(Event{Type: "error", Text: msg, Code: CodeProvider, Usage: usageAcc, LastRequestAt: lastRequestAt})
			return Result{ID: runID, Status: "error", Steps: step, Usage: usageAcc, Err: msg, Code: CodeProvider, Messages: messages, LastRequestAt: lastRequestAt}
		}
		lastRequestAt = time.Now()
		if resp == nil {
			resp = &llm.Response{}
		}
		if resp.Usage != nil {
			if usageAcc == nil {
				usageAcc = &llm.Usage{}
			}
			usageAcc.InputTokens += resp.Usage.InputTokens
			usageAcc.OutputTokens += resp.Usage.OutputTokens
			usageAcc.TotalTokens += resp.Usage.TotalTokens
			usageAcc.CachedTokens += resp.Usage.CachedTokens
			usageAcc.CacheWriteTokens += resp.Usage.CacheWriteTokens
		}

		assistant := resp.Message
		if assistant.Role == "" {
			assistant.Role = llm.RoleAssistant
		}
		// Some streaming adapters can deliver text chunks but return an empty
		// assembled message. Preserve what the user already saw so persistence
		// and the terminal result agree with the stream.
		if assistant.Content == "" && streamedText.Len() > 0 {
			assistant.Content = streamedText.String()
		}
		invalidReason := ""
		invalidError := ""
		correctionPrompt := continueTaskPrompt
		if len(assistant.ToolCalls) == 0 {
			switch {
			case strings.TrimSpace(assistant.Content) == "":
				invalidReason = "empty"
				invalidError = EmptyResponseError
			case background.pending():
				invalidReason = "background"
				invalidError = BackgroundDelegationError
				correctionPrompt = "Background subagents started by this parent are still running or their terminal output has not been read. Continue the parent task: use task_output to join every delegated task, then integrate those results. Starting a subagent is not completion."
			case background.needsCheckpoint():
				invalidReason = "composition"
				invalidError = BackgroundDelegationError
				correctionPrompt = "All background subagent results are now available. Re-evaluate the original user request against those results. If work remains, continue with tools now; otherwise return a concrete final answer describing the completed result. Do not answer with a plan for future work."
			case !requirement.satisfiedBy(evidence):
				invalidReason = "evidence"
				invalidError = CompletionEvidenceError
				correctionPrompt = requirement.correctivePrompt()
			case looksLikeDeferredAction(assistant.Content):
				invalidReason = "deferred"
				invalidError = UnproductiveResponseError
			}
		}
		if invalidReason != "" {
			canCorrect := retryReason == ""
			if invalidReason == "evidence" && completionCorrections >= 1 {
				canCorrect = false
			}
			if canCorrect {
				// Keep the rejected turn and corrective instruction in this one
				// provider request only. They are deliberately not sent to sink.
				transientStart = len(messages)
				if strings.TrimSpace(assistant.Content) != "" {
					messages = append(messages, assistant)
				}
				messages = append(messages, llm.Message{Role: llm.RoleUser, Content: correctionPrompt})
				retryReason = invalidReason
				if invalidReason == "evidence" {
					completionCorrections++
				}
				if invalidReason == "composition" {
					background.checkpointed = true
				}
				step--
				continue
			}
			emit(Event{Type: "error", Text: invalidError, Code: CodeProvider, Usage: usageAcc, LastRequestAt: lastRequestAt})
			return Result{
				ID: runID, Status: "error", Steps: step + 1, Usage: usageAcc,
				Err: invalidError, Code: CodeProvider, Messages: messages, LastRequestAt: lastRequestAt,
			}
		}
		retryReason = ""
		if assistant.Content != "" && !streamed {
			emit(Event{Type: "assistant", Text: assistant.Content})
		}
		messages = append(messages, assistant)
		sink(assistant)

		if len(assistant.ToolCalls) == 0 {
			emit(Event{Type: "done", Text: assistant.Content, Usage: usageAcc, LastRequestAt: lastRequestAt})
			return Result{
				ID:            runID,
				Status:        "finished",
				Result:        assistant.Content,
				Steps:         step + 1,
				Usage:         usageAcc,
				Messages:      messages,
				LastRequestAt: lastRequestAt,
			}
		}

		var results []tools.Result
		messages, results = runToolCalls(ctx, registry, assistant.ToolCalls, cfg, messages, sink, emit)
		observeCompletionEvidence(&evidence, assistant.ToolCalls, results)
		background.observe(assistant.ToolCalls, results)
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
			emit(Event{Type: "error", Text: msg, Code: CodeLoopDetected, Usage: usageAcc, LastRequestAt: lastRequestAt})
			return Result{
				ID: runID, Status: "error", Steps: step + 1, Usage: usageAcc,
				Err: msg, Code: CodeLoopDetected, Messages: messages, LastRequestAt: lastRequestAt,
			}
		}
	}
}

// compactBudget folds older turns into a summary when the estimate crosses the
// declared budget.
//
// It returns messages unchanged when there is no window, when the estimate is
// below the threshold, or when there is nothing old enough to summarize. On a
// compaction it persists the summary marker and the kept tail through the same
// sink every other message uses, so Resume sees exactly one boundary, and emits
// one event so a live reader can draw it.
func compactBudget(ctx context.Context, completer llm.Completer, cfg Config, messages []llm.Message, sink func(llm.Message), emit Emitter) []llm.Message {
	threshold := cfg.Context.threshold()
	if threshold <= 0 {
		return messages
	}
	cr, err := compress.Compact(ctx, completer, cfg.Model, messages, compress.BudgetOptions{
		MaxTokens:  threshold,
		KeepRecent: cfg.Context.KeepRecent,
	})
	if err != nil || !cr.DidCompress {
		return messages
	}
	sink(llm.Message{Role: llm.RoleUser, Name: compress.SummaryMarker, Content: cr.Summary})
	for _, m := range cr.Recent {
		sink(m)
	}
	emit(Event{Type: "compact", Compact: &CompactInfo{
		Reason:     "auto",
		Before:     cr.Before,
		After:      cr.After,
		Summarized: cr.Summarized,
		Window:     cfg.Context.WindowTokens,
	}})
	return cr.Messages
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
			results[i] = registry.Execute(tools.WithCallID(ctx, tc.ID), tc.Name, tc.Args)
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
				results[i] = registry.Execute(tools.WithCallID(ctx, tc.ID), tc.Name, tc.Args)
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
			Rule:      results[i].Rule,
			Protected: results[i].Protected,
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

func cancelledResult(ctx context.Context, runID string, steps int, usage *llm.Usage, messages []llm.Message, err error, lastRequestAt time.Time, emit Emitter) Result {
	code, msg := classifyCancel(ctx, err)
	emit(Event{Type: "error", Text: msg, Code: code, Usage: usage, LastRequestAt: lastRequestAt})
	return Result{ID: runID, Status: "cancelled", Steps: steps, Usage: usage, Err: msg, Code: code, Messages: messages, LastRequestAt: lastRequestAt}
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
