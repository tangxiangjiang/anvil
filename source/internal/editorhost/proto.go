// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package editorhost implements the stdio JSON-RPC protocol an editor client
// (the first-party VS Code extension) uses to drive in-process Anvil sessions.
//
// It is a standalone capability: it consumes the public
// github.com/anvil-dev/anvil package only. It must not import internal/daemon,
// internal/scheduler, internal/web, or any other tool-chain package, and must
// not read platform configuration. See doc/design/v1.0/35-vscode-extension.md.
//
// Framing: one compact JSON object per line on stdin / stdout. Nothing else may
// be written to stdout; diagnostics go to stderr.
package editorhost

import "encoding/json"

// ProtocolVersion is the editor host protocol revision. A breaking change to a
// method name, param, or result shape bumps it (same rule as the agentd
// protocol in doc/design/v1.0/11-worker-daemon.md).
const ProtocolVersion = 1

// Version is the host build version, overridable at link time.
var Version = "0.35.0-dev"

// Client to host request methods.
const (
	MethodInitialize    = "initialize"
	MethodInitialized   = "initialized"
	MethodSessionNew    = "session.new"
	MethodSessionResume = "session.resume"
	MethodSessionList   = "session.list"
	MethodSessionClose  = "session.close"
	MethodPrompt        = "prompt"
	MethodCancel        = "cancel"
	MethodModelsList    = "models.list"
	MethodShutdown      = "shutdown"
)

// Host to client notification methods.
const (
	NotifyEvent = "event"
	NotifyDone  = "done"
	NotifyLog   = "log"
)

// JSON-RPC 2.0 standard error codes.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// Host-specific error codes.
const (
	// CodeHostError reports an error returned by the Anvil SDK itself.
	CodeHostError = -32000
	// CodeNoSession reports an unknown or closed session id.
	CodeNoSession = -32001
	// CodeNoRun reports cancel with no matching active run.
	CodeNoRun = -32002
	// CodeRunInProgress reports a prompt while the session is already running.
	CodeRunInProgress = -32003
)

// Request is one inbound JSON-RPC call or notification. A nil ID marks a
// notification, which gets no reply.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a reply to a Request that carried an ID. A nil ID serializes as
// null, which is what JSON-RPC requires for parse and invalid-request errors.
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      *int64    `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// Notification is an outbound message with no ID.
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// initialize
// ---------------------------------------------------------------------------

// ClientInfo identifies the connecting editor.
type ClientInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitializeParams opens the session and fixes the workspace root. It must be
// the first request.
//
// The API key is deliberately absent: the client passes it to the host process
// environment (ANVIL_API_KEY and friends), where the SDK already picks it up.
// It never crosses the pipe.
type InitializeParams struct {
	Protocol int        `json:"protocol"`
	CWD      string     `json:"cwd"`
	Client   ClientInfo `json:"client,omitempty"`
}

// InitializeResult reports the negotiated protocol and the resolved root.
type InitializeResult struct {
	Protocol     int      `json:"protocol"`
	Host         string   `json:"host"`
	CWD          string   `json:"cwd"`
	Capabilities []string `json:"capabilities"`
}

// ---------------------------------------------------------------------------
// session.new / session.resume
// ---------------------------------------------------------------------------

// SessionParams configures a new or resumed session. Empty fields fall back to
// the process environment (ANVIL_PROVIDER / ANVIL_BASE_URL / ANVIL_MODEL), then
// to the SDK defaults.
type SessionParams struct {
	AgentID string `json:"agent_id,omitempty"` // session.resume only

	Provider        string   `json:"provider,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	Model           string   `json:"model,omitempty"`
	SystemPrompt    string   `json:"system_prompt,omitempty"`
	MaxSteps        int      `json:"max_steps,omitempty"`
	Store           string   `json:"store,omitempty"`
	LoadRules       bool     `json:"load_rules,omitempty"`
	LoadCursorRules bool     `json:"load_cursor_rules,omitempty"`
	SettingSources  []string `json:"setting_sources,omitempty"`
	BuiltinTools    []string `json:"builtin_tools,omitempty"`
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
	Skills          []string `json:"skills,omitempty"`
	Plugins         []string `json:"plugins,omitempty"`
}

