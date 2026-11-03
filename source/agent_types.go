// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"encoding/json"
	"time"
)

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
	// EventTodo carries the model's complete task list (0.40.9), emitted when a
	// todo_write call succeeds. The call is also an ordinary tool message, so a
	// replayed conversation rebuilds the list from the stored arguments; this
	// event is what a live reader receives.
	EventTodo EventType = "todo"
	// EventCompact reports one history compaction (0.43+): what was folded, and
	// the estimate on either side. The summary itself is stored, so a replayed
	// conversation draws the same boundary without this event.
	EventCompact EventType = "compact"
	// EventRetry reports that a provider call failed and will be tried again (0.48+).
	// Attempt is the attempt that failed, Delay the wait before the next, Text the
	// provider's message. It lets a client show "provider busy, retrying" instead of a
	// frozen spinner. Since 0.53 it is stored for transcript replay.
	EventRetry EventType = "retry"
	// EventTruncated reports that the provider cut the final answer off at the output
	// limit (0.48+). It comes just before EventDone, which still follows; the partial
	// answer is the result. Nothing is re-requested: a container decides what to do.
	EventTruncated EventType = "truncated"
	// EventStage is a durable orchestration node update (0.52+). It is emitted
	// by Multi delegation and Complex stages and persisted for transcript replay.
	EventStage EventType = "stage"
)

// CompactInfo is one history compaction's numbers, set on EventCompact.
type CompactInfo struct {
	// Reason is "auto" when the budget triggered it or "manual" when a caller
	// asked. A manual compaction from Agent.Compact is reported by its result;
	// this field is what a runtime compaction says about itself.
	Reason     string
	Before     int
	After      int
	Summarized int
	Window     int
}

// TodoItem is one row of the model's plan.
type TodoItem struct {
	ID         string
	Text       string
	Status     string
	WorkItemID string
}

// Task-list statuses. A todo_write call names one of these for every row.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoDone       = "done"
)

// BriefView is the presentation-safe part of a validated worker brief.
type BriefView struct {
	Goal       string   `json:"goal"`
	Plan       []string `json:"plan"`
	Files      []string `json:"files"`
	Acceptance []string `json:"acceptance"`
}

type AcceptanceStatus string

const (
	AcceptanceMet        AcceptanceStatus = "met"
	AcceptanceNotMet     AcceptanceStatus = "not_met"
	AcceptanceUnreported AcceptanceStatus = "unreported"
)

type AcceptanceItem struct {
	Text   string           `json:"text"`
	Status AcceptanceStatus `json:"status"`
}

// AcceptanceView keeps model claims separate from checkpoint-derived changes.
type AcceptanceView struct {
	Items           []AcceptanceItem `json:"items"`
	Changed         []string         `json:"changed,omitempty"`
	ChangesRecorded bool             `json:"changes_recorded"`
}

// StageEvent is one stable node in Multi or Complex orchestration.
type StageEvent struct {
	NodeID               string          `json:"node_id"`
	ParentNodeID         string          `json:"parent_node_id,omitempty"`
	DependsOn            []string        `json:"depends_on,omitempty"`
	Role                 Role            `json:"agent_role"`
	Stage                string          `json:"stage"`
	AgentID              string          `json:"agent_id,omitempty"`
	Brief                *BriefView      `json:"brief,omitempty"`
	Acceptance           *AcceptanceView `json:"acceptance,omitempty"`
	WorkerID             string          `json:"worker_id,omitempty"`
	WorktreeLabel        string          `json:"worktree_label,omitempty"`
	WorkspaceFingerprint string          `json:"workspace_fingerprint,omitempty"`
	BaseCommit           string          `json:"base_commit,omitempty"`
}

// Usage holds optional token counts from the provider.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	// CachedTokens is how many input tokens were served from a prompt cache
	// (0.43+). Zero means "the provider did not say": an endpoint or gateway
	// that does not report caching leaves it zero, which is not the same fact
	// as a cache miss.
	CachedTokens int
	// CacheWriteTokens is how many input tokens were written into a cache
	// (Anthropic only, 0.43+).
	CacheWriteTokens int
}

// Code is a stable machine-readable run failure class (0.7+).
type Code string

