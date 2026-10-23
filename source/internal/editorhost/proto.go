// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package editorhost implements the stdio JSON-RPC protocol an editor client
// (the first-party VS Code extension) uses to drive in-process Anvil sessions.
//
// It is a standalone capability: it consumes the public
// github.com/anvil-dev/anvil package only. It must not import internal/daemon,
// internal/scheduler, internal/web, or any other tool-chain package, and must
// not read platform configuration. See doc/design/v0.x/35-vscode-extension.md.
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
// protocol in doc/design/v0.x/11-worker-daemon.md).
const ProtocolVersion = 1

// Version is the host build version, overridable at link time. It carries the
// product line, not the protocol revision (that is ProtocolVersion): the
// extension compares the dotted minor it packaged with against this string and
// warns on a mismatch, so the two have to be released together (0.46).
var Version = "0.47.0"

// Client to host request methods.
const (
	MethodInitialize    = "initialize"
	MethodInitialized   = "initialized"
	MethodSessionNew    = "session.new"
	MethodSessionResume = "session.resume"
	MethodSessionList   = "session.list"
	// MethodSessionTranscript replays a stored conversation. It is keyed by agent
	// id rather than by session, so it answers for a session this host has not
	// opened — which is the case it exists for: a client that has just reloaded
	// its window and knows which agent it was talking to.
	MethodSessionTranscript = "session.transcript"
	MethodSessionClose      = "session.close"
	// MethodSessionRename appends a title to a stored session. Additive: it
	// changes no record that is already on disk, and a host without it answers
	// -32601, which the client reads as "no rename here".
	MethodSessionRename = "session.rename"
	// MethodSessionDelete removes a stored session's records.
	MethodSessionDelete = "session.delete"
	// MethodSessionFork copies a stored conversation up to a finished run into a
	// new agent, and answers its id. Additive: a host without it answers -32601,
	// which a client reads as "no fork here".
	MethodSessionFork = "session.fork"
	// MethodSessionCompact summarizes an idle session's older turns on request.
	// It is the wire behind /compact, and answers the same counts the runtime's
	// automatic compaction reports. Additive: a host without it answers -32601,
	// which a client reads as "no manual compaction here".
	MethodSessionCompact = "session.compact"
	// MethodSessionContext reports where a live session stands against its
	// declared window, so a tab that has not run anything can still draw a meter.
	// Additive for the same reason as the rest of this group.
	MethodSessionContext = "session.context"
	// MethodCheckpointList lists the runs of a stored session that captured a
	// pre-image, so a client can offer to put those files back. Additive: a host
	// without it answers -32601, which a client reads as "no checkpoints here".
	MethodCheckpointList = "checkpoint.list"
	// MethodCheckpointRestore puts one run's captured files back.
	MethodCheckpointRestore = "checkpoint.restore"
	MethodPrompt            = "prompt"
	MethodCancel            = "cancel"
	MethodModelsList        = "models.list"
	MethodSkillsList        = "skills.list"
	MethodRulesList         = "rules.list"
	// MethodMCPList reports the MCP servers the selected file layers declare,
	// and which of those files exist. Since 0.44 it also reports the state the
	// host observed, marked by `runtime: true`; an older host omits that field
	// and the client falls back to the configuration-only card. Additive: a host
	// without it answers -32601, which a client reads as "no listing here".
	MethodMCPList = "mcp.list"
	// MethodMCPAdd and MethodMCPUpdate write one entry to the selected scope and
	// answer a fresh listing. The host delegates the write to the public SDK
	// writers, so validation and unknown-field preservation are the same rules a
	// caller of the SDK gets.
	MethodMCPAdd    = "mcp.add"
	MethodMCPUpdate = "mcp.update"
	// MethodMCPRemove deletes one entry from the selected scope.
	MethodMCPRemove = "mcp.remove"
	// MethodMCPSetEnabled turns one entry off or on without deleting it.
	MethodMCPSetEnabled = "mcp.set_enabled"
	// MethodMCPReconnect restarts one server's connection.
	MethodMCPReconnect = "mcp.reconnect"
	// MethodMCPAuthenticate starts a sign-in for a remote server and answers the
	// URL a browser should open. The flow completes in the background; the
	// result is announced by mcp.status, not by this reply.
	MethodMCPAuthenticate = "mcp.authenticate"
	// MethodMCPSignOut deletes a server's stored tokens and puts it back in
	// auth_required.
	MethodMCPSignOut = "mcp.sign_out"
	// MethodPermissionsGet reads the workspace rule file — the effective policy
	// for this workspace, and the place to put a squiggle when it will not
	// compile.
	MethodPermissionsGet = "permissions.get"
	// MethodPermissionsAdd appends one rule to that file, and answers the pending
	// approval it was written for when the new file decides it.
	MethodPermissionsAdd = "permissions.add"
	// MethodPermissionsSet replaces the whole file with the list a client showed
	// a person. It is the writer behind the rules manager: the host compiles the
	// text with the same parser a session uses before touching the disk. Additive:
	// a host without it answers -32601, which a client reads as "no manager here".
	MethodPermissionsSet = "permissions.set"
	// MethodTasksList reports the session tree's background work, shell jobs and
	// subagents alike. Additive: a host without it answers -32601, which a client
	// reads as "no task surface here".
	MethodTasksList = "tasks.list"
	// MethodTasksOutput pages one task's output from a byte offset.
	MethodTasksOutput = "tasks.output"
	// MethodTasksCancel stops one running task and answers its final state.
	MethodTasksCancel = "tasks.cancel"
	MethodShutdown    = "shutdown"

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
	// NotifyTask reports a background task starting, ending, or changing state.
	// It is a table-change signal, not a progress stream: the payload is one
	// snapshot, and a client that wants more asks tasks.list / tasks.output.
	NotifyTask = "task"
	// NotifyCompact reports a manual compaction finishing. A run's automatic
	// compaction travels as an `event` of type compact, because it has a run to
	// belong to; this one has no run, and a client that replays the transcript
	// finds the divider in the store instead.
	NotifyCompact = "compact"
	// NotifyMCPStatus reports one MCP server's state changing: connecting,
	// connected, failed, auth_required or disabled. It is the push half of
	// mcp.list; a client that keeps a panel open does not poll.
	NotifyMCPStatus = "mcp.status"
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
	// CodeNoTask reports tasks.output / tasks.cancel naming a task that is not in
	// this session's table. A task id belonging to another session lands here
	// rather than being served.
	CodeNoTask = -32005
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

	// LSP enables the language tools. Absent means off. A configured server
	// registers definition, references, hover and the rest through the SDK's
	// LSP supervisor; a server that cannot start is a missing tool, not a
	// refused session, which is why Required is not on the wire.
	//
	// The SDK also reads {cwd}/.anvil/lsp.json when the session loads the
	// project setting source. Servers sent here are appended to that file's
	// list and win on the switches; the file stays the workspace's own way to
	// describe a language server.
	LSP *LSPParams `json:"lsp,omitempty"`

	// Vision enables image attachments for the session. It is off by default:
	// a prompt carrying attachments to a session that did not opt in is
	// refused, so a client never gets a run that quietly ignores the image.
	Vision bool `json:"vision,omitempty"`

	// Subagents registers spawn_agent and the background task tools. It is on
	// for the editor unless sent as false, which is the one way a user turns
	// agent-in-agent off for a workspace. The pointer distinguishes "not sent"
	// from "sent as false" for the same reason the load flags use one.
	Subagents *bool `json:"subagents,omitempty"`

	// WebSearch turns on the SDK's web tools for this session. Absent means
	// off, and off means the tools are not registered at all: a tool the model
	// can see but cannot authenticate is worse than no tool, because the model
	// will find it and fail.
	//
	// The key is deliberately not here. It reaches the host through the child
	// environment as ANVIL_WEB_SEARCH_API_KEY, the same discipline the model
	// key follows: credentials do not cross the pipe.
	WebSearch *WebSearchParams `json:"web_search,omitempty"`

	// Context is the session's declared token budget. Absent means the zero
	// value, which keeps automatic compaction off exactly as before this field
	// existed: a client that never declares a window cannot have its session
	// summarized behind its back. Negative values are normalized by the SDK.
	Context *ContextParams `json:"context,omitempty"`
}

