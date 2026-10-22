// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/lsp"
)

// SetLSP attaches an LSP supervisor and registers lsp tools (or clears them).
func (r *Registry) SetLSP(sup *lsp.Supervisor) {
	if r == nil {
		return
	}
	for _, name := range []string{"diagnostics", "definition", "references", "hover", "rename_symbol"} {
		r.Unregister(name)
	}
	r.lsp = sup
	r.fileSync = nil
	if sup == nil {
		return
	}
	r.register(&diagnosticsTool{r: r})
	r.register(&definitionTool{r: r})
	r.register(&referencesTool{r: r})
	r.register(&hoverTool{r: r})
	r.register(&renameSymbolTool{r: r})
	r.fileSync = func(ctx context.Context, absPath string) {
		sup.NotifyFileChanged(ctx, absPath)
	}
}

func (r *Registry) notifyFileWrite(ctx context.Context, absPath string) {
	if r != nil && r.fileSync != nil {
		r.fileSync(ctx, absPath)
	}
}

func (r *Registry) requireLSP() (*lsp.Supervisor, error) {
	if r == nil || r.lsp == nil {
		return nil, fmt.Errorf("LSP is not configured")
	}
	return r.lsp, nil
}

type diagnosticsTool struct{ r *Registry }

func (t *diagnosticsTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "diagnostics",
		Description: "List language-server diagnostics for the workspace (optional path filter).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string","description":"optional file path or glob filter"}
			}
		}`),
	}
}

func (t *diagnosticsTool) Execute(ctx context.Context, args json.RawMessage) Result {
	sup, err := t.r.requireLSP()
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var in struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &in)
	hits, err := sup.Diagnostics(ctx, in.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if len(hits) == 0 {
		return Result{Content: "(no diagnostics)"}
	}
	var b strings.Builder
	for _, h := range hits {
		sev := severityName(h.Severity)
		fmt.Fprintf(&b, "%s:%d:%d [%s] %s\n", h.Path, h.Line, h.Character, sev, h.Message)
	}
	return Result{Content: strings.TrimSpace(b.String())}
}

type definitionTool struct{ r *Registry }

func (t *definitionTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "definition",
		Description: "Go to definition via LSP (0-based line/character).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"line":{"type":"integer"},
				"character":{"type":"integer"}
			},
			"required":["path","line","character"]
		}`),
	}
}

func (t *definitionTool) Execute(ctx context.Context, args json.RawMessage) Result {
	sup, err := t.r.requireLSP()
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var in struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" {
		return Result{Content: "path, line, character required", IsError: true}
	}
	locs, err := sup.Definition(ctx, in.Path, in.Line, in.Character)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: formatLocations(locs)}
}

type referencesTool struct{ r *Registry }

func (t *referencesTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "references",
		Description: "Find references via LSP (0-based line/character).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"line":{"type":"integer"},
				"character":{"type":"integer"},
				"include_declaration":{"type":"boolean"}
			},
			"required":["path","line","character"]
		}`),
	}
}

func (t *referencesTool) Execute(ctx context.Context, args json.RawMessage) Result {
	sup, err := t.r.requireLSP()
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var in struct {
		Path               string `json:"path"`
		Line               int    `json:"line"`
		Character          int    `json:"character"`
		IncludeDeclaration *bool  `json:"include_declaration"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" {
		return Result{Content: "path, line, character required", IsError: true}
	}
	incl := true
	if in.IncludeDeclaration != nil {
		incl = *in.IncludeDeclaration
	}
	locs, err := sup.References(ctx, in.Path, in.Line, in.Character, incl)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: formatLocations(locs)}
}

type hoverTool struct{ r *Registry }

func (t *hoverTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "hover",
		Description: "LSP hover documentation at a position (0-based line/character).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"line":{"type":"integer"},
				"character":{"type":"integer"}
			},
			"required":["path","line","character"]
		}`),
	}
}

func (t *hoverTool) Execute(ctx context.Context, args json.RawMessage) Result {
	sup, err := t.r.requireLSP()
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var in struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" {
		return Result{Content: "path, line, character required", IsError: true}
	}
	text, err := sup.Hover(ctx, in.Path, in.Line, in.Character)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: text}
}

type renameSymbolTool struct{ r *Registry }

func (t *renameSymbolTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "rename_symbol",
		Description: "Rename a symbol via LSP WorkspaceEdit (multi-file; cwd-sandboxed).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"line":{"type":"integer"},
				"character":{"type":"integer"},
				"new_name":{"type":"string"}
			},
			"required":["path","line","character","new_name"]
		}`),
	}
}

func (t *renameSymbolTool) Execute(ctx context.Context, args json.RawMessage) Result {
	sup, err := t.r.requireLSP()
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var in struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
		NewName   string `json:"new_name"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" || in.NewName == "" {
		return Result{Content: "path, line, character, new_name required", IsError: true}
	}
	we, err := sup.Rename(ctx, in.Path, in.Line, in.Character, in.NewName)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	// The edit names every file the rename will change, which the path argument does
	// not. Each one is checkpointed before the write, so the run can be undone
	// and a path rule can see the targets (audit #7).
	if err := t.r.captureWorkspaceEdit(we); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	paths, err := lsp.ApplyWorkspaceEdit(t.r.cwd, we)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	for _, p := range paths {
		t.r.notifyFileWrite(ctx, p)
	}
	return Result{Content: fmt.Sprintf("renamed: updated %d file(s)", len(paths))}
}

func formatLocations(locs []lsp.Location) string {
	if len(locs) == 0 {
		return "(no locations)"
	}
	var b strings.Builder
	for i, loc := range locs {
		fmt.Fprintf(&b, "%d. %s:%d:%d\n", i+1, loc.URI, loc.Range.Start.Line, loc.Range.Start.Character)
	}
	return strings.TrimSpace(b.String())
}

func severityName(s int) string {
	switch s {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "diag"
	}
}
