// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/tasks"
)

// spinCommand loops a builtin with no child process, so killing the shell ends
// the task promptly without depending on the platform process-tree hook.
func spinCommand() string {
	if runtime.GOOS == "windows" {
		return "for /L %i in (1,1,100000000) do @echo x"
	}
	return "while true; do echo x; done"
}

// backgroundSleepCommand keeps a background shell task running long enough for
// a second call in the same turn to see it.
func backgroundSleepCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 30 127.0.0.1 > nul"
	}
	return "sleep 30"
}

func taskIDFromResult(s string) string {
	fields := strings.Fields(s)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "task-") {
		return fields[0]
	}
	return ""
}

// waitTask polls the agent's table until the task leaves running, so assertions
// read a finished row rather than racing the goroutine that ends it.
func waitTask(t *testing.T, a *Agent, id string) TaskInfo {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, info := range a.Tasks() {
			if info.ID == id && info.Status != TaskStatusRunning {
				return info
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return TaskInfo{}
}

// TestBackgroundShellThroughTheTool is the model-visible path: shell returns a
// task id at once, the turn ends, and the task is still readable afterwards.
func TestBackgroundShellThroughTheTool(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if len(trailingToolResults(req)) > 0 {
			return textResponse("parent done"), nil
		}
		return callResponse("call-1", "shell", `{"command":"echo hello","run_in_background":true}`), nil
	})

	run, err := parent.Send(context.Background(), "start a job")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "parent done" {
		t.Fatalf("status=%s result=%q err=%s", res.Status, res.Result, res.Err)
	}

	tr := findToolResult(events, "shell")
	if tr == nil {
		t.Fatal("no shell tool result")
	}
	if tr.IsError {
		t.Fatalf("background shell failed: %s", tr.Content)
	}
	if !strings.Contains(tr.Content, "started in the background: echo hello") {
		t.Fatalf("tool result = %q", tr.Content)
	}
	id := taskIDFromResult(tr.Content)
	if id == "" {
		t.Fatalf("no task id in %q", tr.Content)
	}

	final := waitTask(t, parent, id)
	if final.Kind != TaskKindShell || final.Status != TaskStatusFinished || final.ExitCode != 0 {
		t.Fatalf("final task = %+v", final)
	}
	if final.AgentID != parent.id {
		t.Fatalf("AgentID = %q, want %q", final.AgentID, parent.id)
	}
	out, err := parent.TaskOutput(id, 0, 0)
	if err != nil {
		t.Fatalf("TaskOutput: %v", err)
	}
	if !strings.Contains(out.Text, "hello") {
		t.Fatalf("output = %q", out.Text)
	}
}

// TestTaskToolsReadTheTable exercises the three tools against the same rows the
// embedder API reads, including the error for an unknown id.
func TestTaskToolsReadTheTable(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	ctx := context.Background()

	started := parent.registry.Execute(ctx, "shell", []byte(`{"command":"echo hello","run_in_background":true}`))
	if started.IsError {
		t.Fatalf("background shell: %s", started.Content)
	}
	id := taskIDFromResult(started.Content)
	if id == "" {
		t.Fatalf("no task id in %q", started.Content)
	}

	list := parent.registry.Execute(ctx, "task_list", nil)
	if list.IsError {
		t.Fatalf("task_list: %s", list.Content)
	}
	for _, want := range []string{id, "shell", parent.id, "echo hello"} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("task_list = %q, missing %q", list.Content, want)
		}
	}

	waitTask(t, parent, id)
	out := parent.registry.Execute(ctx, "task_output", []byte(fmt.Sprintf(`{"task_id":%q}`, id)))
	if out.IsError {
		t.Fatalf("task_output: %s", out.Content)
	}
	for _, want := range []string{id, "finished", "(exit 0)", "hello"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("task_output = %q, missing %q", out.Content, want)
		}
	}

	cancel := parent.registry.Execute(ctx, "task_cancel", []byte(fmt.Sprintf(`{"task_id":%q}`, id)))
	if cancel.IsError || !strings.Contains(cancel.Content, "finished") {
		t.Fatalf("cancelling an ended task = %q (error=%v)", cancel.Content, cancel.IsError)
	}

	unknown := parent.registry.Execute(ctx, "task_output", []byte(`{"task_id":"task-nope"}`))
	if !unknown.IsError || !strings.Contains(unknown.Content, "task-nope") {
		t.Fatalf("unknown id = %q (error=%v)", unknown.Content, unknown.IsError)
	}
	if _, err := parent.CancelTask("task-nope"); err == nil || !strings.Contains(err.Error(), "task-nope") {
		t.Fatalf("CancelTask unknown id = %v", err)
	}
}