// LSPParams is the wire form of anvil.LSPOptions. Enabled is explicit because
// a client may switch language servers off without clearing the entries it
// keeps for later, the same shape the MCP layer settings use.
type LSPParams struct {
	Enabled bool              `json:"enabled"`
	Servers []LSPServerParams `json:"servers,omitempty"`
}

// LSPServerParams maps a language or glob to a stdio language server. Command
// and Args stay separate: the host never joins them into a shell string.
type LSPServerParams struct {
	Language string   `json:"language,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
}

// ContextParams is the wire form of anvil.ContextOptions. The SDK fills the
// defaults, so an omitted fraction or keep count means 0.8 and 8.
type ContextParams struct {
	WindowTokens  int     `json:"window_tokens,omitempty"`
	AutoCompactAt float64 `json:"auto_compact_at,omitempty"`
	KeepRecent    int     `json:"keep_recent,omitempty"`
}

// WebSearchParams configures the web tools. Presence is the switch — a client
// that wants them off omits the field rather than sending enabled=false, so a
// host that predates the field and a client that does not want it agree.
type WebSearchParams struct {
	// Provider is "http" (the default when empty) or "mcp". The second
	// registers no built-in tools and expects the workspace's MCP servers to
	// supply web_search.
	Provider string `json:"provider,omitempty"`
	// BaseURL is the JSON search endpoint, required for the http provider.
	BaseURL string `json:"base_url,omitempty"`
	// MaxResults is the per-search result count (0 uses the SDK default of 5,
	// and the SDK caps it at 10).
	MaxResults int `json:"max_results,omitempty"`
}

// SessionResult is the identity of a live session.
type SessionResult struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	CWD       string `json:"cwd"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	// PermissionMode is the mode the session was actually created with, after
	// defaults were applied and after the workspace file had its say.
	PermissionMode string `json:"permission_mode"`
	// PermissionModeSource is where that mode came from: "request" when the client
	// named it, "file" when the workspace's .anvil/permissions.json declared it,
	// "default" when neither did. A client shows it, because "why is this session
	// in accept_edits" has two different answers and only one of them is something
	// the person just clicked.
	PermissionModeSource string `json:"permission_mode_source"`
	// PermissionModeRefused is what the workspace declared and the host would not
	// honour, empty when nothing was refused. The session still starts — in "ask" —
	// because a bad line in a project file must not be a workspace nobody can open;
	// saying so is what keeps that from being a silent drop.
	PermissionModeRefused string `json:"permission_mode_refused,omitempty"`
}

