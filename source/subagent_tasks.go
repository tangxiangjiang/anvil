// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"fmt"
	"time"

	"github.com/anvil-dev/anvil/internal/tasks"
)

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

// TaskOutput is one read of a background task's output.
//
// The text is a window into a bounded ring: a shell task keeps its newest
// megabyte, and FirstOffset says which byte that window now starts at. A
// reader that asks for an offset the ring has dropped is told so by Truncated
// rather than receiving a gap.
type TaskOutput struct {
	Text string
	// NextOffset is where the next read should start.
	NextOffset int64
	// FirstOffset is the oldest byte still held.
	FirstOffset int64
	// Truncated is true when the requested offset was older than FirstOffset.
	Truncated bool
	// More is true when the ring holds bytes after Text. A running task may
	// still produce more: NextOffset is the current end either way.
	More bool
}

// Tasks returns the agent tree's background work in start order: shell
// commands and subagents the model started without waiting. A child sees the
// same table as its root, because it is the same table.
func (a *Agent) Tasks() []TaskInfo {
	mgr := a.taskManager()
	if mgr == nil {
		return nil
	}
	infos := mgr.List()
	out := make([]TaskInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, toTaskInfo(info))
	}
	return out
}

// Task returns one background task's snapshot. An unknown id is an error
// naming it.
func (a *Agent) Task(taskID string) (TaskInfo, error) {
	mgr := a.taskManager()
	if mgr == nil {
		return TaskInfo{}, fmt.Errorf("unknown task: %s", taskID)
	}
	info, err := mgr.Get(taskID)
	if err != nil {
		return TaskInfo{}, err
	}
	return toTaskInfo(info), nil
}

// TaskOutput reads a background task's output from offset, at most limit bytes
// (0 means the per-call cap of 64 KiB). It does not wait for the task: a
// running task returns the bytes it has so far and a NextOffset to poll from.
func (a *Agent) TaskOutput(taskID string, offset, limit int) (TaskOutput, error) {
	mgr := a.taskManager()
	if mgr == nil {
		return TaskOutput{}, fmt.Errorf("unknown task: %s", taskID)
	}
	out, err := mgr.Output(taskID, offset, limit)
	if err != nil {
		return TaskOutput{}, err
	}
	return TaskOutput{
		Text:        out.Text,
		NextOffset:  out.NextOffset,
		FirstOffset: out.FirstOffset,
		Truncated:   out.Truncated,
		More:        out.More,
	}, nil
}

// CancelTask stops a running task and returns its status afterwards.
// Cancelling a task that already ended is not an error.
func (a *Agent) CancelTask(taskID string) (TaskInfo, error) {
	mgr := a.taskManager()
	if mgr == nil {
		return TaskInfo{}, fmt.Errorf("unknown task: %s", taskID)
	}
	info, err := mgr.Cancel(taskID)
	if err != nil {
		return TaskInfo{}, err
	}
	return toTaskInfo(info), nil
}

// taskManager reads the table under the lock Close takes when it clears it.
func (a *Agent) taskManager() *tasks.Manager {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.taskMgr
}

func toTaskInfo(info tasks.Info) TaskInfo {
	return TaskInfo{
		ID:        info.ID,
		Kind:      TaskKind(info.Kind),
		Title:     info.Title,
		Status:    TaskStatus(info.Status),
		AgentID:   info.AgentID,
		StartedAt: info.StartedAt,
		EndedAt:   info.EndedAt,
		ExitCode:  info.ExitCode,
	}
}
