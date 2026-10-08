package llm

import (
	"context"
	"encoding/json"
)

// Role is a chat message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a provider-agnostic chat message.
type Message struct {
	Role       Role
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
	Name       string
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID    string
	Name  string
	Args  json.RawMessage
}

// ToolDef describes a tool schema for the model.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
}

// Request is one completion step.
type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolDef
}

// Usage is token usage when available.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Response is one completion step result.
type Response struct {
	Message Message
	Usage   *Usage
}

// Completer is the LLM adapter interface.
type Completer interface {
	Complete(ctx context.Context, req Request) (*Response, error)
}

// Delta is a streaming assistant text chunk.
type Delta struct {
	Text string
}

// StreamCompleter optionally streams assistant text while assembling the final Response.
type StreamCompleter interface {
	Completer
	CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error)
}

// Config shared by adapters.
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
}