// Where a session's mode came from. See resolveMode.
const (
	// ModeSourceRequest is the mode the client sent.
	ModeSourceRequest = "request"
	// ModeSourceFile is the mode the workspace's .anvil/permissions.json declared.
	ModeSourceFile = "file"
	// ModeSourceDefault is neither: the ladder's floor, ModeAsk.
	ModeSourceDefault = "default"
)

// ---------------------------------------------------------------------------
// permissions.get / permissions.add
// ---------------------------------------------------------------------------

// RuleSpec is one rule as a rule file spells it. It is the same shape in both
// directions: what a client reads out of a file, and what it asks the host to
// append. The file it belongs to is not part of the rule — see RuleInfo.Source
// and PermissionsAddParams.Source.
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

// Rule sources on the wire (0.46+). They name the file a rule was read from or
// written to; the manager groups by them and the operations take one.
const (
	// RuleSourceUser is ~/.anvil/permissions.json, or $ANVIL_HOME/permissions.json.
	RuleSourceUser = "user"
	// RuleSourceWorkspace is .anvil/permissions.json. It is the default when a
	// request names no source, which is what a client that predates scopes means.
	RuleSourceWorkspace = "workspace"
)

// RuleInfo is one rule of a set as a client sees it: the rule, where it sits,
// where it came from, and how to describe it.
type RuleInfo struct {
	// Index is the rule's position in its own source, one-based — the number a
	// denial quotes ("user permissions.json rule 2"), and the same number Describe
	// mentions. It is not an array offset and not a position in the merged list:
	// with two files, a merged position names no line a reader can open.
	Index int `json:"index"`
	// Source is "user" or "workspace" (0.46+). Empty on a rule built by hand —
	// a rule being written has no source yet.
	Source string `json:"source,omitempty"`
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
	// DefaultMode is the mode the file declares for new sessions, and only when the
	// host would honour it. Empty means this workspace starts sessions in the
	// ladder's floor — either because it declares nothing (DefaultModeRefused is
	// empty too) or because what it declares cannot be honoured (DefaultModeRefused
	// says what it was). It is deliberately not filled in with "ask" for a file that
	// said nothing: that would read as if the file had.
	DefaultMode string `json:"default_mode,omitempty"`
	// DefaultModeRefused is what the file declared and the host would not honour,
	// in words, empty when there is nothing to report.
	DefaultModeRefused string `json:"default_mode_refused,omitempty"`
	// Rules is every readable layer's rules in the order they decide: the user
	// file's first, then the workspace's, each entry carrying its own Source and
	// its own one-based Index (0.46+). A layer that cannot be read contributes
	// none — its Problem says why.
	Rules []RuleInfo `json:"rules"`
	// Problem is set when the file exists but cannot be read as rules. It is a
	// result rather than an error because a client asked to see the state of the
	// file, and a file somebody is halfway through editing is one of its states —
	// reported with the place, so it can be fixed where it is edited.
	Problem *RuleProblem `json:"problem,omitempty"`
	// UserPath is the person's rule file (0.46+), absolute. Empty only when the
	// home directory could not be resolved, in which case there is no user layer
	// to show.
	UserPath string `json:"user_path,omitempty"`
	// UserExists reports that the user file is on disk. False with no
	// UserProblem is a person who has written no preferences.
	UserExists bool `json:"user_exists"`
	// UserProblem is the user file's parse problem, reported the same way as the
	// workspace's — with the place — and shown read-only by the manager.
	UserProblem *RuleProblem `json:"user_problem,omitempty"`
	// Protected are the globs the user file adds to the fixed protected list
	// (0.46+). They are not rules: they are paths an allow rule may not cover.
	Protected []string `json:"protected,omitempty"`
}

