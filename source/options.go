// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"time"
)

// Provider selects the LLM wire protocol.
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

// SettingSource selects file-based config layers (Cursor SDK local settingSources).
type SettingSource string

const (
	SettingSourceProject SettingSource = "project" // {cwd}/.cursor/…
	SettingSourceUser    SettingSource = "user"    // ~/.cursor/…
)

// Options configures Create / Prompt / Resume.
type Options struct {
	Provider Provider
	APIKey   string
	BaseURL  string
	Model    Model
	Local    LocalOptions
	MaxSteps int // 0 = default (128)
	// MaxWallTime bounds a single Send/Prompt run. Zero means no wall-time limit.
	// When exceeded, the run ends with StatusCancelled and CodeMaxWallTime.
	// Parent context deadline → CodeContextDeadline; explicit Cancel → CodeCancelled.
	// MaxSteps still applies independently (whichever hits first wins).
	MaxWallTime time.Duration

	// Tools are caller-registered tools merged with built-ins (0.3+).
	Tools []ToolSpec

	// MCPServers are inline MCP servers (0.3+). Not restored from disk — pass again on Resume.
	// When SettingSources load file MCP, inline entries override same-named file servers.
	MCPServers map[string]MCPServerConfig

	// Permission is invoked before mutating built-ins (write_file, edit_file, shell).
	Permission PermissionFunc

	// HistoryMaxChars enables context compression when history exceeds this size (0 = off).
	HistoryMaxChars int
	// HistoryKeepRecent is how many trailing messages to keep verbatim when compressing.
	// 0 defaults to 6.
	HistoryKeepRecent int

	// ParallelTools runs same-turn tool_calls concurrently when all are non-mutating (0.9+).
	ParallelTools bool
	// ParallelToolsMax bounds concurrent tool executions when ParallelTools is on (0 = 4).
	ParallelToolsMax int

	// SystemPrompt replaces Anvil's default main system text when non-empty (0.10+).
	// Project rules (when enabled) are still appended. Not persisted across Resume unless passed again.
	SystemPrompt string

	// BuiltinTools, when non-empty, is an allow-list of built-in tool names or groups (0.10+).
	// Groups: shell, read, write, edit, search, skills, mcp. Custom Tools entries are never filtered out.
	BuiltinTools []string
	// DisallowedTools denies built-in names or groups after allow-list (0.10+).
	DisallowedTools []string

	// EmbedBaseURL is an OpenAI-compatible root (…/v1) for /embeddings (0.13+).
	// Empty: lexical-only semantic_search (and index build skips vectors unless CLI flags set).
	EmbedBaseURL string
	// EmbedModel is the embedding model id when EmbedBaseURL is set.
	EmbedModel string
	// EmbedAPIKey overrides APIKey for embeddings when non-empty.
	EmbedAPIKey string

	// AllowHookCommands enables hooks.json `command` entries (default false / 0.14+).
	AllowHookCommands bool

	// Skills lists skill ids to always inject into the system prompt (0.14+).
	Skills []string
	// SkillDirs are extra directories that contain skill packages (each subdir with SKILL.md).
	SkillDirs []string
	// SkillRouterTopK caps keyword-matched skills injected per Send (0 = default 3).
	SkillRouterTopK int
}

// Model identifies the model id for the selected provider.
type Model struct {
	ID string
}

// LocalOptions applies to the local runtime only.
type LocalOptions struct {
	CWD        string
	AllowShell *bool // nil = true (default)

	// LoadRules loads AGENTS.md and .anvil/rules/*.md into the system prompt (off by default).
	LoadRules bool
	// LoadCursorRules also loads .cursor/rules/* when LoadRules is true (still off unless set).
	// When SettingSources includes "project", .cursor/rules are loaded even if this is false.
	LoadCursorRules bool

	// Store selects the session backend: "jsonl" (default) or "sqlite".
	Store string

	// SettingSources enables file-based MCP/rules/hooks layers (0.10+).
	// Empty = inline-only (current default): no .cursor/mcp.json load.
	SettingSources []SettingSource

	// EnsureIndex builds a lexical project index when search.db is missing (0.13+).
	// Does not call the embed API. Create still prefers a fast path — use CLI for full rebuilds.
	EnsureIndex bool
}

// HasSettingSource reports whether src is enabled.
func (o LocalOptions) HasSettingSource(src SettingSource) bool {
	for _, s := range o.SettingSources {
		if s == src {
			return true
		}
	}
	return false
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

// MCPServerConfig launches or connects to an MCP server.
type MCPServerConfig struct {
	// Transport is stdio | http | sse. Empty: stdio if Command set, else http if URL set.
	Transport string
	Command   string
	Args      []string
	Env       []string // KEY=VAL for stdio process
	URL       string   // http / sse base URL
	// Headers are sent on HTTP/SSE requests (0.9+). Ignored for stdio. Not for OAuth flows.
	Headers map[string]string
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

// SendOption mutates per-send behavior.
type SendOption func(*sendConfig)

type sendConfig struct {
	Force bool
}

// WithForce cancels an in-flight run (if any) before accepting the new Send (0.10+).
func WithForce() SendOption {
	return func(c *sendConfig) { c.Force = true }
}
