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
	Step       int // 1-based LLM round; set on EventProgress
	MaxSteps   int // configured limit; set on EventProgress
	Code       Code // set on EventError when classifiable (0.7+)
}