// PermissionsAddParams appends one rule to a rule file.
type PermissionsAddParams struct {
	Rule RuleSpec `json:"rule"`
	// Source is the file to append to: "user" or "workspace" (0.46+). Empty
	// means "workspace", which is what every client that predates scopes means.
	Source string `json:"source,omitempty"`
	// RequestID, when set, names the pending approval this rule is being written
	// for. The host then answers that approval itself if the file now decides it,
	// so "always allow" takes effect on the call in front of the user instead of
	// on the next one.
	RequestID string `json:"request_id,omitempty"`
}

// PermissionsAddResult reports what the file became and what that did to the
// call the rule was written for.
type PermissionsAddResult struct {
	Path string `json:"path"`
	// Source is the file Path names: "user" or "workspace" (0.46+).
	Source string `json:"source,omitempty"`
	// Index is the appended rule's one-based position in that file.
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

// PermissionsSetParams replaces the workspace rule file with exactly these
// rules and this declaration.
//
// It is a replacement, not a patch: a client sends the list it showed a person,
// and the file becomes that list. The host refuses rather than guesses when the
// existing file cannot be read — replacing a policy this build could not parse
// is the failure the rule file is arranged against.
type PermissionsSetParams struct {
	Rules []RuleSpec `json:"rules"`
	// DefaultMode is a ladder word, or empty to remove the declaration. The host
	// refuses a word the ladder does not have and refuses bypass, in the same
	// words get and the session result use. It belongs to the workspace file
	// only: sending it for the user layer is refused.
	DefaultMode string `json:"default_mode,omitempty"`
	// Source is the layer this save replaces: "user" or "workspace" (0.46+).
	// Empty means "workspace".
	Source string `json:"source,omitempty"`
	// Protected replaces the user file's protected-path globs. It belongs to the
	// user file only: sending it for the workspace layer is refused.
	Protected []string `json:"protected,omitempty"`
	// Create allows a save of the user layer to make the file when it does not
	// exist (0.46+). Without it the host refuses, because the manager shows a
	// missing user file read-only and a save that was not meant to create must
	// not quietly create one. The workspace layer keeps create-on-save.
	Create bool `json:"create,omitempty"`
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
	// ModeAuto installs a classifier that approves the calls it can prove
	// harmless and leaves the rest to the ordinary card. It is a deterministic
	// classifier — anvil.AutoApproveCall — rather than a model call: read-only
	// shell lines (proved by the same analyzer the "readonly" rule uses),
	// in-workspace writes and edits, and the operator-configured test/build
	// commands run without asking; egress, MCP, plugins and anything unknown
	// still do. A rule of effect "ask" opens the card, as it does in bypass.
	ModeAuto = "auto"
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

// PermissionModes is every mode the ladder has, in the order a composer offers
// them: from the one that asks about everything to the one that asks about
// nothing. It is the list permissionMode accepts, and the list the extension's
// select renders — test/unit/permissionModes.test.ts reads this and protocol.ts
// and the panel's markup as text, because a mode this side has and the client
// does not is a silent hole in what a person can choose.
var PermissionModes = []string{ModeAsk, ModeAcceptEdits, ModeAuto, ModePlan, ModeDontAsk, ModeBypass}

// DefaultPermissionGroups is what ModeAsk and ModeAcceptEdits gate when the
// client names no groups. It closes the historical gap: before 0.35 only
// write_file, edit_file, apply_patch, and shell were gated, leaving run_tests,
// build, egress, MCP, plugin, and custom tools able to run unattended. The
// agent group joined in 0.42: a call that spends tokens and creates an agent
// with this session's permissions is exactly what a card is for.
var DefaultPermissionGroups = []string{"write", "edit", "shell", "verify", "web", "mcp", "agent"}

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
	// Protected are the paths this call touches that an allow rule may not cover
	// (0.46+): any `.git` segment, the workspace's `.anvil/checkpoints/`, the
	// Anvil home, and the user file's globs. It is set whether or not a rule
	// asked, because it is why no remembered allow can settle this call.
	Protected []string `json:"protected,omitempty"`
	// TimeoutMS is how long the host will wait, for a UI countdown.
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// AgentID and Depth name the agent whose call is being decided, so a card can
	// say a subagent is asking instead of implying the session's own agent is.
	// AgentID is empty only for a callback installed outside Create/Resume, and
	// Depth is 0 and omitted for the session's own agent.
	AgentID string `json:"agent_id,omitempty"`
	Depth   int    `json:"depth,omitempty"`
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
	AgentID  string `json:"agent_id"`
	CWD      string `json:"cwd"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Title is the session's first user message, one line and truncated by the
	// store. Absent when the session was never prompted: a client falls back to
	// the agent id rather than showing a title nobody wrote.
	Title      string `json:"title,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	LastStatus string `json:"last_status,omitempty"`
	LastRunID  string `json:"last_run_id,omitempty"`
	Runs       int    `json:"runs"`
	// Usage is cumulative across the stored runs, when the provider reported
	// any. The store is the only place a past session's tokens exist: the
	// terminal `done` of a run that has already ended is gone.
	Usage *Usage `json:"usage,omitempty"`
	// Subagent identity (0.42+). Empty on a top-level session. ParentAgentID is
	// what the tree nests a child under, and TaskTitle is the description its
	// spawn call carried — the name a person gave the work, not a derived one.
	ParentAgentID    string `json:"parent_agent_id,omitempty"`
	ParentRunID      string `json:"parent_run_id,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
	Depth            int    `json:"depth,omitempty"`
	Subagent         bool   `json:"subagent,omitempty"`
	TaskTitle        string `json:"task_title,omitempty"`
}

// ---------------------------------------------------------------------------
// session.transcript
// ---------------------------------------------------------------------------

// SessionTranscriptParams names the agent to replay.
type SessionTranscriptParams struct {
	AgentID string `json:"agent_id"`
	// Limit caps the entries returned, keeping the newest. Zero means the SDK's
	// default.
	Limit int `json:"limit,omitempty"`
	// Store selects the session backend to read: "jsonl" (default) or "sqlite".
	Store string `json:"store,omitempty"`
}

// ---------------------------------------------------------------------------
// session.rename / session.delete
// ---------------------------------------------------------------------------

// SessionRenameParams names the session and the title to give it. Both are
// agent-keyed, like session.list and session.transcript: a session can be named
// before it is ever opened in this window, and after the window that ran it is
// gone.
type SessionRenameParams struct {
	AgentID string `json:"agent_id"`
	Title   string `json:"title"`
	// Store selects the session backend to write: "jsonl" (default) or "sqlite".
	Store string `json:"store,omitempty"`
}

// SessionRenameResult confirms what was written, so a client can show the name
// it will actually see in the list rather than the one it typed.
type SessionRenameResult struct {
	AgentID string `json:"agent_id"`
	Title   string `json:"title"`
}

// SessionDeleteParams names the session to forget.
type SessionDeleteParams struct {
	AgentID string `json:"agent_id"`
	// Store selects the session backend to write: "jsonl" (default) or "sqlite".
	Store string `json:"store,omitempty"`
}

// SessionDeleteResult confirms the deletion.
type SessionDeleteResult struct {
	AgentID string `json:"agent_id"`
	Deleted bool   `json:"deleted"`
}

// SessionForkParams names the conversation and the run ending the part to keep.
//
// Both are required. A fork copies records only up to a `run_end`, so a run id
// that never ended — one still going, or one whose end was never written — is
// refused rather than approximated.
type SessionForkParams struct {
	AgentID string `json:"agent_id"`
	RunID   string `json:"run_id"`
	// Store selects the session backend to read and write: "jsonl" (default) or
	// "sqlite".
	Store string `json:"store,omitempty"`
}

// SessionForkResult is the id of the new conversation. The host does not open
// it: the client does, in a new tab, which is what makes the fork usable.
type SessionForkResult struct {
	AgentID string `json:"agent_id"`
}

// ---------------------------------------------------------------------------
// session.compact / session.context
// ---------------------------------------------------------------------------

// SessionCompactResult is what a manual compaction did. Compacted false means
// there was nothing old enough to summarize — the call is a no-op, not an
// error, because a person who asks twice should get an answer, not a failure.
type SessionCompactResult struct {
	SessionID  string `json:"session_id"`
	Compacted  bool   `json:"compacted"`
	Before     int    `json:"before,omitempty"`
	After      int    `json:"after,omitempty"`
	Summarized int    `json:"summarized,omitempty"`
	// Context is the state after the compaction, so a client does not have to
	// follow with session.context to redraw its meter.
	Context ContextInfo `json:"context"`
}

// ContextInfo describes where a session's conversation stands against its
// declared budget. Every figure is an estimate of the next request's size, never
// the provider's token count; the field names say so by saying "estimated".
type ContextInfo struct {
	EstimatedTokens int `json:"estimated_tokens"`
	// WindowTokens is 0 when the session did not declare a window, which is also
	// why CompactAtTokens is 0 and automatic compaction is off.
	WindowTokens    int `json:"window_tokens"`
	CompactAtTokens int `json:"compact_at_tokens"`
	Messages        int `json:"messages"`
}

// ---------------------------------------------------------------------------
// checkpoint.list / checkpoint.restore
// ---------------------------------------------------------------------------

// CheckpointListParams names the session whose run checkpoints to read. Like
// session.transcript it is agent-keyed: a checkpoint belongs to a session that
// may not be open in this window.
type CheckpointListParams struct {
	AgentID string `json:"agent_id"`
}

// CheckpointListResult is the runs that have a checkpoint, newest first.
type CheckpointListResult struct {
	Runs []CheckpointRun `json:"runs"`
}

// CheckpointRun mirrors anvil.CheckpointRun in host vocabulary.
type CheckpointRun struct {
	RunID     string           `json:"run_id"`
	CreatedAt string           `json:"created_at,omitempty"`
	Files     []CheckpointFile `json:"files"`
}

// CheckpointFile is one path a run captured.
type CheckpointFile struct {
	Path string `json:"path"`
	// Existed false means the run created the file; restoring deletes it.
	Existed bool  `json:"existed"`
	Size    int64 `json:"size,omitempty"`
}

// CheckpointRestoreParams names the run to put back.
type CheckpointRestoreParams struct {
	AgentID string `json:"agent_id"`
	RunID   string `json:"run_id"`
}

// CheckpointRestoreResult is what the restore actually did.
type CheckpointRestoreResult struct {
	RunID string         `json:"run_id"`
	Files []RestoredFile `json:"files"`
}

// RestoredFile is one path a restore wrote or deleted.
type RestoredFile struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
	// Skipped and Reason are set when the file changed after the run and was left
	// as it is. Warning is set when it was restored with no post-run state to check.
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Warning string `json:"warning,omitempty"`
}

// SessionTranscriptResult is a stored conversation, oldest first.
type SessionTranscriptResult struct {
	Entries []TranscriptEntry `json:"entries"`
	// Dropped is how many entries the cap and the run boundary left out. The
	// client shows the tail and says how much of it this is.
	Dropped int `json:"dropped,omitempty"`
}

// TranscriptEntry mirrors anvil.TranscriptEntry in host vocabulary. It carries
// what can be rendered and nothing else: the reader never had reasoning or an
// API key to hand back.
type TranscriptEntry struct {
	// Kind is "message", "run_end", or "summary" (0.43+). A summary entry is the
	// compaction boundary: it carries the text the model kept and the run it
	// belongs to, and the turns before it stay in the replay below it.
	Kind  string `json:"kind"`
	RunID string `json:"run_id,omitempty"`

	// message
	Role       string           `json:"role,omitempty"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []TranscriptCall `json:"tool_calls,omitempty"`

	// run_end
	Status string `json:"status,omitempty"`
	Steps  int    `json:"steps,omitempty"`
	Usage  *Usage `json:"usage,omitempty"`
}

// TranscriptCall is one tool invocation inside a replayed assistant message.
type TranscriptCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
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

// MCPListParams names the file layers to report. An empty list is an empty
// listing: the client sends exactly the sources it turned on, so nothing is
// read that the session would not read.
type MCPListParams struct {
	SettingSources []string `json:"setting_sources,omitempty"`
	// IncludeTools asks for each connected server's tool catalogue. The chat
	// card does not need it; the manager draws it.
	IncludeTools bool `json:"include_tools,omitempty"`
}

// MCPServerInfo is one declared MCP server and — from a host that answers with
// Runtime true — the state the host observed for it.
//
// The launch fields are what a manager needs to show and edit the entry; EnvNames
// and HeaderNames are keys only, because those maps commonly hold bearer tokens
// and API keys.
type MCPServerInfo struct {
	Name      string `json:"name"`
	Transport string `json:"transport,omitempty"`
	// Source is the config file that declared this server.
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
	// State is disabled | connecting | connected | failed | auth_required. Empty
	// on a host that predates the runtime.
	State string `json:"state,omitempty"`
	// ToolCount is the number of tools the connected server listed. It is
	// meaningful only when State is connected.
	ToolCount int `json:"tool_count"`
	// Tools is present only when the request asked for include_tools and the
	// server is connected.
	Tools []MCPToolInfo `json:"tools,omitempty"`
	// Error is the start or transport failure, in words. It never contains a
	// token or a secret header value.
	Error string `json:"error,omitempty"`
	// ConnectedAt is when the current session was established, RFC 3339 in UTC.
	ConnectedAt string `json:"connected_at,omitempty"`

	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
	// EnvNames and HeaderNames are the keys, sorted. Values never leave the file
	// through a listing.
	EnvNames    []string `json:"env_names,omitempty"`
	HeaderNames []string `json:"header_names,omitempty"`
}

// MCPToolInfo is one tool a connected server offers.
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MCPSourceInfo is one config file the listing looked at.
type MCPSourceInfo struct {
	Source string `json:"source"` // project | user
	Path   string `json:"path"`
	// Exists is false when the file is not there. That is a note, not an error.
	Exists bool `json:"exists"`
}

// MCPProblem is a malformed enabled config file. It is a result, not an RPC
// error: the panel has to keep showing the last good statuses beside the parser
// error, and make the document read-only until a person fixes the file.
type MCPProblem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// MCPListResult is what the selected layers declare and what the host observed.
type MCPListResult struct {
	// Runtime marks a host that can observe connection state. An older host
	// omits it and a client falls back to configuration only.
	Runtime bool            `json:"runtime"`
	Servers []MCPServerInfo `json:"servers"`
	Sources []MCPSourceInfo `json:"sources"`
	Problem *MCPProblem     `json:"problem,omitempty"`
}

// MCPStatusParams is one server's state transition. The server object is the
// same shape the listing uses, and the source set says which runtime it belongs
// to — a client shows two runtimes' servers apart.
type MCPStatusParams struct {
	Server         MCPServerInfo `json:"server"`
	SettingSources []string      `json:"setting_sources,omitempty"`
}

// MCPServerDraftWire is one entry as a client wants it written. Env and Headers
// follow "nil means keep" on update: an omitted map leaves the file's values
// alone, and an explicit empty map clears them.
type MCPServerDraftWire struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	URL       string            `json:"url,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
}

// MCPAddParams writes one new entry.
type MCPAddParams struct {
	Scope          string             `json:"scope"` // project | user
	Server         MCPServerDraftWire `json:"server"`
	SettingSources []string           `json:"setting_sources,omitempty"`
	IncludeTools   bool               `json:"include_tools,omitempty"`
}

// MCPUpdateParams replaces the fields a draft names on an existing entry.
type MCPUpdateParams struct {
	Scope          string             `json:"scope"`
	Server         MCPServerDraftWire `json:"server"`
	SettingSources []string           `json:"setting_sources,omitempty"`
	IncludeTools   bool               `json:"include_tools,omitempty"`
}

// MCPRemoveParams deletes one entry from one scope.
type MCPRemoveParams struct {
	Scope          string   `json:"scope"`
	Name           string   `json:"name"`
	SettingSources []string `json:"setting_sources,omitempty"`
	IncludeTools   bool     `json:"include_tools,omitempty"`
}

// MCPSetEnabledParams turns one entry off or on.
type MCPSetEnabledParams struct {
	Scope          string   `json:"scope"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	SettingSources []string `json:"setting_sources,omitempty"`
	IncludeTools   bool     `json:"include_tools,omitempty"`
}

