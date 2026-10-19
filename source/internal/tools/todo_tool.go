// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
)

// TodoToolName is the built-in that publishes the model's plan. It is the one
// tool this milestone adds rather than enables, because a visible checklist is
// part of the interaction being aligned and the runtime has no equivalent.
const TodoToolName = "todo_write"

// Todo item statuses. Any other value is a tool error: a checklist whose state
// a client cannot draw is worse than a refused write.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoDone       = "done"
)

// TodoItem is one row of the model's plan.
type TodoItem struct {
	Text   string
	Status string
}

// todoWriteTool replaces the session's task list. It writes no file, runs no
// process and touches no network; the list lives in the run's event stream and
// in the ordinary tool message the call is persisted as.
type todoWriteTool struct{ r *Registry }

func (t *todoWriteTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: TodoToolName,
		Description: "Replace the session's task list. Send the complete list every time, not a delta. " +
			"Use it to show the plan you are working through and keep it current as you go; " +
			"an empty list clears it.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"items":{
					"type":"array",
					"description":"The complete list, in order. An empty array clears it.",
					"items":{
						"type":"object",
						"properties":{
							"text":{"type":"string","description":"One task, one line"},
							"status":{"type":"string","enum":["pending","in_progress","done"]}
						},
						"required":["text","status"]
					}
				}
			},
			"required":["items"]
		}`),
	}
}

func (t *todoWriteTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	var in struct {
		Items []struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: "invalid args: " + err.Error(), IsError: true}
	}
	// Non-nil even when empty: an empty list is how a call clears the panel,
	// and a nil slice would reach the wire as null rather than [].
	items := make([]TodoItem, 0, len(in.Items))
	for i, it := range in.Items {
		text := strings.TrimSpace(it.Text)
		if text == "" {
			return Result{Content: fmt.Sprintf("items[%d].text is empty", i), IsError: true}
		}
		switch it.Status {
		case TodoPending, TodoInProgress, TodoDone:
		default:
			return Result{
				Content: fmt.Sprintf("items[%d].status %q is not one of pending, in_progress, done", i, it.Status),
				IsError: true,
			}
		}
		items = append(items, TodoItem{Text: text, Status: it.Status})
	}
	noun := "items"
	if len(items) == 1 {
		noun = "item"
	}
	return Result{Content: fmt.Sprintf("%d %s", len(items), noun), Todos: items}
}
