// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTodoWriteStoresTheList(t *testing.T) {
	reg := NewRegistry(t.TempDir(), false)
	res := reg.Execute(context.Background(), TodoToolName, json.RawMessage(
		`{"items":[{"text":" read the file ","status":"done"},{"text":"fix it","status":"in_progress"}]}`,
	))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "2 items" {
		t.Fatalf("content %q", res.Content)
	}
	if len(res.Todos) != 2 {
		t.Fatalf("todos %+v", res.Todos)
	}
	// Text is trimmed so the panel's rows line up; the status is kept verbatim
	// because the set is closed and was validated above.
	if res.Todos[0] != (TodoItem{Text: "read the file", Status: TodoDone}) {
		t.Fatalf("first item %+v", res.Todos[0])
	}
	if res.Todos[1] != (TodoItem{Text: "fix it", Status: TodoInProgress}) {
		t.Fatalf("second item %+v", res.Todos[1])
	}
}

func TestTodoWriteSaysOneItem(t *testing.T) {
	reg := NewRegistry(t.TempDir(), false)
	res := reg.Execute(context.Background(), TodoToolName, json.RawMessage(
		`{"items":[{"text":"only","status":"pending"}]}`,
	))
	if res.IsError || res.Content != "1 item" {
		t.Fatalf("content %q error %v", res.Content, res.IsError)
	}
}

func TestTodoWriteRejectsAnUnknownStatus(t *testing.T) {
	reg := NewRegistry(t.TempDir(), false)
	res := reg.Execute(context.Background(), TodoToolName, json.RawMessage(
		`{"items":[{"text":"ok","status":"pending"},{"text":"bad","status":"blocked"}]}`,
	))
	if !res.IsError {
		t.Fatalf("want an error, got %+v", res)
	}
	// The message has to say which row, or the model has to guess which of its
	// items the client refused.
	if !strings.Contains(res.Content, "items[1]") || !strings.Contains(res.Content, "blocked") {
		t.Fatalf("unhelpful error %q", res.Content)
	}
	// A refused call changes nothing: the panel keeps the list it had.
	if res.Todos != nil {
		t.Fatalf("a failed call produced a list: %+v", res.Todos)
	}
}

func TestTodoWriteRejectsEmptyText(t *testing.T) {
	reg := NewRegistry(t.TempDir(), false)
	res := reg.Execute(context.Background(), TodoToolName, json.RawMessage(
		`{"items":[{"text":"   ","status":"pending"}]}`,
	))
	if !res.IsError || !strings.Contains(res.Content, "items[0].text") {
		t.Fatalf("got %q error=%v", res.Content, res.IsError)
	}
}

func TestTodoWriteEmptyListClears(t *testing.T) {
	reg := NewRegistry(t.TempDir(), false)
	res := reg.Execute(context.Background(), TodoToolName, json.RawMessage(`{"items":[]}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "0 items" {
		t.Fatalf("content %q", res.Content)
	}
	// Non-nil: an empty list is the statement "nothing is planned", which the
	// wire and the panel must be able to tell from "no list was sent".
	if res.Todos == nil || len(res.Todos) != 0 {
		t.Fatalf("todos %#v", res.Todos)
	}
}

// The checklist changes nothing a permission could protect, so a group that
// names it must not turn it into an approval prompt. The callback fails the
// call if it is ever consulted.
func TestTodoWriteIsNotGated(t *testing.T) {
	reg := NewRegistry(t.TempDir(), false)
	reg.SetPermissionGroups([]string{TodoToolName})
	reg.SetPermission(func(ctx context.Context, call GatedCall) (json.RawMessage, error) {
		return nil, fmt.Errorf("todo_write must not ask")
	})
	res := reg.Execute(context.Background(), TodoToolName, json.RawMessage(
		`{"items":[{"text":"plan","status":"pending"}]}`,
	))
	if res.IsError {
		t.Fatalf("gated: %s", res.Content)
	}
	if len(res.Todos) != 1 {
		t.Fatalf("todos %+v", res.Todos)
	}
}

func TestTodoWriteIsInTheDefaultSet(t *testing.T) {
	reg := NewRegistry(t.TempDir(), false)
	for _, name := range reg.Names() {
		if name == TodoToolName {
			return
		}
	}
	t.Fatalf("%s is not registered by default: %v", TodoToolName, reg.Names())
}
