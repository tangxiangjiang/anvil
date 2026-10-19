// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import "time"

// TaskKind is what a background task runs.
type TaskKind string

const (
	// TaskKindShell is a command started by `shell` with run_in_background.
	TaskKindShell TaskKind = "shell"
	// TaskKindAgent is a subagent (or workflow) started in the background.
	TaskKindAgent TaskKind = "agent"
)

// TaskStatus is a background task's lifecycle state.
type TaskStatus string

const (
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusFinished  TaskStatus = "finished"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// TaskInfo is one row of the agent tree's background work.
//
// It is a snapshot: the fields are read when the caller asks, and a task that
// changes afterwards is a different snapshot. OnTask is how a caller hears that
// it changed.
type TaskInfo struct {
	ID string
	// Kind is shell or agent.
	Kind TaskKind
	// Title is the command for a shell task, the brief's description for an
	// agent task.
	Title string
	// Status is running, finished, failed, or cancelled.
	Status  TaskStatus
	AgentID string
	// StartedAt and EndedAt are UTC. EndedAt is zero while the task runs.
	StartedAt time.Time
	EndedAt   time.Time
	// ExitCode is the process exit code for a shell task, -1 when the process
	// did not exit on its own. It is 0 for an agent task.
	ExitCode int
}
