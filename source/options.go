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
	// MaxSteps is an optional hard ceiling on LLM rounds (0 = unlimited, default).
	// Runaway loops are stopped by the runtime loop guard (repeated tool calls / stalls).
	MaxSteps int
	// MaxWallTime bounds a single Send/Prompt run. Zero means no wall-time limit.
	// When exceeded, the run ends with StatusCancelled and CodeMaxWallTime.
	// Parent context deadline → CodeContextDeadline; explicit Cancel → CodeCancelled.
	MaxWallTime time.Duration

	// Tools are caller-registered tools merged with built-ins (0.3+).
	Tools []ToolSpec

	// MCPServers are inline MCP servers (0.3+). Not restored from disk — pass again on Resume.
	// When SettingSources load file MCP, inline entries override same-named file servers.
	MCPServers map[string]MCPServerConfig

	// Permission is invoked before a gated tool call. Which calls that covers is
	// set by PermissionGroups; with no groups it is the historical set of
	// mutating built-ins (write_file, edit_file, apply_patch, shell).
	// Passing Preview through lets a UI show the change before answering.
	Permission PermissionFunc

	// PermissionGroups widens Permission beyond the mutating built-ins (0.35+).
	//
	// Empty keeps today's behaviour exactly: only write_file, edit_file,
	// apply_patch, and shell are gated. Non-empty gates the built-in tools in
	// the listed groups (write, edit, shell, verify, web, mcp, …) or by literal
	// tool name, plus every external tool — MCP servers, plugins, and
	// Options.Tools entries — because those belong to no group and would
	// otherwise run without ever asking.
	//
	// Has no effect unless Permission is also set, since there is nobody to ask.
	PermissionGroups []string

	// PermissionRules is a rule set given in code (0.36+). Unlike PermissionGroups
	// it needs no callback to take effect: a deny holds whether or not Permission
	// is set, and an allow runs the call without a prompt.
	//
	// Rules written in .anvil/permissions.json come first when LocalOptions
	// asks for them, so the file keeps the order a reader sees and these append
	// to it.
	PermissionRules []PermissionRule

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
	// Groups: shell, read, write, edit, search, skills, web, lsp, git, verify, vision, mcp. Custom Tools entries are never filtered out.
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

	// WebSearch enables web_search / optional fetch_url (0.15+). Disabled by default (no egress).
	WebSearch WebSearchOptions

	// LSP enables language-server tools (0.16+). Disabled by default unless configured.
	LSP LSPOptions

	// Test configures run_tests (0.18+). Absent/empty Command → tool not registered.
	Test TestOptions
	// Build configures build (0.18+). Absent/empty Command → tool not registered.
	Build BuildOptions

	// Vision enables multimodal image attachments and optional read_image (0.19+).
	Vision VisionOptions
	// Attachments are images included on Prompt (and Create→first Send via Prompt helper).
	// Prefer WithAttachments on Agent.Send for multi-turn.
	Attachments []ImageAttachment

	// Plugins lists in-tree or discovered plugin ids to load (0.20+).
	Plugins []string
	// PluginDirs are search roots containing <id>/plugin.json for exec plugins.
	PluginDirs []string
	// PluginPolicy optionally gates ids/permissions (daemon/worker); nil = allow requested.
	PluginPolicy *PluginPolicy
}

// PluginPolicy mirrors daemon allowlist gates for in-process hosts (0.20+).
type PluginPolicy struct {
	Available      []string
	Deny           []string
	MaxPermissions []string // permission strings; empty = no cap
}

// VisionOptions controls image attachments / read_image (0.19+).
type VisionOptions struct {
	Enabled            bool
	MaxSide            int  // longest side before downscale; 0 = 2048
	MaxBytes           int  // max base64 payload bytes; 0 = 4MiB
	AllowRemoteImageURL bool // reserved; remote https fetch stays off in 0.19
}

// ImageAttachment is a host-supplied image for Send/Prompt (0.19+).
type ImageAttachment struct {
	Path   string // under cwd sandbox when set
	Bytes  []byte // in-memory image (e.g. screenshot); preferred over Path when non-empty
	MIME   string // optional; sniffed when empty
	Detail string // openai detail: low|high|auto
}

// TestOptions is the argv test command (0.18+).
type TestOptions struct {
	Command []string      // e.g. ["go","test","./..."]
	Timeout time.Duration // 0 = 10m
	Dir     string        // optional subdir under cwd
}

// BuildOptions is the argv build command (0.18+).
type BuildOptions struct {
	Command []string
	Timeout time.Duration // 0 = 5m
	Dir     string
}

// LSPOptions configures language servers (0.16+).
type LSPOptions struct {
	Enabled  bool
	Required bool // if true, Create fails when a server cannot start
	Servers  []LSPServerConfig
	// InitTimeout / RequestTimeout; zero uses package defaults (~15s / ~20s).
	InitTimeout    time.Duration
	RequestTimeout time.Duration
}

