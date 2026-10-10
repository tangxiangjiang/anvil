// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anvil-dev/anvil/internal/gitutil"
	"github.com/anvil-dev/anvil/internal/llm"
)

func (r *Registry) git() gitutil.Runner {
	return gitutil.Runner{CWD: r.cwd}
}

type gitStatusTool struct{ r *Registry }

func (t *gitStatusTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "git_status",
		Description: "Show git branch and staged/unstaged/untracked summary (read-only). Prefer this over shell git status.",
		Parameters:  mustSchema(`{"type":"object","properties":{}}`),
	}
}

func (t *gitStatusTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = args
	st, err := t.r.git().Status(ctx)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: gitutil.FormatStatus(st)}
}

type gitDiffTool struct{ r *Registry }

func (t *gitDiffTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "git_diff",
		Description: "Unified git diff (read-only). Optional staged=true for --cached; paths must stay under cwd.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"staged":{"type":"boolean"},
				"paths":{"type":"array","items":{"type":"string"}},
				"context":{"type":"integer","description":"diff -U lines"}
			}
		}`),
	}
}

func (t *gitDiffTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		Staged  bool     `json:"staged"`
		Paths   []string `json:"paths"`
		Context int      `json:"context"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{Content: "invalid args: " + err.Error(), IsError: true}
		}
	}
	out, err := t.r.git().Diff(ctx, gitutil.DiffOpts{
		Staged:  in.Staged,
		Paths:   in.Paths,
		Context: in.Context,
	})
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: out}
}

type gitLogTool struct{ r *Registry }

func (t *gitLogTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "git_log",
		Description: "Recent git commits (read-only). Default n=20, hard max 50.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"n":{"type":"integer"},
				"path":{"type":"string"},
				"author":{"type":"string"}
			}
		}`),
	}
}

func (t *gitLogTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		N      int    `json:"n"`
		Path   string `json:"path"`
		Author string `json:"author"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{Content: "invalid args: " + err.Error(), IsError: true}
		}
	}
	entries, err := t.r.git().Log(ctx, gitutil.LogOpts{
		N: in.N, Path: strings.TrimSpace(in.Path), Author: strings.TrimSpace(in.Author),
	})
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: gitutil.FormatLog(entries)}
}