// SessionResult is the identity of a live session.
type SessionResult struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	CWD       string `json:"cwd"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
}

// ---------------------------------------------------------------------------
// session.list
// ---------------------------------------------------------------------------

// SessionListParams filters the persisted agents under the workspace root.
type SessionListParams struct {
	// Store selects the session backend to read: "jsonl" (default) or "sqlite".
	Store string `json:"store,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// SessionListResult is the persisted-session listing.
type SessionListResult struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SessionInfo mirrors anvil.AgentInfo in host vocabulary.
type SessionInfo struct {
	AgentID    string `json:"agent_id"`
	CWD        string `json:"cwd"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	LastStatus string `json:"last_status,omitempty"`
	LastRunID  string `json:"last_run_id,omitempty"`
	Runs       int    `json:"runs"`
}

// ---------------------------------------------------------------------------
// session.close / prompt / cancel
// ---------------------------------------------------------------------------

// SessionRefParams names an open session.
type SessionRefParams struct {
	SessionID string `json:"session_id"`
}

// PromptParams sends one user turn and returns immediately. Output arrives as
// event / done notifications.
type PromptParams struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// PromptResult acknowledges the accepted turn.
type PromptResult struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	AgentID   string `json:"agent_id"`
}

// CancelParams stops the session's active run.
type CancelParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id,omitempty"` // empty cancels whatever is active
}

// ---------------------------------------------------------------------------
// models.list
// ---------------------------------------------------------------------------

// ModelsParams queries the OpenAI-compatible model catalog. The bearer token
// comes from the process environment, never from the wire.
type ModelsParams struct {
	Provider string `json:"provider,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
}

// ModelEntry is one catalog row.
type ModelEntry struct {
	ID            string `json:"id"`
	OwnedBy       string `json:"owned_by,omitempty"`
	Provider      string `json:"provider,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	Vision        bool   `json:"vision,omitempty"`
}

// ModelsResult is the catalog listing.
type ModelsResult struct {
	Models []ModelEntry `json:"models"`
}

// ---------------------------------------------------------------------------
// event / done notifications
// ---------------------------------------------------------------------------

// EventParams is one streamed unit of a run, projected from anvil.Event.
//
// Fields are omitted when empty so the wire stays compact; the client may treat
// every field except session_id, run_id, and type as optional.
type EventParams struct {
	SessionID  string          `json:"session_id"`
	RunID      string          `json:"run_id"`
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Content    string          `json:"content,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	Step       int             `json:"step,omitempty"`
	MaxSteps   int             `json:"max_steps,omitempty"`
	Code       string          `json:"code,omitempty"`
}

// DoneParams is the terminal envelope of a run, projected from anvil.RunResult.
// It is emitted exactly once per prompt, always after the last event.
type DoneParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Status    string `json:"status"` // finished | error | cancelled
	Result    string `json:"result,omitempty"`
	Steps     int    `json:"steps,omitempty"`
	Usage     *Usage `json:"usage,omitempty"`
	Err       string `json:"err,omitempty"`
	Code      string `json:"code,omitempty"`
}

// Usage carries provider token counts when the provider reports them.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// LogParams is a host diagnostic. Clients may surface these in an output
// channel; they never appear on stdout as anything but this notification.
type LogParams struct {
	Level   string `json:"level"` // debug | info | warn | error
	Message string `json:"message"`
}

func errInvalidParams(msg string) *RPCError {
	return &RPCError{Code: CodeInvalidParams, Message: msg}
}

func errHost(err error) *RPCError {
	if err == nil {
		return nil
	}
	return &RPCError{Code: CodeHostError, Message: err.Error()}
}

func errCode(code int, msg string) *RPCError {
	return &RPCError{Code: code, Message: msg}
}