// TestBackgroundSubagent checks the whole background-subagent path: the tool
// returns a task id, the child keeps working after the turn, its final message
// is the task output, and its events were forwarded while it ran.
func TestBackgroundSubagent(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var childEvents []SubagentEvent
	opts := subagentTestOptions(dir)
	opts.Subagents.OnEvent = func(ev SubagentEvent) {
		mu.Lock()
		childEvents = append(childEvents, ev)
		mu.Unlock()
	}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if lastUserText(req) == "say hello" {
			return textResponse("child answer"), nil
		}
		if len(trailingToolResults(req)) > 0 {
			return textResponse("parent done"), nil
		}
		return callResponse("call-1", "spawn_agent",
			`{"prompt":"say hello","description":"Greeter","background":true}`), nil
	})

	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "parent done" {
		t.Fatalf("status=%s result=%q err=%s", res.Status, res.Result, res.Err)
	}
	tr := findToolResult(events, "spawn_agent")
	if tr == nil || tr.IsError {
		t.Fatalf("spawn result = %+v", tr)
	}
	if !strings.Contains(tr.Content, "started in the background: subagent Greeter") {
		t.Fatalf("tool result = %q", tr.Content)
	}
	id := taskIDFromResult(tr.Content)
	if id == "" {
		t.Fatalf("no task id in %q", tr.Content)
	}

	final := waitTask(t, parent, id)
	if final.Kind != TaskKindAgent || final.Status != TaskStatusFinished {
		t.Fatalf("final task = %+v", final)
	}
	out, err := parent.TaskOutput(id, 0, 0)
	if err != nil {
		t.Fatalf("TaskOutput: %v", err)
	}
	if out.Text != "child answer" {
		t.Fatalf("task output = %q", out.Text)
	}

	mu.Lock()
	got := append([]SubagentEvent(nil), childEvents...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("OnEvent received nothing from the background child")
	}
	for _, ev := range got {
		if ev.ParentAgentID != parent.id || ev.ToolCallID != "call-1" {
			t.Fatalf("event identity = %+v", ev)
		}
		if ev.AgentID == "" || ev.AgentID == parent.id {
			t.Fatalf("event AgentID = %q", ev.AgentID)
		}
	}
}

// TestMaxBackgroundRefusal checks that the second start past the limit is a
// tool error naming the setting, and that the run continues.
func TestMaxBackgroundRefusal(t *testing.T) {
	dir := t.TempDir()
	opts := subagentTestOptions(dir)
	opts.Subagents.MaxBackground = 1
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch len(trailingToolResults(req)) {
		case 0:
			return callResponse("call-1", "shell",
				fmt.Sprintf(`{"command":%q,"run_in_background":true}`, backgroundSleepCommand())), nil
		case 1:
			return callResponse("call-2", "shell", `{"command":"echo second","run_in_background":true}`), nil
		default:
			return textResponse("parent done"), nil
		}
	})

	run, err := parent.Send(context.Background(), "start two jobs")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "parent done" {
		t.Fatalf("status=%s result=%q err=%s", res.Status, res.Result, res.Err)
	}

	var shellResults []*Event
	for i := range events {
		if events[i].Type == EventToolResult && events[i].Name == "shell" {
			shellResults = append(shellResults, &events[i])
		}
	}
	if len(shellResults) != 2 {
		t.Fatalf("shell results = %d", len(shellResults))
	}
	if shellResults[0].IsError {
		t.Fatalf("first start failed: %s", shellResults[0].Content)
	}
	second := shellResults[1]
	if !second.IsError || !strings.Contains(second.Content, "MaxBackground") {
		t.Fatalf("second start = %q (error=%v)", second.Content, second.IsError)
	}

	if id := taskIDFromResult(shellResults[0].Content); id != "" {
		if _, err := parent.CancelTask(id); err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
	}
}

