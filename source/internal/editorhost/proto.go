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

import (
	"encoding/json"
	"time"
)

// ProtocolVersion is the editor host protocol revision. A breaking change to a
// method name, param, or result shape bumps it (same rule as the agentd
// protocol in doc/design/v1.0/11-worker-daemon.md).
const ProtocolVersion = 1

// Version is the host build version, overridable at link time.
var Version = "0.36.0-dev"

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
	MethodSkillsList    = "skills.list"
	MethodRulesList     = "rules.list"
	// MethodPermissionsGet reads the workspace rule file — the effective policy
	// for this workspace, and the place to put a squiggle when it will not
	// compile.
	MethodPermissionsGet = "permissions.get"
	// MethodPermissionsAdd appends one rule to that file, and answers the pending
	// approval it was written for when the new file decides it.
	MethodPermissionsAdd = "permissions.add"
	MethodShutdown       = "shutdown"

	// MethodApprovalResponse answers a pending approval.request. It is the only
	// client request that resolves a notification the host sent earlier.
	MethodApprovalResponse = "approval.response"
)

// Host to client notification methods.
const (
	NotifyEvent           = "event"
	NotifyDone            = "done"
	NotifyLog             = "log"
	NotifyApprovalRequest = "approval.request"
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
	// CodeNoApproval reports an approval.response naming a request that is not
	// pending — already answered, timed out, or aborted by a cancel.
	CodeNoApproval = -32004
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

	// LoadHooks and LoadPermissions are pointers so that "not sent" is distinct
	// from "sent as false": both default to ON for a workspace the editor opened,
	// because .anvil/hooks.json and .anvil/permissions.json are policy the user
	// wrote for that workspace. A client that wants them off has to say so.
	LoadHooks       *bool    `json:"load_hooks,omitempty"`
	LoadPermissions *bool    `json:"load_permissions,omitempty"`
	BuiltinTools    []string `json:"builtin_tools,omitempty"`
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
	Skills          []string `json:"skills,omitempty"`
	Plugins         []string `json:"plugins,omitempty"`

	// PermissionMode selects the approval policy. Empty means ModeAsk.
	PermissionMode string `json:"permission_mode,omitempty"`
	// PermissionGroups narrows or widens what ModeAsk / ModeAcceptEdits gate.
	// Empty means DefaultPermissionGroups; ModeBypass ignores it.
	PermissionGroups []string `json:"permission_groups,omitempty"`
	// ApprovalTimeoutMS bounds how long a gated call waits for the client.
	// Zero means DefaultApprovalTimeout; negative means wait until the run ends.
	ApprovalTimeoutMS int `json:"approval_timeout_ms,omitempty"`

	// TestCommand and BuildCommand register the run_tests and build tools. They
	// are argv arrays and never shell strings, so the client decides exactly
	// which process runs; the workspace settings supply the defaults.
	TestCommand  []string `json:"test_command,omitempty"`
	BuildCommand []string `json:"build_command,omitempty"`

	// Vision enables image attachments for the session. It is off by default:
	// a prompt carrying attachments to a session that did not opt in is
	// refused, so a client never gets a run that quietly ignores the image.
	Vision bool `json:"vision,omitempty"`
}

