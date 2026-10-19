// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import "encoding/json"

// RunStatus is the terminal status of a Run.
type RunStatus string

const (
	StatusFinished  RunStatus = "finished"
	StatusError     RunStatus = "error"
	StatusCancelled RunStatus = "cancelled"
)

// EventType classifies streamed events.
type EventType string

const (
	EventProgress   EventType = "progress"
	EventAssistant  EventType = "assistant"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventDone       EventType = "done"
	EventError      EventType = "error"
	// EventReasoning carries the model's thinking, for providers that stream it.
	// It rides in Text, like EventAssistant, and is display-only: it is not part
	// of RunResult, is not stored, and has no replay.
	EventReasoning EventType = "reasoning"
	// EventTodo carries the model's complete task list (0.40.9), emitted when a
	// todo_write call succeeds. The call is also an ordinary tool message, so a
	// replayed conversation rebuilds the list from the stored arguments; this
	// event is what a live reader receives.
	EventTodo EventType = "todo"
)

// TodoItem is one row of the model's plan.
type TodoItem struct {
	Text   string
	Status string
}

// Task-list statuses. A todo_write call names one of these for every row.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoDone       = "done"
)

// Usage holds optional token counts from the provider.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Code is a stable machine-readable run failure class (0.7+).
type Code string

const (
	CodeMaxSteps        Code = "max_steps"
	CodeCancelled       Code = "cancelled"
	CodeContextDeadline Code = "context_deadline"
	CodeMaxWallTime     Code = "max_wall_time"
	CodeProvider        Code = "provider"
	CodeToolPanic       Code = "tool_panic"
)

// RunResult is the authoritative outcome of a Run (from Wait).
type RunResult struct {
	ID     string
	Status RunStatus
	Result string
	Steps  int
	Usage  *Usage
	Err    string
	Code   Code // set on error/cancelled when classifiable (0.7+)
}

// Event is one streamed unit from a Run.
type Event struct {
	Type       EventType
	Text       string
	ToolCallID string
	Name       string
	Args       json.RawMessage
	Content    string
	IsError    bool
	Step       int  // 1-based LLM round; set on EventProgress
	MaxSteps   int  // configured limit; set on EventProgress
	Code       Code // set on EventError when classifiable (0.7+)
	// Usage is the run's token usage so far, set on the terminal event (done or
	// error) when the provider reported any (0.40.8+). It is absent from every
	// other event and from a run whose provider sent no counts: nil means
	// "unknown", which a client must not draw as zero.
	Usage *Usage
	// Rule is the workspace rule that decided this call, set on EventToolResult
	// when one did (0.36+). It is nil for a call no rule decided — an ungated
	// read, a call the mode gate allowed untouched, or one a callback approved —
	// so a client shows "allowed by rule 2" only where a rule is really why.
	Rule *PermissionRuleMatch
	// Items is the model's task list, set on EventTodo (0.40.9) and nil on every
	// other event. It is the complete list the call wrote, not a change: a
	// client replaces its checklist, and an empty list means the call cleared it.
	Items []TodoItem
}
