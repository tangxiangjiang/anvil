// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/tasks"
)

// The three task tools read and steer the agent tree's background table. They
// do not start work: shell's run_in_background and spawn_agent's background
// flag do, so a task always has the tool call that owns it.

type taskListTool struct{ r *Registry }

func (t *taskListTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "task_list",
		Description: "List the background tasks of this agent tree: shell commands and subagents " +
			"started without waiting. One line per task with its id, kind, status, age, owning " +
			"agent and title; tasks are in start order.",
		Parameters: mustSchema(`{"type":"object","properties":{}}`),
	}
}

func (t *taskListTool) Execute(_ context.Context, _ json.RawMessage) Result {
	mgr := t.r.taskMgr
	if mgr == nil {
		return Result{Content: "background tasks are not available in this registry", IsError: true}
	}
	infos := mgr.List()
	if len(infos) == 0 {
		return Result{Content: "(no tasks)"}
	}
	lines := make([]string, 0, len(infos))
	for _, info := range infos {
		lines = append(lines, fmt.Sprintf("%s  %-5s  %-9s  %-5s  %s  %s",
			info.ID, info.Kind, info.Status, taskAge(info), info.AgentID, taskTitle(info.Title)))
	}
	return Result{Content: strings.Join(lines, "\n")}
}

type taskOutputTool struct{ r *Registry }

func (t *taskOutputTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "task_output",
		Description: "Read a background task's output from an offset. The first line is the task's " +
			"status; the chunk follows; a final \"next offset N\" line means more remains and is " +
			"where the next call should start. A shell task's output is its process output; an " +
			"agent task's output is its final message. This call does not wait: poll it while the " +
			"task runs.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"task_id":{"type":"string"},
				"offset":{"type":"integer","minimum":0,"description":"Byte offset to read from. Default 0 or the previous call's next offset."},
				"limit":{"type":"integer","minimum":1,"description":"How many bytes to read; capped at 65536."}
			},
			"required":["task_id"]
		}`),
	}
}

func (t *taskOutputTool) Execute(_ context.Context, args json.RawMessage) Result {
	var in struct {
		TaskID string `json:"task_id"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if in.TaskID == "" {
		return Result{Content: "task_id is required", IsError: true}
	}
	mgr := t.r.taskMgr
	if mgr == nil {
		return Result{Content: "background tasks are not available in this registry", IsError: true}
	}
	info, err := mgr.Get(in.TaskID)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	out, err := mgr.Output(in.TaskID, in.Offset, in.Limit)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}

	header := fmt.Sprintf("%s %s", info.ID, info.Status)
	if info.Kind == tasks.KindShell {
		header += fmt.Sprintf(" (exit %d)", info.ExitCode)
	}
	if out.Truncated {
		header += fmt.Sprintf("\nolder output dropped; the oldest available offset is %d", out.FirstOffset)
	}
	body := out.Text
	if body == "" {
		body = "(no output)"
	} else if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	result := header + "\n" + body
	if out.More || info.Status == tasks.StatusRunning {
		result += fmt.Sprintf("next offset %d", out.NextOffset)
	}
	return Result{Content: strings.TrimRight(result, "\n")}
}

type taskCancelTool struct{ r *Registry }

func (t *taskCancelTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "task_cancel",
		Description: "Stop a running background task and kill what it started. Cancelling a task " +
			"that already ended is not an error; the result is the task's status afterwards.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"task_id":{"type":"string"}
			},
			"required":["task_id"]
		}`),
	}
}

func (t *taskCancelTool) Execute(_ context.Context, args json.RawMessage) Result {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if in.TaskID == "" {
		return Result{Content: "task_id is required", IsError: true}
	}
	mgr := t.r.taskMgr
	if mgr == nil {
		return Result{Content: "background tasks are not available in this registry", IsError: true}
	}
	info, err := mgr.Cancel(in.TaskID)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: fmt.Sprintf("%s %s", info.ID, info.Status)}
}

// taskAge renders how long a task ran, or has been running, in one short
// unit. A finished task keeps a stable age because it ends at EndedAt.
func taskAge(info tasks.Info) string {
	end := time.Now()
	if !info.EndedAt.IsZero() {
		end = info.EndedAt
	}
	d := end.Sub(info.StartedAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// taskTitle keeps one table row on one line: a shell command may be a script
// and a description may wrap.
func taskTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "(untitled)"
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:79]) + "…"
	}
	return s
}