const (
	CodeMaxSteps        Code = "max_steps"
	CodeCancelled       Code = "cancelled"
	CodeContextDeadline Code = "context_deadline"
	CodeMaxWallTime     Code = "max_wall_time"
	CodeProvider        Code = "provider"
	// CodeProviderMidStream is a provider failure after text had reached the user.
	// It is never retried: a retry would show the answer twice.
	CodeProviderMidStream Code = "provider_mid_stream"
	// CodeProviderBusy is a rate limit or transient server failure that
	// outlasted the retries.
	CodeProviderBusy Code = "provider_busy"
	// CodeRequestRejected is a 4xx that names the request itself: an unknown parameter,
	// a context overflow, an invalid tool schema. The provider's text is in Err.
	CodeRequestRejected Code = "request_rejected"
	// CodeAuth is a 401 or 403.
	CodeAuth      Code = "auth"
	CodeToolPanic Code = "tool_panic"
	// CodeEmptyResponse is a provider that kept returning neither text nor a tool call
	// after bounded recovery. CodeFinishProtocol is a run that required finish_task and
	// never called it.
	CodeEmptyResponse  Code = "empty_response"
	CodeFinishProtocol Code = "finish_protocol"
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
	// Truncated is true when the provider cut the final answer off at the output limit
	// (0.48+). The run still finished and Result is the partial answer.
	Truncated bool
	// LastRequestAt is when the run's most recent model request completed
	// (0.43+). Zero when the run never reached the provider. A client uses it
	// with a TTL it knows to draw a cache countdown; the SDK does not know any
	// provider's TTL.
	LastRequestAt time.Time
}

// Event is one streamed unit from a Run.
type Event struct {
	// Seq is the durable per-run sequence. Zero means the event is intentionally
	// live-only (for example reasoning or progress).
	Seq        uint64
	Type       EventType
	Text       string
	ToolCallID string
	Name       string
	Args       json.RawMessage
	Content    string
	IsError    bool
	Step       int  // 1-based LLM round; set on EventProgress
	MaxSteps   int  // configured limit; set on EventProgress
	Code       Code // set on EventError when classifiable (0.7+)
	// Attempt, MaxAttempts and Delay are set on EventRetry (0.53+): the attempt
	// that failed, the resolved maximum, and the wait before the next one.
	Attempt     int
	MaxAttempts int
	Delay       time.Duration
	// Usage is authoritative on terminal events (set when the provider reported any
	// counts; nil means "unknown", which a client must not draw as zero). While a
	// streamed model request is in flight it may carry a live estimate, explicitly marked
	// by UsageEstimated; clients must replace that estimate with RunResult.Usage.
	Usage          *Usage
	UsageEstimated bool
	// Rule is the workspace rule that decided this call, set on EventToolResult
	// when one did (0.36+). It is nil for a call no rule decided — an ungated
	// read, a call the mode gate allowed untouched, or one a callback approved —
	// so a client shows "allowed by rule 2" only where a rule is really why.
	Rule *PermissionRuleMatch
	// Protected are the paths this call touched that an allow rule may not cover
	// (0.46+), set on EventToolResult. It is a fact about the call, not about a
	// rule: it is set even when no rule decided, so a client can say why a
	// remembered allow did not cover a `.git` write. Nil on every other event.
	Protected []string
	// PathAnalysis and PathProblem carry the shell analyzer's finding on a
	// tool_result. Empty for non-shell tools.
	PathAnalysis string
	PathProblem  string
	// Stage is set only on EventStage. Unlike ordinary streamed progress it is
	// persisted and therefore has an identical transcript representation.
	Stage *StageEvent
	// Structured workflow identity (0.55+). Empty means this event is not
	// explicitly linked; clients must never infer a link from matching text.
	ArtifactID  string
	WorkItemID  string
	ExecutionID string
	// Worktree identity (0.57+) is stamped by the verified host route. Empty
	// means this is a same-workspace event; clients never infer it from cwd.
	WorkerID             string
	WorktreeLabel        string
	WorkspaceFingerprint string
	BaseCommit           string
	// Items is the model's task list, set on EventTodo (0.40.9) and nil on every
	// other event. It is the complete list the call wrote, not a change: a
	// client replaces its checklist, and an empty list means the call cleared it.
	Items []TodoItem
	// Compact is what a history compaction folded away, set on EventCompact
	// (0.43+) and nil on every other event.
	Compact *CompactInfo
	// LastRequestAt is when the most recent model request completed, set on the
	// terminal event (done or error) (0.43+). Zero when the run never reached
	// the provider.
	LastRequestAt time.Time
}