// TestAgentCloseCancelsTasks pins the ownership rule: closing the root stops
// its tree's work, and the rows stay visible as cancelled.
func TestAgentCloseCancelsTasks(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	mgr := parent.registry.TaskManager()
	info, err := mgr.StartShell(tasks.ShellSpec{Command: spinCommand(), Timeout: 60 * time.Second})
	if err != nil {
		t.Fatalf("StartShell: %v", err)
	}
	start := time.Now()
	if err := parent.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("Close took %s", elapsed)
	}
	// The table keeps its rows after Close, so the snapshot still says what
	// happened rather than the task vanishing.
	deadline := time.Now().Add(3 * time.Second)
	for {
		after, err := mgr.Get(info.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if after.Status != tasks.StatusRunning {
			if after.Status != tasks.StatusCancelled {
				t.Fatalf("status after Close = %s", after.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task still running after Close")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestChildShellTaskIsVisibleInTheParentsTable checks ownership: a task a
// child starts is a row in the tree's one table, labelled with the child.
func TestChildShellTaskIsVisibleInTheParentsTable(t *testing.T) {
	dir := t.TempDir()
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if lastUserText(req) == "run a job" {
			if len(trailingToolResults(req)) > 0 {
				return textResponse("child done"), nil
			}
			return callResponse("child-call-1", "shell",
				fmt.Sprintf(`{"command":%q,"run_in_background":true}`, backgroundSleepCommand())), nil
		}
		if len(trailingToolResults(req)) > 0 {
			return textResponse("parent done"), nil
		}
		return callResponse("call-1", "spawn_agent",
			`{"prompt":"run a job","description":"Worker","tools":["shell"]}`), nil
	})

	run, err := parent.Send(context.Background(), "parent task")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	tr := findToolResult(events, "spawn_agent")
	if tr == nil || tr.IsError {
		t.Fatalf("spawn result = %+v", tr)
	}
	childID := ""
	if fields := strings.Fields(tr.Content); len(fields) > 0 {
		childID = fields[0]
	}
	if !strings.HasPrefix(childID, "agent-") {
		t.Fatalf("no child id in %q", tr.Content)
	}

	var found *TaskInfo
	for _, info := range parent.Tasks() {
		if info.Kind == TaskKindShell && info.AgentID == childID {
			row := info
			found = &row
		}
	}
	if found == nil {
		t.Fatalf("no shell task owned by %s in %+v", childID, parent.Tasks())
	}
	if strings.Contains(found.Title, "\n") {
		t.Fatalf("task title spans lines: %q", found.Title)
	}
	if _, err := parent.CancelTask(found.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
}

// TestCancelKillsTheProcessTree checks the hook the manager is handed: a
// grandchild that would write a marker after the cancel must not be alive to
// write it. Without Prepare only the shell dies and the marker appears. The
// writer is a script file so the command needs no nested quoting.
func TestCancelKillsTheProcessTree(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	var command string
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "writer.cmd")
		content := "@echo off\r\nping -n 3 127.0.0.1 >nul\r\necho x>marker.txt\r\n"
		if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		command = `.\writer.cmd`
	} else {
		script := filepath.Join(dir, "writer.sh")
		content := "sleep 3\necho x > marker.txt\n"
		if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		command = "sh writer.sh"
	}
	parent, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	started := parent.registry.Execute(context.Background(), "shell",
		[]byte(fmt.Sprintf(`{"command":%q,"run_in_background":true}`, command)))
	if started.IsError {
		t.Fatalf("background shell: %s", started.Content)
	}
	id := taskIDFromResult(started.Content)
	if id == "" {
		t.Fatalf("no task id in %q", started.Content)
	}
	// Let the shell start its child before cancelling.
	time.Sleep(time.Second)
	info, err := parent.CancelTask(id)
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if info.Status != TaskStatusCancelled {
		t.Fatalf("status = %s", info.Status)
	}
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a grandchild survived the cancel and wrote its marker")
	}
}

// TestAgentToolsAreNeverAutoApproved pins the gate: work that creates agents
// or reads their table always gets the call-card decision.
func TestAgentToolsAreNeverAutoApproved(t *testing.T) {
	for _, name := range []string{
		"spawn_agent", "run_workflow", "task_list", "task_output", "task_cancel",
	} {
		if AutoApproveCall(name, nil) {
			t.Errorf("AutoApproveCall(%q) = true; these tools must reach the card", name)
		}
	}
}
