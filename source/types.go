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
	EventAssistant  EventType = "assistant"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventDone       EventType = "done"
	EventError      EventType = "error"
)

// Usage holds optional token counts from the provider.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// RunResult is the authoritative outcome of a Run (from Wait).
type RunResult struct {
	ID     string
	Status RunStatus
	Result string
	Steps  int
	Usage  *Usage
	Err    string
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
}
