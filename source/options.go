package anvil

import (
	"context"
	"encoding/json"
)

// Provider selects the LLM wire protocol.
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

// Options configures Create / Prompt / Resume.
type Options struct {
	Provider Provider
	APIKey   string
	BaseURL  string
	Model    Model
	Local    LocalOptions
	MaxSteps int // 0 = default (32)

	// Tools are caller-registered tools merged with built-ins (0.3+).
	Tools []ToolSpec

	// MCPServers are stdio MCP servers started on Create/Resume (0.3+).
	// Not restored from disk — pass again on Resume.
	MCPServers map[string]MCPServerConfig

	// Permission is invoked before mutating built-ins (write_file, edit_file, shell).
	Permission PermissionFunc

	// HistoryMaxChars enables context compression when history exceeds this size (0 = off).
	HistoryMaxChars int
	// HistoryKeepRecent is how many trailing messages to keep verbatim when compressing.
	// 0 defaults to 6.
	HistoryKeepRecent int
}

// Model identifies the model id for the selected provider.
type Model struct {
	ID string
}

// LocalOptions applies to the local runtime only.
type LocalOptions struct {
	CWD        string
	AllowShell *bool // nil = true (default)
}

// AllowShellEnabled returns whether shell tool is enabled.
func (o LocalOptions) AllowShellEnabled() bool {
	if o.AllowShell == nil {
		return true
	}
	return *o.AllowShell
}

// ToolHandler executes a custom tool. Return text content for the model.
type ToolHandler func(ctx context.Context, args json.RawMessage) (string, error)

// ToolSpec registers a custom tool.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
	Handler     ToolHandler
}

// MCPServerConfig launches a stdio MCP server.
type MCPServerConfig struct {
	Command string
	Args    []string
	Env     []string // KEY=VAL entries appended to the process environment
}

// PermissionFunc decides whether a mutating tool may run.
type PermissionFunc func(ctx context.Context, req PermissionRequest) (PermissionDecision, error)

// PermissionRequest describes a pending mutating tool call.
type PermissionRequest struct {
	ToolName string
	Args     json.RawMessage
}

// PermissionDecision is the hook outcome.
type PermissionDecision struct {
	Allow   bool
	Args    json.RawMessage // if non-nil, replaces tool args
	Message string          // deny reason shown to the model
}

// SendOption mutates per-send behavior (reserved).
type SendOption func(*sendConfig)

type sendConfig struct{}
