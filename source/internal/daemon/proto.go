// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package daemon implements agentd control protocol types, config, and local IPC server.
package daemon

import "encoding/json"

// ProtoVersion is the control protocol version.
const ProtoVersion = 1

// Message kinds (controller ↔ agentd).
const (
	KindHello       = "hello"
	KindHelloOK     = "hello_ok"
	KindHelloFail   = "hello_fail"
	KindWorkerList  = "worker.list"
	KindWorkerListR = "worker.list_result"
	KindJobAssign   = "job.assign"
	KindJobEvent    = "job.event"
	KindJobCancel   = "job.cancel"
	KindJobDone     = "job.done"
	KindError       = "error"

	// Cluster (0.11 edge; 0.24 scheduler llm provision).
	KindClusterJoin           = "cluster.join"
	KindClusterJoinOK         = "cluster.join_ok"
	KindClusterHeartbeat      = "cluster.heartbeat"
	KindClusterAssign         = "cluster.assign"
	KindClusterEvent          = "cluster.event"
	KindClusterDone           = "cluster.done"
	KindClusterConfig         = "cluster.config"
	KindClusterControl        = "cluster.control"
	KindClusterWorkerAdd      = "cluster.worker_add"
	KindClusterWorkerRemove   = "cluster.worker_remove"
	KindClusterWorkerStart    = "cluster.worker_start"
	KindClusterWorkerShutdown = "cluster.worker_shutdown"
	KindClusterWorkerResult   = "cluster.worker_result"
	KindClusterSandboxList    = "cluster.sandbox_list"
	KindClusterSandboxMkdir   = "cluster.sandbox_mkdir"
	KindClusterSandboxResult  = "cluster.sandbox_result"
)