// SessionResult is the identity of a live session.
type SessionResult struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	CWD       string `json:"cwd"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	// PermissionMode is the mode the session was actually created with, after
	// defaults were applied.
	PermissionMode string `json:"permission_mode"`
}

// ---------------------------------------------------------------------------
// permissions.get / permissions.add
// ---------------------------------------------------------------------------

// RuleSpec is one rule as the workspace file spells it. It is the same shape in
// both directions: what a client reads out of the file, and what it asks the
// host to append.
type RuleSpec struct {
	// Effect is "allow", "ask", or "deny". Empty means allow when a client is
	// asking for a rule to be written.
	Effect string `json:"effect,omitempty"`
	Tool   string `json:"tool"`
	// Pattern is what the rule matches, read according to the call's facts: a
	// command line for shell, a path glob for the file tools, a tool-name glob
	// for anything else. Empty matches every call to the tool.
	Pattern string `json:"pattern,omitempty"`
	// Reason is shown to whoever the rule decides about: the model's tool error
	// for a deny, the card for an ask.
	Reason string `json:"reason,omitempty"`
}

// RuleInfo is one rule of a set as a client sees it: the rule, where it sits,
// and how to describe it.
type RuleInfo struct {
	// Index is the rule's position as the SDK counts it — one-based, the number a
	// denial quotes ("permissions.json rule 2"), and so the same number Describe
	// mentions. It is not an array offset.
	Index int `json:"index"`
	RuleSpec
	// Describe is the SDK's own sentence for this rule, the one a denial quotes.
	// A client shows it rather than composing its own, so the explanation of a
	// decision is written in the words that decision uses.
	Describe string `json:"describe,omitempty"`
}

// RuleProblem is a rule file that will not compile, as the parser sees it.
type RuleProblem struct {
	Message string `json:"message"`
	// Line and Column are one-based, and zero when the problem belongs to no
	// particular place — a version this build does not read, say.
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// PermissionsResult is the workspace rule file's state.
type PermissionsResult struct {
	// Path is absolute, so a client can open the file even before it exists.
	Path string `json:"path"`
	// Exists reports that the file is on disk. False with no Problem is a
	// workspace that has written down nothing.
	Exists bool `json:"exists"`
	// Rules is the file's rules, in the order that decides.
	Rules []RuleInfo `json:"rules"`
	// Problem is set when the file exists but cannot be read as rules. It is a
	// result rather than an error because a client asked to see the state of the
	// file, and a file somebody is halfway through editing is one of its states —
	// reported with the place, so it can be fixed where it is edited.
	Problem *RuleProblem `json:"problem,omitempty"`
}

// PermissionsAddParams appends one rule to the workspace rule file.
type PermissionsAddParams struct {
	Rule RuleSpec `json:"rule"`
	// RequestID, when set, names the pending approval this rule is being written
	// for. The host then answers that approval itself if the file now decides it,
	// so "always allow" takes effect on the call in front of the user instead of
	// on the next one.
	RequestID string `json:"request_id,omitempty"`
}

// PermissionsAddResult reports what the file became and what that did to the
// call the rule was written for.
type PermissionsAddResult struct {
	Path  string     `json:"path"`
	Index int        `json:"index"`
	Rules []RuleInfo `json:"rules"`
	// DecidedBy is the rule that decides the pending call now, which is not
	// always the one just written: a deny outranks an allow and an ask outranks
	// it too, wherever in the file each one sits.
	DecidedBy *RuleInfo `json:"decided_by,omitempty"`
	// Approved reports that the host answered the pending approval with allow,
	// because the file now decides the call. False means the card still has to be
	// answered — DecidedBy says what stood in the way.
	Approved bool `json:"approved,omitempty"`
}

// RememberOption is one "always allow" a client may offer on an approval card.
//
// Every option was evaluated against the very call being shown, so a button
// built from one always writes a rule that decides that call; a choice that
// would write a rule doing nothing is not offered at all.
type RememberOption struct {
	// Kind labels the choice: "exact", "prefix", "readonly", "path", or "tool".
	// The host decides it, so a client never derives a label from the pattern.
	Kind string   `json:"kind"`
	Rule RuleSpec `json:"rule"`
}

// ---------------------------------------------------------------------------
// approval.request / approval.response
// ---------------------------------------------------------------------------

// Permission modes, Claude Code-class. They compose public SDK options only.
const (
	// ModeAsk round-trips every gated call to the client.
	ModeAsk = "ask"
	// ModeAcceptEdits auto-allows in-workspace writes and edits; shell, verify,
	// egress, and external tools still ask. The SDK sandbox already guarantees
	// that path-taking tools stay inside the workspace, so "in workspace" needs
	// no extra check here.
	ModeAcceptEdits = "accept_edits"
	// ModePlan removes the tools that can change the workspace, so the model
	// produces a plan and stops.
	ModePlan = "plan"
	// ModeBypass installs a callback that approves every call without showing it.
	// The client is expected to say so loudly; the host deliberately does not
	// soften it. What bypass does *not* skip is the workspace's written policy:
	// a deny rule is still decided in the registry, where no callback can reach
	// it, and a rule of effect "ask" still opens the card — a statement about one
	// tool is narrower than a mode that speaks about everything.
	ModeBypass = "bypass"
	// ModeDontAsk installs a callback that refuses every call without showing it:
	// the mirror of ModeBypass. Anything the workspace's rules allow is decided
	// before any callback is reached, so what arrives here is exactly what nobody
	// pre-approved — which is what a run with nobody watching needs, and the one
	// thing no other mode can say.
	ModeDontAsk = "dont_ask"
)

// DefaultPermissionGroups is what ModeAsk and ModeAcceptEdits gate when the
// client names no groups. It closes the historical gap: before 0.35 only
// write_file, edit_file, apply_patch, and shell were gated, leaving run_tests,
// build, egress, MCP, plugin, and custom tools able to run unattended.
var DefaultPermissionGroups = []string{"write", "edit", "shell", "verify", "web", "mcp"}

// DefaultApprovalTimeout bounds an unanswered approval. On expiry the call is
// denied, so a run can never hang forever on a closed editor window.
const DefaultApprovalTimeout = 5 * time.Minute

// Approval decisions.
const (
	// DecisionAllow runs this call only.
	DecisionAllow = "allow"
	// DecisionAllowSession runs it and stops asking for the same tool and scope
	// for the rest of the session.
	DecisionAllowSession = "allow_session"
	// DecisionDeny refuses it. The model sees a tool error and can adapt.
	DecisionDeny = "deny"
)

// ApprovalPreview is what a gated call would do, projected from the SDK's
// public anvil.ToolPreview.
type ApprovalPreview struct {
	// Kind is "diff", "command", "summary", or empty when nothing could be
	// previewed — which is not a statement that the call is harmless.
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
	Diff string `json:"diff,omitempty"`
	// Before and After are the target's body before and after the projected
	// change, so the client can open a native diff editor. Empty for
	// apply_patch, which spans several files and carries only Diff.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Truncated reports that Before and After were withheld for size; Diff is
	// still present and is what the client should render instead.
	Truncated bool `json:"truncated,omitempty"`
	// Command is a display-ready argv. shell carries its one command string.
	Command []string `json:"command,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