// MCPReconnectParams restarts one server.
type MCPReconnectParams struct {
	Name           string   `json:"name"`
	SettingSources []string `json:"setting_sources,omitempty"`
	IncludeTools   bool     `json:"include_tools,omitempty"`
}

// MCPAuthenticateParams starts a sign-in for one server.
type MCPAuthenticateParams struct {
	Name           string   `json:"name"`
	SettingSources []string `json:"setting_sources,omitempty"`
}

// MCPAuthenticateResult is the URL a browser should open. The reply does not
// wait for the sign-in: completion arrives as mcp.status.
type MCPAuthenticateResult struct {
	Server    string `json:"server"`
	AuthURL   string `json:"auth_url"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// MCPSignOutParams deletes one server's stored tokens.
type MCPSignOutParams struct {
	Name           string   `json:"name"`
	SettingSources []string `json:"setting_sources,omitempty"`
	IncludeTools   bool     `json:"include_tools,omitempty"`
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
	// Quotes are earlier conversation messages the user explicitly referenced.
	Quotes []Quote `json:"quotes,omitempty"`
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

// Quote is one earlier visible conversation message.
type Quote struct {
	Role string `json:"role"`
	Text string `json:"text"`
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
// tasks.list / tasks.output / tasks.cancel
// ---------------------------------------------------------------------------

// TaskInfo mirrors anvil.TaskInfo in host vocabulary: one row of the session
// tree's background work.
type TaskInfo struct {
	ID string `json:"id"`
	// Kind is "shell" or "agent".
	Kind string `json:"kind"`
	// Title is the command for a shell task, the description for an agent task.
	Title  string `json:"title"`
	Status string `json:"status"` // running | finished | failed | cancelled
	// AgentID is the agent whose tool call started the task. For an agent task
	// it is the starter, not the child: the child does not exist yet when the
	// row is created.
	AgentID          string    `json:"agent_id,omitempty"`
	ChildAgentID     string    `json:"child_agent_id,omitempty"`
	ParentRunID      string    `json:"parent_run_id,omitempty"`
	ParentToolCallID string    `json:"parent_tool_call_id,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	// EndedAt is absent while the task runs.
	EndedAt *time.Time `json:"ended_at,omitempty"`
	// ExitCode is the process exit code of a shell task, -1 when it did not
	// exit on its own. It is 0 for an agent task.
	ExitCode int `json:"exit_code"`
}