// LSPServerConfig maps a language/glob to a stdio language server command.
type LSPServerConfig struct {
	Language string
	Pattern  string // e.g. **/*.go
	Command  string
	Args     []string
}

// WebSearchOptions configures the web_search provider (0.15+).
type WebSearchOptions struct {
	Enabled bool
	// Provider is "http" (default when Enabled) or "mcp" (rely on MCPServers only; no builtin tools).
	Provider string
	// BaseURL is the search endpoint (POST JSON by default). May point at a gateway /v1/web/search.
	BaseURL string
	// APIKey is used as Bearer when non-empty; otherwise APIKeyEnv is read.
	APIKey string
	// APIKeyEnv defaults to ANVIL_WEB_SEARCH_KEY when empty and APIKey is empty.
	APIKeyEnv string
	// Method is POST (default) or GET.
	Method string
	// Timeout bounds each search/fetch call (0 = 15s).
	Timeout time.Duration
	// MaxResults default for web_search (0 = 5, hard max 10).
	MaxResults int
	// AllowFetchURL registers fetch_url when true (default false).
	AllowFetchURL bool
	// AllowPrivateURLs disables SSRF private-IP blocking (default false; tests only).
	AllowPrivateURLs bool
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

	// LoadHooks loads .anvil/hooks.json (off by default, 0.36+). Before this
	// existed the file was reachable only through SettingSources containing
	// "project", which neither the editor host nor the CLI ever set — so its deny
	// rules never applied in either. It is a switch of its own now.
	LoadHooks bool
	// LoadPermissions loads .anvil/permissions.json (off by default, 0.36+). A
	// malformed file fails Create rather than being ignored: silently dropping a
	// policy somebody wrote is worse than refusing to start.
	LoadPermissions bool

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

// PermissionFunc decides whether a gated tool may run.
type PermissionFunc func(ctx context.Context, req PermissionRequest) (PermissionDecision, error)

// PermissionRequest describes a pending gated tool call.
type PermissionRequest struct {
	ToolName string
	Args     json.RawMessage
	// Preview is what the call would do, computed without doing it (0.35+).
	// It is best-effort: an empty Kind means the tool's effect could not be
	// previewed, not that the call is harmless.
	Preview ToolPreview
	// Rule is the workspace rule that insisted on this prompt, when one did
	// (0.36+). It is nil when the mode gate alone is the reason the client is
	// being asked, so a prompt can always say why it appeared.
	Rule *PermissionRuleMatch
}

// ToolPreview is a description of a pending tool call's effect.
type ToolPreview struct {
	// Kind is "diff", "command", "summary", or "" when there is nothing to show.
	Kind string
	// Path is the target of a diff preview; for apply_patch it lists every file
	// the patch touches, comma-separated, for display.
	Path string
	// Paths are the paths the call touches, one entry each and unjoined — what a
	// permission rule matches, since a filename may contain the separator Path
	// joins with. Empty means the call touches no file, or that its paths could
	// not be determined.
	Paths []string
	// Diff is a unified diff of the projected change, for Kind == "diff".
	Diff string
	// Before and After are the target's body before and after the projected
	// change, for Kind == "diff". They let a client open a real diff editor
	// instead of applying the unified diff itself. Both are empty for
	// apply_patch, which may span several files.
	//
	// Both share one size cap (see maxPreviewBody in agent.go); a preview too
	// large to carry sets Truncated and drops **both** bodies rather than
	// handing a client a clipped post-image it would show as the whole file.
	Before string
	After  string
	// Truncated reports that Before and After were dropped for size. The Diff
	// is still present, so a client can fall back to rendering it.
	Truncated bool
	// Command is the argv that would run, for Kind == "command". shell is the
	// one exception: its argv is the single command string it hands to a shell.
	Command []string
	// Summary is a one-line description for Kind == "summary".
	Summary string
}

// Preview kinds, mirroring the values ToolPreview.Kind takes.
const (
	PreviewDiff    = "diff"
	PreviewCommand = "command"
	PreviewSummary = "summary"
)

// PermissionDecision is the hook outcome.
type PermissionDecision struct {
	Allow   bool
	Args    json.RawMessage // if non-nil, replaces tool args
	Message string          // deny reason shown to the model
}

// SendOption mutates per-send behavior.
type SendOption func(*sendConfig)

type sendConfig struct {
	Force       bool
	Attachments []ImageAttachment
}

// WithForce cancels an in-flight run (if any) before accepting the new Send (0.10+).
func WithForce() SendOption {
	return func(c *sendConfig) { c.Force = true }
}

// WithAttachments attaches images to this Send (requires Options.Vision.Enabled).
func WithAttachments(atts ...ImageAttachment) SendOption {
	return func(c *sendConfig) {
		c.Attachments = append(c.Attachments, atts...)
	}
}
