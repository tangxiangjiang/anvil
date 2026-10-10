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
	KindClusterJoin      = "cluster.join"
	KindClusterJoinOK    = "cluster.join_ok"
	KindClusterHeartbeat = "cluster.heartbeat"
	KindClusterAssign    = "cluster.assign"
	KindClusterDone      = "cluster.done"
	KindClusterConfig    = "cluster.config"
	KindClusterControl   = "cluster.control"
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

// WorkerState is one capacity slot.
type WorkerState struct {
	ID     string `json:"id"`
	Status string `json:"status"` // idle|busy|starting|unhealthy
	JobID  string `json:"job_id,omitempty"`
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
	JobID      string   `json:"job_id"`
	RepoID     string   `json:"repo_id"`
	Prompt     string   `json:"prompt"`
	ResumeID   string   `json:"resume_id,omitempty"`
	MaxSteps   int      `json:"max_steps,omitempty"`
	Model      string   `json:"model,omitempty"`
	Provider   string   `json:"provider,omitempty"`
	Plugins    []string `json:"plugins,omitempty"` // 0.20+ intersected with daemon policy
	ForceStub  bool     `json:"force_stub,omitempty"` // test: skip LLM
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
	JobID  string      `json:"job_id"`
	Status string      `json:"status"` // finished|error|cancelled
	Result string      `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
	Code   string      `json:"code,omitempty"`
	Steps  int         `json:"steps,omitempty"`
	Usage  *TokenUsage `json:"usage,omitempty"`
}

// ErrorBody is a protocol-level error.
type ErrorBody struct {
	Message string `json:"message"`
	JobID   string `json:"job_id,omitempty"`
}

// ClusterJoinBody is sent to a scheduler (p_0.11.4).
type ClusterJoinBody struct {
	NodeID     string `json:"node_id"`
	Token      string `json:"token"`
	MaxWorkers int    `json:"max_workers"`
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
