// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	ID         string
	Text       string
	Status     string
	WorkItemID string
}

var todoIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

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
				"artifact_id":{"type":"string","description":"Optional structured plan artifact id"},
				"items":{
					"type":"array",
					"description":"The complete list, in order. An empty array clears it.",
					"items":{
						"type":"object",
						"properties":{
							"id":{"type":"string","description":"Optional stable todo row id"},
							"text":{"type":"string","description":"One task, one line"},
							"status":{"type":"string","enum":["pending","in_progress","done"]},
							"work_item_id":{"type":"string","description":"Optional explicit work item link"}
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
		ArtifactID string `json:"artifact_id"`
		Items      []struct {
			ID         string `json:"id"`
			Text       string `json:"text"`
			Status     string `json:"status"`
			WorkItemID string `json:"work_item_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: "invalid args: " + err.Error(), IsError: true}
	}
	// Non-nil even when empty: an empty list is how a call clears the panel,
	// and a nil slice would reach the wire as null rather than [].
	items := make([]TodoItem, 0, len(in.Items))
	if in.ArtifactID != "" && !todoIDPattern.MatchString(in.ArtifactID) {
		return Result{Content: "artifact_id is not a stable identifier", IsError: true}
	}
	ids := make(map[string]struct{})
	workItems := make(map[string]struct{})
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
		if it.ID != "" {
			if !todoIDPattern.MatchString(it.ID) {
				return Result{Content: fmt.Sprintf("items[%d].id is not a stable identifier", i), IsError: true}
			}
			if _, duplicate := ids[it.ID]; duplicate {
				return Result{Content: fmt.Sprintf("items[%d].id %q is duplicated", i, it.ID), IsError: true}
			}
			ids[it.ID] = struct{}{}
		}
		if it.WorkItemID != "" {
			if in.ArtifactID == "" {
				return Result{Content: fmt.Sprintf("items[%d].work_item_id requires artifact_id", i), IsError: true}
			}
			if !todoIDPattern.MatchString(it.WorkItemID) {
				return Result{Content: fmt.Sprintf("items[%d].work_item_id is not a stable identifier", i), IsError: true}
			}
			if _, duplicate := workItems[it.WorkItemID]; duplicate {
				return Result{Content: fmt.Sprintf("items[%d].work_item_id %q is duplicated", i, it.WorkItemID), IsError: true}
			}
			workItems[it.WorkItemID] = struct{}{}
		}
		items = append(items, TodoItem{
			ID: it.ID, Text: text, Status: it.Status, WorkItemID: it.WorkItemID,
		})
	}
	noun := "items"
	if len(items) == 1 {
		noun = "item"
	}
	return Result{
		Content: fmt.Sprintf("%d %s", len(items), noun),
		Todos:   items, TodoArtifactID: in.ArtifactID,
	}
}
