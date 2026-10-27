// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

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
	Role    Role
	Content string
	// Parts are optional multimodal content parts (0.19+). When non-empty, providers
	// prefer Parts over Content for user messages (Content may still hold plain text).
	Parts      []ContentPart
	ToolCallID string
	ToolCalls  []ToolCall
	Name       string
}

// ContentPart is a multimodal message part.
type ContentPart struct {
	Type   string // "text" | "image"
	Text   string
	MIME   string // image/*
	Data   []byte // raw image bytes (not logged)
	Detail string // openai detail hint: low|high|auto
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
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
	// PromptCache asks the adapter to place cache breakpoints where the provider
	// supports them. Only the Anthropic adapter acts on it; OpenAI-compatible
	// providers cache on their own and ignore the flag.
	PromptCache bool
	// Effort is how hard the model is asked to think. Empty sends nothing, so the
	// request is exactly what it was before effort existed. It is a word, not a
	// number: OpenAI-compatible providers take a level, Anthropic takes a token
	// budget, and each adapter converts. It is never the model's own thinking text.
	Effort Effort `json:",omitempty"`
}

// Effort is how hard a model is asked to think before it answers.
type Effort string

const (
	EffortDefault Effort = ""
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
)

// Valid reports whether e is one of the named levels or empty.
func (e Effort) Valid() bool {
	switch e {
	case EffortDefault, EffortLow, EffortMedium, EffortHigh:
		return true
	}
	return false
}

// anthropicThinking is the Anthropic spelling of an effort level. The budgets are
// the adapter's, not the caller's: they live here so one place defines them.
func anthropicThinking(e Effort) (budget int, ok bool) {
	switch e {
	case EffortLow:
		return 1024, true
	case EffortMedium:
		return 4096, true
	case EffortHigh:
		return 16384, true
	}
	return 0, false
}

// anthropicMaxTokens is the output limit sent with a request. The provider needs
// it to exceed the thinking budget, and an answer keeps the room it had without
// thinking, so a budget raises the limit by the same fixed amount.
func anthropicMaxTokens(e Effort) int {
	const base = 8192
	if budget, ok := anthropicThinking(e); ok && budget >= base {
		return budget + base
	}
	return base
}

// Usage is token usage when available.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	// CachedTokens is how many input tokens the provider served from a prompt
	// cache. Zero means the provider said nothing — not that nothing was cached:
	// an older endpoint or a gateway may simply omit the field.
	CachedTokens int
	// CacheWriteTokens is how many input tokens the provider wrote into a cache
	// (Anthropic only). Zero elsewhere.
	CacheWriteTokens int
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

// Delta is a streaming assistant chunk. At most one provider format fills both
// fields, but a delta carrying both text and reasoning is legal and the runtime
// emits both.
type Delta struct {
	Text string
	// Reasoning is the model's thinking, for providers that stream one. It is
	// display-only: it is not accumulated into Response.Message, not stored, and
	// never sent back in a request.
	Reasoning string
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