// ApprovalRequestParams asks the client to allow or deny one tool call. It is a
// notification: the client answers with an approval.response request carrying
// the same request_id.
type ApprovalRequestParams struct {
	RequestID string          `json:"request_id"`
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args,omitempty"`
	Preview   ApprovalPreview `json:"preview"`
	// Decisions lists the answers this host accepts, so a client can render
	// buttons from the protocol rather than hard-coding them.
	Decisions []string `json:"decisions"`
	// Rule is the workspace rule that insisted on this prompt, when one did. It
	// is nil when the mode gate alone is the reason, so a card never claims a
	// rule asked when none did.
	Rule *RuleInfo `json:"rule,omitempty"`
	// Remember lists the "always allow" choices this call can honestly be
	// offered, each one already proven against it. Empty means no rule shape
	// would decide this call — a compound shell line, say — so the card offers
	// none rather than offering one that would not work.
	Remember []RememberOption `json:"remember,omitempty"`
	// TimeoutMS is how long the host will wait, for a UI countdown.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// ApprovalResponseParams answers an approval.request.
type ApprovalResponseParams struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
	// Scope, with DecisionAllowSession, narrows what is remembered. The host
	// does not interpret it, it only compares it verbatim, so a client can
	// allow one path by sending it and allow the whole tool by omitting it.
	Scope string `json:"scope,omitempty"`
	// Args, when set, replaces the call's arguments. It is the client's way to
	// approve an edited version of what the model proposed.
	Args json.RawMessage `json:"args,omitempty"`
	// Message is shown to the model when the decision is deny.
	Message string `json:"message,omitempty"`
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
	// Usage is cumulative across the stored runs, when the provider reported
	// any. The store is the only place a past session's tokens exist: the
	// terminal `done` of a run that has already ended is gone.
	Usage *Usage `json:"usage,omitempty"`
}

// ---------------------------------------------------------------------------
// skills.list / rules.list
// ---------------------------------------------------------------------------

