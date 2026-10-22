// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package tasks owns the background work of one agent tree: shell commands the
// model started without waiting, and subagents it started the same way.
//
// One table holds both kinds because a client asks one question — "what is
// running?" — and because ownership is the same: a task belongs to the agent
// whose tool call started it, and to the root tree that agent lives in. See
// doc/design/v0.x/41-subagents-and-tasks.md.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/id"
)

// Kind is what a background task runs.
type Kind string

const (
	KindShell Kind = "shell"
	KindAgent Kind = "agent"
)

// Status is a task's lifecycle state.
type Status string

const (
	StatusRunning   Status = "running"
	StatusFinished  Status = "finished"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// DefaultLimit is how many tasks may run at once when no limit was configured.
const DefaultLimit = 4

// Info is one task's snapshot.
type Info struct {
	ID        string
	Kind      Kind
	Title     string
	Status    Status
	AgentID   string
	StartedAt time.Time
	EndedAt   time.Time
	// ExitCode is the process exit code for a shell task; -1 when the process
	// did not exit on its own. It is 0 for an agent task.
	ExitCode int
}

// ShellSpec describes one background shell command.
type ShellSpec struct {
	Title   string
	Command string
	CWD     string
	// AgentID is the agent whose tool call started the task; the table is
	// tree-wide, and this is what keeps a child's job attributable.
	AgentID string
	// Timeout bounds the process. Zero means no limit, which no caller uses.
	Timeout time.Duration
	// Prepare is the platform hook that makes cancellation kill the process
	// tree (tools.configureShellCommand).
	Prepare func(*exec.Cmd)
}

// AgentSpec describes one background subagent.
type AgentSpec struct {
	Title   string
	AgentID string
	// Run performs the work. status is a run status ("finished", "cancelled",
	// "error"); message is what the task's output returns. The runner observes
	// ctx, so cancelling the task stops the agent.
	Run func(ctx context.Context) (status, message string, err error)
}

var (
	// ErrCancelled is the cause set when Cancel stops a task.
	ErrCancelled = errors.New("task cancelled")
	// ErrClosed is the cause set when Close stops the tree's tasks.
	ErrClosed = errors.New("task stopped when the session closed")
	// ErrTimeout is the cause set when a shell task reaches its timeout.
	ErrTimeout = errors.New("task timed out")
)

type task struct {
	Info
	ring   *ring
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// Manager is the tree's task table. It is safe for concurrent use.
type Manager struct {
	mu     sync.Mutex
	tasks  map[string]*task
	order  []string
	closed bool
	// Limit bounds concurrently running tasks. It is written once, before any
	// task starts; DefaultLimit applies while it is zero.
	Limit int
	// onChange, when set, hears about every start and every end.
	onChange func(Info)
}

// NewManager returns an empty table.
func NewManager() *Manager {
	return &Manager{tasks: make(map[string]*task)}
}

// SetOnChange installs the table-change callback. It is called outside the
// manager's lock, once when a task starts and once when it ends.
func (m *Manager) SetOnChange(fn func(Info)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

func (m *Manager) limit() int {
	if m.Limit <= 0 {
		return DefaultLimit
	}
	return m.Limit
}

// start registers a new running task, refusing past the limit. The caller
// starts the goroutine that will call finish.
func (m *Manager) start(kind Kind, title, agentID string, cancel context.CancelCauseFunc) (*task, Info, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, Info{}, errors.New("the session is closing; no new tasks can start")
	}
	running := 0
	for _, t := range m.tasks {
		if t.Status == StatusRunning {
			running++
		}
	}
	if running >= m.limit() {
		limit := m.limit()
		m.mu.Unlock()
		return nil, Info{}, fmt.Errorf("this session already has %d background tasks running; raise Subagents.MaxBackground to allow more", limit)
	}
	t := &task{
		Info: Info{
			ID:        "task-" + id.New(),
			Kind:      kind,
			Title:     title,
			Status:    StatusRunning,
			AgentID:   agentID,
			StartedAt: time.Now().UTC(),
		},
		ring:   newRing(),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	m.tasks[t.ID] = t
	m.order = append(m.order, t.ID)
	info := t.Info
	hook := m.onChange
	m.mu.Unlock()
	if hook != nil {
		hook(info)
	}
	return t, info, nil
}

// StartShell starts a command in the background and returns at once.
func (m *Manager) StartShell(spec ShellSpec) (Info, error) {
	if spec.Command == "" {
		return Info{}, errors.New("command is required")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	t, info, err := m.start(KindShell, spec.Title, spec.AgentID, cancel)
	if err != nil {
		cancel(nil)
		return Info{}, err
	}
	go m.runShell(ctx, t, spec)
	return info, nil
}

// StartAgent starts a subagent in the background and returns at once.
func (m *Manager) StartAgent(spec AgentSpec) (Info, error) {
	if spec.Run == nil {
		return Info{}, errors.New("agent runner is required")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	t, info, err := m.start(KindAgent, spec.Title, spec.AgentID, cancel)
	if err != nil {
		cancel(nil)
		return Info{}, err
	}
	go m.runAgent(ctx, t, spec)
	return info, nil
}

func (m *Manager) runShell(base context.Context, t *task, spec ShellSpec) {
	ctx := base
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(base, spec.Timeout, ErrTimeout)
		defer cancel()
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", spec.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", spec.Command)
	}
	cmd.Dir = spec.CWD
	if spec.Prepare != nil {
		spec.Prepare(cmd)
	}
	cmd.Stdout = t.ring
	cmd.Stderr = t.ring

	err := cmd.Start()
	if err == nil {
		err = cmd.Wait()
	}

	status := StatusFinished
	exit := 0
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, ErrTimeout):
		t.ring.appendText(fmt.Sprintf("\ncommand killed after %s", spec.Timeout))
		status = StatusFailed
		exit = -1
	case errors.Is(cause, ErrCancelled), errors.Is(cause, ErrClosed):
		// A reader that polls a cancelled task should not be left wondering why
		// the output stops mid-line.
		if errors.Is(cause, ErrClosed) {
			t.ring.appendText("\ncommand stopped when the session closed")
		} else {
			t.ring.appendText("\ncommand cancelled")
		}
		status = StatusCancelled
		exit = -1
	case err != nil:
		status = StatusFailed
		if cmd.ProcessState != nil {
			exit = cmd.ProcessState.ExitCode()
		} else {
			exit = -1
		}
	}
	m.finish(t, status, exit, "")
}

func (m *Manager) runAgent(ctx context.Context, t *task, spec AgentSpec) {
	status, message, err := spec.Run(ctx)
	st := StatusFailed
	switch status {
	case "finished":
		st = StatusFinished
	case "cancelled":
		st = StatusCancelled
	}
	if err != nil && message == "" {
		message = err.Error()
	}
	m.finish(t, st, 0, message)
}

// finish records a task's terminal state and tells the hook.
func (m *Manager) finish(t *task, status Status, exit int, output string) {
	if output != "" {
		t.ring.appendText(output)
	}
	m.mu.Lock()
	// A terminal state is written once: a task that was already cancelled by
	// Close keeps cancelled even if its process later exits on its own.
	if t.Status == StatusRunning {
		t.Status = status
		t.ExitCode = exit
		t.EndedAt = time.Now().UTC()
	}
	info := t.Info
	hook := m.onChange
	m.mu.Unlock()
	close(t.done)
	if hook != nil {
		hook(info)
	}
}

// List returns the table in start order.
func (m *Manager) List() []Info {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, taskID := range m.order {
		if t := m.tasks[taskID]; t != nil {
			out = append(out, t.Info)
		}
	}
	return out
}

// Get returns one task's snapshot.
func (m *Manager) Get(taskID string) (Info, error) {
	if m == nil {
		return Info{}, fmt.Errorf("unknown task: %s", taskID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[taskID]
	if t == nil {
		return Info{}, fmt.Errorf("unknown task: %s", taskID)
	}
	return t.Info, nil
}

// Output reads a task's output from offset, at most limit bytes (0 means the
// per-call cap).
func (m *Manager) Output(taskID string, offset, limit int) (Output, error) {
	if m == nil {
		return Output{}, fmt.Errorf("unknown task: %s", taskID)
	}
	m.mu.Lock()
	t := m.tasks[taskID]
	m.mu.Unlock()
	if t == nil {
		return Output{}, fmt.Errorf("unknown task: %s", taskID)
	}
	return t.ring.snapshot(offset, limit), nil
}

// Cancel stops a running task. A task that already ended is returned as it is:
// asking twice is not a failure.
func (m *Manager) Cancel(taskID string) (Info, error) {
	if m == nil {
		return Info{}, fmt.Errorf("unknown task: %s", taskID)
	}
	m.mu.Lock()
	t := m.tasks[taskID]
	if t == nil {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("unknown task: %s", taskID)
	}
	running := t.Status == StatusRunning
	cancel := t.cancel
	m.mu.Unlock()
	if !running {
		return t.Info, nil
	}
	if cancel != nil {
		cancel(ErrCancelled)
	}
	// The task tools answer "the status after the cancel", so give the process
	// a moment to observe it. A process that ignores SIGKILL does not hold this
	// open for ever.
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return t.Info, nil
}

// Close cancels everything still running and waits up to grace for the
// goroutines to observe it. The table keeps its rows: a client that asks after
// the close sees cancelled tasks rather than work that vanished.
func (m *Manager) Close(grace time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	var waiting []*task
	for _, t := range m.tasks {
		if t.Status == StatusRunning {
			waiting = append(waiting, t)
		}
	}
	m.mu.Unlock()
	for _, t := range waiting {
		if t.cancel != nil {
			t.cancel(ErrClosed)
		}
	}
	deadline := time.Now().Add(grace)
	for _, t := range waiting {
		remain := time.Until(deadline)
		if remain <= 0 {
			return
		}
		timer := time.NewTimer(remain)
		select {
		case <-t.done:
			timer.Stop()
		case <-timer.C:
			return
		}
	}
}