// TasksListParams names the session whose tree to report. A task id from
// another session's tree is never served.
type TasksListParams struct {
	SessionID string `json:"session_id"`
}

// TasksListResult is the whole tree's tasks in start order.
type TasksListResult struct {
	Tasks []TaskInfo `json:"tasks"`
}

// TasksOutputParams pages one task's output. Offset is a byte offset into the
// ring; Limit caps the chunk (0 means the host's per-call cap).
type TasksOutputParams struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// TasksOutputResult is one read. Truncated says the requested offset had
// already been dropped, so the client shows a gap rather than pretending the
// output starts where it asked.
type TasksOutputResult struct {
	Status      string `json:"status"`
	ExitCode    int    `json:"exit_code"`
	Output      string `json:"output,omitempty"`
	NextOffset  int    `json:"next_offset"`
	FirstOffset int    `json:"first_offset"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// TasksCancelParams names the task to stop. Cancelling a task that already
// ended answers its final state and no error.
type TasksCancelParams struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
}

// TasksCancelResult is the task's state after the cancel.
type TasksCancelResult struct {
	Task TaskInfo `json:"task"`
}

// TaskNotification reports one task changing state. It is additive: a client
// that does not know the method ignores it, and one that never asks
// tasks.list still learns nothing it did not ask for.
type TaskNotification struct {
	SessionID string   `json:"session_id"`
	Task      TaskInfo `json:"task"`
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
	// Usage is authoritative on a forwarded terminal error. On streamed events
	// it may be a live estimate, explicitly marked by UsageEstimated. The
	// authoritative end-of-run usage is still the `done` notification.
	Usage          *Usage `json:"usage,omitempty"`
	UsageEstimated bool   `json:"usage_estimated,omitempty"`
	// Rule is the rule that decided this tool result, when one did. It is how a
	// call that ran without a prompt says why it did not need one: nil means no
	// rule decided it, which is not the same as "some rule allowed it".
	Rule *RuleInfo `json:"rule,omitempty"`
	// Protected are the paths this call touched that an allow rule may not cover
	// (0.46+), set on a tool_result whether or not a rule decided it. It is how
	// the record says after the fact why a remembered allow did not cover the
	// call — including where bypass ran it all the same.
	Protected []string `json:"protected,omitempty"`
	// Items is the complete task list on a `todo` event (0.40.9). It is a
	// pointer so that an empty list reaches the client as [] — clearing the
	// panel — while every other event omits the field entirely.
	Items *[]TodoItem `json:"items,omitempty"`
	// Compact is set on a `compact` event (0.43+): the runtime folded older
	// turns into a summary before the next request. It is nil on every other
	// event, and a manual compaction arrives as a `compact` notification with
	// the same inner shape.
	Compact *CompactInfo `json:"compact,omitempty"`
	// AgentID names the agent that emitted this event: the session's own agent
	// for parent events, the child's id for a subagent's. ParentToolCallID is
	// set only on a child event and names the spawn_agent call it belongs under,
	// which is the key the webview groups its steps by. Depth is 0 and omitted
	// for the session's own agent (0.42+).
	AgentID          string `json:"agent_id,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
	Depth            int    `json:"depth,omitempty"`
}