// SkillEntry is one discoverable skill package. It is the catalogue, not the
// session's selection: a session uses a skill only when it names its id on
// session.new / session.resume, or when .anvil/skills.json marks it always-on.
type SkillEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Path is the SKILL.md, workspace-relative. Empty for a skill outside the
	// workspace root, which the client has no way to name.
	Path string `json:"path,omitempty"`
	// Always reports that this skill is injected into every session.
	Always bool `json:"always,omitempty"`
}

// SkillsParams selects the roots to scan.
type SkillsParams struct {
	// ScanUser includes ~/.anvil/skills. Off by default: the host walks outside
	// the workspace only when the client asks.
	ScanUser  bool     `json:"scan_user,omitempty"`
	SkillDirs []string `json:"skill_dirs,omitempty"`
}

// SkillsResult is the skill catalogue under the workspace root.
type SkillsResult struct {
	Skills []SkillEntry `json:"skills"`
}

// RuleFileInfo is one rule file as the system prompt sees it.
type RuleFileInfo struct {
	// Path is workspace-relative, and is the heading the prompt writes above
	// the body.
	Path string `json:"path"`
	// Bytes is the body's size after trimming.
	Bytes int `json:"bytes,omitempty"`
	// Included reports whether the body reached the system prompt.
	Included bool `json:"included"`
	// Truncated reports that the body was cut short by the character budget.
	Truncated bool `json:"truncated,omitempty"`
}

// RulesParams selects which rule layers to report. The values the client passes
// are the ones it put on the session, so the list describes that session.
type RulesParams struct {
	LoadProject bool `json:"load_project,omitempty"`
	LoadCursor  bool `json:"load_cursor_rules,omitempty"`
}

// RulesResult is the rule files that apply to the workspace.
type RulesResult struct {
	Files []RuleFileInfo `json:"files"`
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
	// Context is the editor's ambient material — referenced paths, the current
	// selection, diagnostics. The host folds it into the message it sends; see
	// context.go. It is never trusted for path containment: every path in it is
	// resolved against the workspace root and rejected if it leaves.
	Context *PromptContext `json:"context,omitempty"`
	// Attachments are images for the turn. They require a session created with
	// vision: true, because Options.Vision.Enabled is what makes the SDK accept
	// an attachment at all.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// PromptContext is what the editor knows that the prompt text alone does not
// say. Every field is optional.
type PromptContext struct {
	// Mentions are workspace-relative paths the user referenced. Files are read
	// into the message; directories become a listing.
	Mentions []Mention `json:"mentions,omitempty"`
	// Selection is the editor's active selection, sent as text.
	Selection *Selection `json:"selection,omitempty"`
	// Diagnostics are the client's own, straight from its language server.
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Mention is one referenced path. It carries a path, never contents: what gets
// read, how much of it, and whether it is inside the workspace at all are the
// host's decisions.
type Mention struct {
	Path string `json:"path"`
}

// Selection is the active editor selection.
type Selection struct {
	Path  string `json:"path"`
	Range Range  `json:"range"`
	Text  string `json:"text"`
}

// Range is a 1-based editor range, in the shape VS Code reports it.
type Range struct {
	StartLine   int `json:"start_line"`
	StartColumn int `json:"start_column"`
	EndLine     int `json:"end_line"`
	EndColumn   int `json:"end_column"`
}

// Diagnostic is one problem the client already has from its language server.
type Diagnostic struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message"`
}

// Attachment is one image. Exactly one of Data / Path is used: Data (base64) for
// an image pasted into the editor, Path (workspace-relative) for one picked
// from disk. Bytes never touch the filesystem, and the path is resolved by the
// SDK's own sandbox when the image is loaded.
type Attachment struct {
	Path   string `json:"path,omitempty"`
	Data   string `json:"data,omitempty"`
	MIME   string `json:"mime,omitempty"`
	Detail string `json:"detail,omitempty"` // openai detail: low | high | auto
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
	// Rule is the workspace rule that decided this tool result, when one did. It
	// is how a call that ran without a prompt says why it did not need one: nil
	// means no rule decided it, which is not the same as "some rule allowed it".
	Rule *RuleInfo `json:"rule,omitempty"`
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