// Envelope is a length-framed JSON control message.
type Envelope struct {
	Proto int             `json:"proto"`
	Kind  string          `json:"kind"`
	ID    string          `json:"id,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
}

// HelloBody is sent by the controller to authenticate.
type HelloBody struct {
	Token        string   `json:"token"`
	Client       string   `json:"client,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// HelloOKBody is the daemon reply after successful auth.
type HelloOKBody struct {
	NodeID       string   `json:"node_id,omitempty"`
	MaxWorkers   int      `json:"max_workers"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// HelloFailBody explains auth failure.
type HelloFailBody struct {
	Message string `json:"message"`
}

// WorkerListBody is an empty request (reserved for filters).
type WorkerListBody struct{}

// WorkerState is one capacity slot (optionally bound to a project).
type WorkerState struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"` // stopped|idle|busy|starting|unhealthy
	JobID       string `json:"job_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	ProjectRel  string `json:"project_rel,omitempty"` // path relative to sandbox_dir
	GitRemote   string `json:"git_remote,omitempty"`
	GitBranch   string `json:"git_branch,omitempty"` // current branch (or short SHA if detached)
	RepoID      string `json:"repo_id,omitempty"`
}

// WorkerListResultBody lists slots and capacity.
type WorkerListResultBody struct {
	MaxWorkers int           `json:"max_workers"`
	Idle       int           `json:"idle"`
	Busy       int           `json:"busy"`
	Workers    []WorkerState `json:"workers"`
}

// JobAssignBody assigns work to an allowlisted repo.
type JobAssignBody struct {
	JobID     string   `json:"job_id"`
	RepoID    string   `json:"repo_id"`
	Prompt    string   `json:"prompt"`
	ResumeID  string   `json:"resume_id,omitempty"`
	MaxSteps  int      `json:"max_steps,omitempty"`
	Model     string   `json:"model,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Plugins   []string `json:"plugins,omitempty"`    // 0.20+ intersected with daemon policy
	ForceStub bool     `json:"force_stub,omitempty"` // test: skip LLM
	// Delivery bind (0.39.5+): pass-through metadata for logs / web; scheduler does not interpret.
	MilestoneID string `json:"milestone_id,omitempty"`
	PhaseID     string `json:"phase_id,omitempty"`
	EngReqID    string `json:"eng_req_id,omitempty"`
	Major       int    `json:"major,omitempty"`
	Minor       int    `json:"minor,omitempty"`
}

// JobEventBody streams one run event to the controller.
type JobEventBody struct {
	JobID      string          `json:"job_id"`
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

// JobCancelBody requests cancellation.
type JobCancelBody struct {
	JobID string `json:"job_id"`
}

// TokenUsage is LLM token counts (0.25+).
type TokenUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// JobDoneBody is the terminal job status.
type JobDoneBody struct {
	JobID   string      `json:"job_id"`
	AgentID string      `json:"agent_id,omitempty"` // for resume_id on the next turn
	Status  string      `json:"status"`             // finished|error|cancelled
	Result  string      `json:"result,omitempty"`
	Error   string      `json:"error,omitempty"`
	Code    string      `json:"code,omitempty"`
	Steps   int         `json:"steps,omitempty"`
	Usage   *TokenUsage `json:"usage,omitempty"`
}

// ErrorBody is a protocol-level error.
type ErrorBody struct {
	Message string `json:"message"`
	JobID   string `json:"job_id,omitempty"`
}

// ClusterJoinBody is sent to a scheduler (p_0.11.4).
type ClusterJoinBody struct {
	NodeID         string `json:"node_id"`
	Token          string `json:"token"`
	MaxWorkers     int    `json:"max_workers"`
	Hostname       string `json:"hostname,omitempty"`
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	WorkspaceToken string `json:"workspace_token,omitempty"` // 0.34+ join a Workspace domain
}

// ClusterLLMBody is LLM defaults pushed by the scheduler (0.24+).
// APIKey is a gateway bearer; never log it.
type ClusterLLMBody struct {
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	MaxSteps int    `json:"max_steps,omitempty"`
}

// ClusterJoinOKBody is the scheduler admission reply (may include llm).
type ClusterJoinOKBody struct {
	LLM *ClusterLLMBody `json:"llm,omitempty"`
}

// ClusterConfigBody pushes live llm updates (cluster.config).
type ClusterConfigBody struct {
	LLM *ClusterLLMBody `json:"llm,omitempty"`
}

// ClusterControlBody scales capacity or drain/enable (0.25+).
type ClusterControlBody struct {
	SetMaxWorkers *int  `json:"set_max_workers,omitempty"`
	Accepting     *bool `json:"accepting,omitempty"`
}

// ClusterHeartbeatBody advertises capacity.
type ClusterHeartbeatBody struct {
	NodeID       string        `json:"node_id"`
	Idle         int           `json:"idle"`
	Busy         int           `json:"busy"`
	Max          int           `json:"max_workers"`
	Accepting    bool          `json:"accepting"`
	Workers      []WorkerState `json:"workers,omitempty"`
	Capabilities []string      `json:"capabilities,omitempty"` // 0.20+ plugin labels
	Hostname     string        `json:"hostname,omitempty"`
	Name         string        `json:"name,omitempty"`
	Description  string        `json:"description,omitempty"`
	SandboxDir   string        `json:"sandbox_dir,omitempty"`
}

// ClusterWorkerAddBody creates a project-bound worker on agentd.
type ClusterWorkerAddBody struct {
	WorkerID     string `json:"worker_id,omitempty"`
	Name         string `json:"name,omitempty"`
	Description  string `json:"description,omitempty"`
	ProjectPath  string `json:"project_path"` // absolute or relative to sandbox_dir
	GitURL       string `json:"git_url,omitempty"`
	InitGit      bool   `json:"init_git,omitempty"` // empty dir → git init (+ optional README)
	Readme       string `json:"readme,omitempty"`
	WorktreeBase string `json:"worktree_base,omitempty"` // existing checkout; project_path = new worktree
	Branch       string `json:"branch,omitempty"`        // new branch for worktree add -b

	// LLM is this worker's own provider settings (0.46). Empty fields fall back to
	// agentd's llm block, so a worker that says nothing behaves exactly as before.
	//
	// It exists because one fleet routinely points at more than one model: a cheap
	// one for bulk phases, an expensive one for the phase that has to think. The
	// daemon-wide block could only express one.
	LLM *WorkerLLMBody `json:"llm,omitempty"`
}

// WorkerLLMBody is a per-worker provider override.
//
// APIKey is deliberately absent: a key travels through the environment
// (APIKeyEnv) or is inherited from agentd, never through a JSON body that would
// end up in workers.json on disk.
type WorkerLLMBody struct {
	Provider  string `json:"provider,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	MaxSteps  int    `json:"max_steps,omitempty"`
}

// ClusterWorkerRemoveBody removes a project worker (stopped or idle).
type ClusterWorkerRemoveBody struct {
	WorkerID string `json:"worker_id"`
}

// ClusterWorkerControlBody starts or shuts down a project worker.
type ClusterWorkerControlBody struct {
	WorkerID string `json:"worker_id"`
}

// ClusterWorkerResultBody is the reply for worker add/remove/start/shutdown.
type ClusterWorkerResultBody struct {
	OK      bool        `json:"ok"`
	Error   string      `json:"error,omitempty"`
	Worker  WorkerState `json:"worker,omitempty"`
	Request string      `json:"request,omitempty"` // add|remove|start|shutdown
}

// ClusterSandboxListBody lists entries under a sandbox-relative path.
type ClusterSandboxListBody struct {
	Path string `json:"path,omitempty"`
}

// ClusterSandboxMkdirBody creates a directory under the sandbox.
type ClusterSandboxMkdirBody struct {
	Path string `json:"path"`
}

// SandboxEntry is one filesystem entry inside the sandbox.
type SandboxEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // relative to sandbox_dir
	IsDir bool   `json:"is_dir"`
	IsGit bool   `json:"is_git,omitempty"`
	Size  int64  `json:"size,omitempty"`
	Mtime int64  `json:"mtime,omitempty"` // unix seconds
}

// ClusterSandboxResultBody is the reply for sandbox list/mkdir.
type ClusterSandboxResultBody struct {
	OK         bool           `json:"ok"`
	Error      string         `json:"error,omitempty"`
	Request    string         `json:"request,omitempty"` // list|mkdir
	SandboxDir string         `json:"sandbox_dir,omitempty"`
	Path       string         `json:"path,omitempty"`
	Entries    []SandboxEntry `json:"entries,omitempty"`
}

// MustMarshalBody encodes v as JSON RawMessage; panics only on programmer error in tests — prefer MarshalBody.
func MarshalBody(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// DecodeBody unmarshals env.Body into dest.
func DecodeBody(env Envelope, dest any) error {
	if len(env.Body) == 0 {
		return nil
	}
	return json.Unmarshal(env.Body, dest)
}
