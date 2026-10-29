// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tasks

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func echoCommand() string {
	return "echo hello"
}

func sleepCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 60 127.0.0.1 > nul"
	}
	return "sleep 60"
}

// spinCommand loops a builtin with no child process. The timeout test uses it
// because a grandchild that outlives the shell keeps the output pipe open; in
// production Prepare is what kills the tree (tools.configureShellCommand), and
// this test exercises the manager, not the platform hook.
func spinCommand() string {
	if runtime.GOOS == "windows" {
		return "for /L %i in (1,1,100000000) do @echo x"
	}
	return "while true; do echo x; done"
}

// waitStatus polls until the task is no longer running, so a test reads a
// stable row instead of racing the goroutine that ends it.
func waitStatus(t *testing.T, m *Manager, id string) Info {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		info, err := m.Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if info.Status != StatusRunning {
			return info
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return Info{}
}

func TestShellTaskRunsAndOutputIsReadable(t *testing.T) {
	m := NewManager()
	info, err := m.StartShell(ShellSpec{Title: "echo", Command: echoCommand(), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("StartShell: %v", err)
	}
	if info.Kind != KindShell || info.Status != StatusRunning {
		t.Fatalf("start snapshot = %+v", info)
	}
	final := waitStatus(t, m, info.ID)
	if final.Status != StatusFinished {
		t.Fatalf("status = %s, want finished", final.Status)
	}
	if final.ExitCode != 0 {
		t.Fatalf("exit = %d", final.ExitCode)
	}
	out, err := m.Output(info.ID, 0, 0)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if !strings.Contains(out.Text, "hello") {
		t.Fatalf("output = %q", out.Text)
	}
}

func TestOutputOffsetsAndTruncation(t *testing.T) {
	m := NewManager()
	info, err := m.StartShell(ShellSpec{Command: echoCommand(), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("StartShell: %v", err)
	}
	waitStatus(t, m, info.ID)

	first, err := m.Output(info.ID, 0, 2)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if len(first.Text) != 2 || first.NextOffset != 2 || !first.More {
		t.Fatalf("first chunk = %+v", first)
	}
	rest, err := m.Output(info.ID, int(first.NextOffset), 0)
	if err != nil {
		t.Fatalf("Output rest: %v", err)
	}
	if rest.Text == "" || rest.NextOffset <= first.NextOffset || rest.More {
		t.Fatalf("rest chunk = %+v", rest)
	}
	if !strings.HasPrefix(first.Text+rest.Text, "hello") {
		t.Fatalf("joined output = %q", first.Text+rest.Text)
	}

	// A ring keeps its newest MaxOutputBytes; asking for what was dropped is
	// reported, not silently skipped.
	r := newRing()
	chunk := make([]byte, 64<<10)
	for i := range chunk {
		chunk[i] = 'x'
	}
	for written := 0; written < MaxOutputBytes+3*len(chunk); written += len(chunk) {
		if _, err := r.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	dropped := r.snapshot(0, 0)
	if !dropped.Truncated || dropped.FirstOffset == 0 {
		t.Fatalf("expected truncation, got %+v", dropped)
	}
	if int64(len(dropped.Text)) > MaxChunkBytes {
		t.Fatalf("one read returned %d bytes", len(dropped.Text))
	}
}

func TestCancelStopsShellTask(t *testing.T) {
	m := NewManager()
	info, err := m.StartShell(ShellSpec{Command: sleepCommand(), Timeout: 60 * time.Second})
	if err != nil {
		t.Fatalf("StartShell: %v", err)
	}
	cancelled, err := m.Cancel(info.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("status after cancel = %s", cancelled.Status)
	}
	if cancelled.ExitCode != -1 {
		t.Fatalf("exit after cancel = %d", cancelled.ExitCode)
	}
	// Cancelling again is not an error.
	again, err := m.Cancel(info.ID)
	if err != nil || again.Status != StatusCancelled {
		t.Fatalf("second cancel = %+v, %v", again, err)
	}
}

func TestShellTimeoutIsFailureWithNote(t *testing.T) {
	m := NewManager()
	info, err := m.StartShell(ShellSpec{Command: spinCommand(), Timeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatalf("StartShell: %v", err)
	}
	final := waitStatus(t, m, info.ID)
	if final.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", final.Status)
	}
	var output strings.Builder
	for offset := 0; ; {
		out, err := m.Output(info.ID, offset, 0)
		if err != nil {
			t.Fatalf("Output: %v", err)
		}
		output.WriteString(out.Text)
		if !out.More {
			break
		}
		offset = int(out.NextOffset)
	}
	if !strings.Contains(output.String(), "command killed after 150ms") {
		t.Fatalf("output does not contain the timeout note")
	}
}

func TestLimitRefusesWithSentence(t *testing.T) {
	m := NewManager()
	m.Limit = 1
	first, err := m.StartShell(ShellSpec{Command: sleepCommand(), Timeout: 60 * time.Second})
	if err != nil {
		t.Fatalf("StartShell: %v", err)
	}
	defer m.Cancel(first.ID)

	_, err = m.StartShell(ShellSpec{Command: echoCommand(), Timeout: time.Second})
	if err == nil {
		t.Fatal("second start should be refused")
	}
	if !strings.Contains(err.Error(), "MaxBackground") {
		t.Fatalf("error = %q", err)
	}
}

func TestAgentTaskOutputIsItsMessage(t *testing.T) {
	m := NewManager()
	info, err := m.StartAgent(AgentSpec{
		Title:   "subagent find calls",
		AgentID: "agent-parent",
		Run: func(ctx context.Context) (string, string, error) {
			return "finished", "the answer", nil
		},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	final := waitStatus(t, m, info.ID)
	if final.Kind != KindAgent || final.Status != StatusFinished {
		t.Fatalf("final = %+v", final)
	}
	if final.AgentID != "agent-parent" {
		t.Fatalf("agent id = %q", final.AgentID)
	}
	out, err := m.Output(info.ID, 0, 0)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if out.Text != "the answer" {
		t.Fatalf("output = %q", out.Text)
	}
}

func TestAgentTaskPublishesLineageAndConcreteChild(t *testing.T) {
	m := NewManager()
	release := make(chan struct{})
	started := make(chan string, 1)
	info, err := m.StartAgent(AgentSpec{
		Title:            "subagent inspect graph",
		AgentID:          "agent-parent",
		ParentRunID:      "run-parent",
		ParentToolCallID: "call-spawn",
		Run: func(ctx context.Context) (string, string, error) {
			started <- IDFromContext(ctx)
			<-release
			return "finished", "done", nil
		},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if got := <-started; got != info.ID {
		t.Fatalf("task id in context = %q, want %q", got, info.ID)
	}
	if info.ParentRunID != "run-parent" || info.ParentToolCallID != "call-spawn" {
		t.Fatalf("start lineage = %+v", info)
	}
	if err := m.SetChildAgent(info.ID, "agent-child"); err != nil {
		t.Fatalf("SetChildAgent: %v", err)
	}
	linked, err := m.Get(info.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if linked.AgentID != "agent-parent" || linked.ChildAgentID != "agent-child" {
		t.Fatalf("linked task = %+v", linked)
	}
	close(release)
	final := waitStatus(t, m, info.ID)
	if final.ChildAgentID != "agent-child" {
		t.Fatalf("terminal child = %q", final.ChildAgentID)
	}
}

func TestCloseCancelsRunningTasks(t *testing.T) {
	m := NewManager()
	info, err := m.StartShell(ShellSpec{Command: sleepCommand(), Timeout: 60 * time.Second})
	if err != nil {
		t.Fatalf("StartShell: %v", err)
	}
	start := time.Now()
	m.Close(2 * time.Second)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Close took %s", elapsed)
	}
	after, err := m.Get(info.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Status == StatusRunning {
		t.Fatalf("task still running after Close")
	}
	if _, err := m.StartShell(ShellSpec{Command: echoCommand()}); err == nil {
		t.Fatal("StartShell after Close should be refused")
	}
}

func TestUnknownTaskIsAnErrorNamingIt(t *testing.T) {
	m := NewManager()
	if _, err := m.Output("task-nope", 0, 0); err == nil || !strings.Contains(err.Error(), "task-nope") {
		t.Fatalf("Output error = %v", err)
	}
	if _, err := m.Cancel("task-nope"); err == nil || !strings.Contains(err.Error(), "task-nope") {
		t.Fatalf("Cancel error = %v", err)
	}
}