// TodoItem is one row of the model's plan.
type TodoItem struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// CompactInfo is one compaction, live. It is what a client draws as the
// transcript divider; the replay reads the summary record instead.
type CompactInfo struct {
	// Reason is "auto" for the runtime's budget check and "manual" for a person
	// asking through session.compact.
	Reason     string `json:"reason"`
	Before     int    `json:"before"`
	After      int    `json:"after"`
	Summarized int    `json:"summarized"`
	// Window is the declared window in tokens; 0 for a manual compaction on a
	// session that never declared one.
	Window int `json:"window,omitempty"`
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
	// Context is where the conversation stands after the run, including any
	// compaction the run itself performed. A meter that redraws from this never
	// has to follow up with session.context.
	Context *ContextInfo `json:"context,omitempty"`
	// LastRequestAt is the wall-clock time of the run's last model request, RFC
	// 3339 in UTC. Empty when the run never reached the provider; a client shows
	// no cache countdown for that run rather than a wrong one.
	LastRequestAt string `json:"last_request_at,omitempty"`
}

// Usage carries provider token counts when the provider reports them.
//
// CachedTokens is a subset of the input on OpenAI-compatible providers and a
// separate count on Anthropic, whose input_tokens excludes it. Both stay zero
// when the provider reported nothing: zero is "unknown", not "no cache".
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
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
