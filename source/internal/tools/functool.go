// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"

	"github.com/anvil-dev/anvil/internal/llm"
)

// FuncTool wraps a handler function as a Tool.
type FuncTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
	// Builtin marks a tool the runtime itself ships but implements as a function
	// because it needs the surrounding agent (spawn_agent, run_workflow). It is
	// what keeps such a tool out of IsExternalTool, so the group table and the
	// tool filters treat it like any other built-in.
	Builtin bool
}

// Def implements Tool.
func (t *FuncTool) Def() llm.ToolDef {
	params := t.Parameters
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return llm.ToolDef{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  params,
	}
}

// Execute implements Tool.
func (t *FuncTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if t.Handler == nil {
		return Result{Content: "tool handler is nil", IsError: true}
	}
	out, err := t.Handler(ctx, args)
	if err != nil {
		if out == "" {
			out = err.Error()
		} else {
			out = out + "\n" + err.Error()
		}
		return Result{Content: out, IsError: true}
	}
	if out == "" {
		out = "(no output)"
	}
	return Result{Content: out}
}
