// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
)

/*
apply_patch format (multi-hunk / multi-file):

*** Begin Patch
*** Update File: path/rel.go
@@
 context line (required anchor; exact match once)
-old line
+new line
@@
 other context
-old2
+new2
*** Add File: path/new.txt
+line one
+line two
*** Delete File: path/old.txt
*** End Patch
*/

type applyPatchTool struct{ r *Registry }

func (t *applyPatchTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "apply_patch",
		Description: `Apply a multi-hunk patch to the workspace. Format:
*** Begin Patch
*** Update File: relative/path
@@
 context line
-old
+new
*** Add File: relative/new.txt
+contents
*** Delete File: relative/gone.txt
*** End Patch
Prefer apply_patch for multi-hunk edits; use edit_file for a single unique string replace.`,
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"patch":{"type":"string","description":"Full patch text including Begin/End markers"}
			},
			"required":["patch"]
		}`),
	}
}

func (t *applyPatchTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	ops, err := parsePatch(in.Patch)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	// Phase one: work out every operation's result in memory and touch no file.
	// A patch that cannot apply in full is refused here, so nothing is written
	// (audit #4: the patch is all-or-nothing).
	planned, err := t.plan(ops)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	// Phase two: write. The checks above catch every failure this function can
	// predict, so a write error here is a real I/O failure.
	var notes []string
	for _, p := range planned {
		if err := p.apply(ctx, t); err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
		notes = append(notes, p.note)
	}
	return Result{Content: strings.Join(notes, "\n")}
}

// plannedOp is one operation with its result already computed.
type plannedOp struct {
	abs     string
	delete  bool
	content string
	note    string
}

// plan checks every operation and computes what each one will write.
func (t *applyPatchTool) plan(ops []patchOp) ([]plannedOp, error) {
	out := make([]plannedOp, 0, len(ops))
	for _, op := range ops {
		abs, err := t.r.resolvePath(op.Path)
		if err != nil {
			return nil, err
		}
		switch op.Kind {
		case patchAdd:
			out = append(out, plannedOp{abs: abs, content: op.AddContent, note: "added " + op.Path})
		case patchDelete:
			out = append(out, plannedOp{abs: abs, delete: true, note: "deleted " + op.Path})
		case patchUpdate:
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, err
			}
			text := string(data)
			for i, h := range op.Hunks {
				next, err := applyHunk(text, h)
				if err != nil {
					return nil, fmt.Errorf("%s hunk %d: %v", op.Path, i+1, err)
				}
				text = next
			}
			out = append(out, plannedOp{abs: abs, content: text, note: fmt.Sprintf("updated %s (%d hunks)", op.Path, len(op.Hunks))})
		}
	}
	return out, nil
}

// apply performs one planned operation.
func (p plannedOp) apply(ctx context.Context, t *applyPatchTool) error {
	if p.delete {
		return os.Remove(p.abs)
	}
	if err := os.MkdirAll(filepath.Dir(p.abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p.abs, []byte(p.content), 0o644); err != nil {
		return err
	}
	t.r.notifyFileWrite(ctx, p.abs)
	return nil
}

type patchKind int

const (
	patchUpdate patchKind = iota
	patchAdd
	patchDelete
)

type patchHunk struct {
	Old string
	New string
}

type patchOp struct {
	Kind       patchKind
	Path       string
	Hunks      []patchHunk
	AddContent string
}

func parsePatch(raw string) ([]patchOp, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.Contains(raw, "*** Begin Patch") || !strings.Contains(raw, "*** End Patch") {
		return nil, fmt.Errorf("patch must include *** Begin Patch and *** End Patch")
	}
	start := strings.Index(raw, "*** Begin Patch")
	end := strings.Index(raw, "*** End Patch")
	if end < start {
		return nil, fmt.Errorf("invalid patch markers")
	}
	body := raw[start+len("*** Begin Patch") : end]
	lines := strings.Split(body, "\n")

	var ops []patchOp
	var cur *patchOp
	var hunkLines []string
	flushHunk := func() error {
		if cur == nil || len(hunkLines) == 0 {
			hunkLines = nil
			return nil
		}
		h, err := parseHunkLines(hunkLines)
		if err != nil {
			return err
		}
		cur.Hunks = append(cur.Hunks, h)
		hunkLines = nil
		return nil
	}
	flushOp := func() error {
		if cur == nil {
			return nil
		}
		if err := flushHunk(); err != nil {
			return err
		}
		if cur.Kind == patchUpdate && len(cur.Hunks) == 0 {
			return fmt.Errorf("update %s has no hunks", cur.Path)
		}
		ops = append(ops, *cur)
		cur = nil
		return nil
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Update File:"):
			if err := flushOp(); err != nil {
				return nil, err
			}
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File:"))
			cur = &patchOp{Kind: patchUpdate, Path: path}
		case strings.HasPrefix(line, "*** Add File:"):
			if err := flushOp(); err != nil {
				return nil, err
			}
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File:"))
			cur = &patchOp{Kind: patchAdd, Path: path}
		case strings.HasPrefix(line, "*** Delete File:"):
			if err := flushOp(); err != nil {
				return nil, err
			}
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File:"))
			cur = &patchOp{Kind: patchDelete, Path: path}
		case line == "@@":
			if cur == nil || cur.Kind != patchUpdate {
				return nil, fmt.Errorf("@@ outside Update File")
			}
			if err := flushHunk(); err != nil {
				return nil, err
			}
		default:
			if cur == nil {
				continue
			}
			if cur.Kind == patchAdd {
				if strings.HasPrefix(line, "+") {
					cur.AddContent += strings.TrimPrefix(line, "+") + "\n"
				} else if line != "" {
					cur.AddContent += line + "\n"
				}
				continue
			}
			if cur.Kind == patchUpdate {
				hunkLines = append(hunkLines, line)
			}
		}
	}
	if err := flushOp(); err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("patch contained no file operations")
	}
	return ops, nil
}

func parseHunkLines(lines []string) (patchHunk, error) {
	var oldB, newB strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		switch line[0] {
		case ' ':
			oldB.WriteString(line[1:])
			oldB.WriteByte('\n')
			newB.WriteString(line[1:])
			newB.WriteByte('\n')
		case '-':
			oldB.WriteString(line[1:])
			oldB.WriteByte('\n')
		case '+':
			newB.WriteString(line[1:])
			newB.WriteByte('\n')
		default:
			// treat as context
			oldB.WriteString(line)
			oldB.WriteByte('\n')
			newB.WriteString(line)
			newB.WriteByte('\n')
		}
	}
	oldS := strings.TrimSuffix(oldB.String(), "\n")
	newS := strings.TrimSuffix(newB.String(), "\n")
	if oldS == "" {
		return patchHunk{}, fmt.Errorf("hunk has empty old side; include context lines")
	}
	return patchHunk{Old: oldS, New: newS}, nil
}

func applyHunk(text string, h patchHunk) (string, error) {
	count := strings.Count(text, h.Old)
	if count == 0 {
		preview := h.Old
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		return "", fmt.Errorf("hunk context not found: %q", preview)
	}
	if count > 1 {
		return "", fmt.Errorf("hunk context matched %d times; add more unique context", count)
	}
	return strings.Replace(text, h.Old, h.New, 1), nil
}
