// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// Preview is what a tool call would do, computed without doing it. It exists so
// that a permission hook — the editor host above all — can show the user the
// effect of a call before answering allow or deny.
type Preview struct {
	// Kind is "diff" (a file would change), "command" (a process would run),
	// "summary" (recognised but not previewable), or "" (nothing to say).
	Kind string

	// Path is the workspace-relative target for a diff preview.
	Path string
	// Diff is a unified diff of the projected change, for Kind == "diff".
	Diff string
	// Before and After are the file's body before and after the projected
	// change, for Kind == "diff". They are what a native diff editor needs: a
	// unified diff alone would make every client re-implement the patch
	// algorithm, and the client's second implementation is exactly the thing the
	// user would be approving. Both are empty for apply_patch, which may span
	// several files and so has no single pre- or post-image.
	Before string
	After  string
	// Command is the argv that would run, for Kind == "command".
	Command []string
	// Summary is a one-line description for anything else.
	Summary string
}

// Preview kinds.
const (
	PreviewDiff    = "diff"
	PreviewCommand = "command"
	PreviewSummary = "summary"
)

// PreviewFor describes a pending call. It never fails: a preview is advisory,
// and an unreadable pre-image or a malformed patch yields a summary instead.
func (r *Registry) PreviewFor(name string, args json.RawMessage) Preview {
	if r == nil {
		return Preview{}
	}
	switch name {
	case "write_file":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal(args, &in) != nil || in.Path == "" {
			return Preview{}
		}
		return r.diffPreview(in.Path, in.Content)
	case "edit_file":
		var in struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal(args, &in) != nil || in.Path == "" {
			return Preview{}
		}
		abs, err := r.resolvePath(in.Path)
		if err != nil {
			return Preview{Kind: PreviewSummary, Path: in.Path, Summary: err.Error()}
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return Preview{Kind: PreviewSummary, Path: in.Path, Summary: err.Error()}
		}
		text := string(data)
		if count := strings.Count(text, in.OldString); count != 1 {
			return Preview{
				Kind:    PreviewSummary,
				Path:    in.Path,
				Summary: editCountSummary(count),
			}
		}
		return r.diffPreview(in.Path, strings.Replace(text, in.OldString, in.NewString, 1))
	case "apply_patch":
		return r.patchPreview(args)
	case "shell":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(args, &in) != nil || in.Command == "" {
			return Preview{}
		}
		// shell takes a command string, not argv; show it as a one-line command.
		return Preview{Kind: PreviewCommand, Command: []string{in.Command}}
	case "run_tests", "build":
		return commandPreview(r, name)
	case "grep", "glob", "read_file", "semantic_search", "read_skill", "list_skills",
		"git_status", "git_diff", "git_log", "diagnostics", "definition", "references",
		"hover", "web_search", "fetch_url", "read_image":
		return Preview{Kind: PreviewSummary, Summary: string(args)}
	default:
		return Preview{Kind: PreviewSummary, Summary: string(args)}
	}
}

func editCountSummary(count int) string {
	switch {
	case count == 0:
		return "old_string does not appear in the file; the edit will fail"
	case count > 1:
		return "old_string appears more than once; the edit will fail"
	default:
		return ""
	}
}

// commandPreview renders the configured argv for run_tests / build, which is
// fixed by Options rather than supplied by the model.
func commandPreview(r *Registry, name string) Preview {
	var argv []string
	if name == "run_tests" {
		argv = r.verifyCfg.Test.Command
	} else {
		argv = r.verifyCfg.Build.Command
	}
	if len(argv) == 0 {
		return Preview{}
	}
	return Preview{Kind: PreviewCommand, Command: append([]string(nil), argv...)}
}

// patchPreview dry-runs an apply_patch body: it parses the patch and replays
// every hunk in memory, so the diff it returns is the same one the real call
// would produce, or the reason the real call would fail.
func (r *Registry) patchPreview(args json.RawMessage) Preview {
	var in struct {
		Patch string `json:"patch"`
	}
	if json.Unmarshal(args, &in) != nil || in.Patch == "" {
		return Preview{}
	}
	ops, err := parsePatch(in.Patch)
	if err != nil {
		return Preview{Kind: PreviewSummary, Summary: err.Error()}
	}
	var paths []string
	var diffs []string
	for _, op := range ops {
		switch op.Kind {
		case patchAdd:
			paths = append(paths, op.Path)
			diffs = append(diffs, unifiedDiff(op.Path, "", op.AddContent))
		case patchDelete:
			paths = append(paths, op.Path)
			pre, err := r.readForPreview(op.Path)
			if err != nil {
				return Preview{Kind: PreviewSummary, Path: op.Path, Summary: err.Error()}
			}
			diffs = append(diffs, unifiedDiff(op.Path, pre, ""))
		case patchUpdate:
			paths = append(paths, op.Path)
			pre, err := r.readForPreview(op.Path)
			if err != nil {
				return Preview{Kind: PreviewSummary, Path: op.Path, Summary: err.Error()}
			}
			text := pre
			for i, h := range op.Hunks {
				next, err := applyHunk(text, h)
				if err != nil {
					return Preview{
						Kind:    PreviewSummary,
						Path:    op.Path,
						Summary: hunkErrorSummary(op.Path, i, err),
					}
				}
				text = next
			}
			diffs = append(diffs, unifiedDiff(op.Path, pre, text))
		}
	}
	// Before/After stay empty on purpose: one patch may touch several files, so
	// there is no single pre- or post-image to hand a diff editor. The
	// concatenated diff is what the caller shows instead.
	return Preview{
		Kind: PreviewDiff,
		Path: strings.Join(paths, ", "),
		Diff: strings.Join(diffs, ""),
	}
}

// hunkErrorSummary mirrors the wording the real tool reports, 1-based hunk
// numbering included, so the prompt and the eventual tool error agree.
func hunkErrorSummary(path string, index int, err error) string {
	return path + ": the patch will fail — hunk " + strconv.Itoa(index+1) + ": " + err.Error()
}

// readForPreview reads the current body of a workspace path. A path that is
// rejected by the sandbox is an error; a path that simply does not exist yet is
// an empty pre-image, because that is what the real call would create. Any
// other read failure — a directory, a permission bit — is reported rather than
// passed off as an empty file, which would render the whole body as additions.
func (r *Registry) readForPreview(path string) (string, error) {
	abs, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// diffPreview builds the diff between the file on disk and the intended body.
func (r *Registry) diffPreview(path, next string) Preview {
	pre, err := r.readForPreview(path)
	if err != nil {
		return Preview{Kind: PreviewSummary, Path: path, Summary: err.Error()}
	}
	return Preview{
		Kind:   PreviewDiff,
		Path:   path,
		Diff:   unifiedDiff(path, pre, next),
		Before: pre,
		After:  next,
	}
}

// ---------------------------------------------------------------------------
// unified diff
// ---------------------------------------------------------------------------

const diffContext = 3

// unifiedDiff renders a unified diff with `--- a/path` / `+++ b/path` headers.
// It is intentionally small: no rename detection, no "\ No newline" marker, and
// bounded context — enough for a human to approve a change, and stable enough
// to assert on in tests.
func unifiedDiff(path, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	oldLines := splitDiffLines(oldText)
	newLines := splitDiffLines(newText)
	ops := diffLines(oldLines, newLines)

	var b strings.Builder
	b.WriteString("--- a/")
	b.WriteString(path)
	b.WriteString("\n+++ b/")
	b.WriteString(path)
	b.WriteString("\n")

	for _, h := range groupHunks(ops, diffContext) {
		b.WriteString("@@ -")
		b.WriteString(rangeHeader(h.oldStart, h.oldCount))
		b.WriteString(" +")
		b.WriteString(rangeHeader(h.newStart, h.newCount))
		b.WriteString(" @@\n")
		for _, op := range h.ops {
			switch op.kind {
			case diffEqual:
				b.WriteString(" ")
			case diffDelete:
				b.WriteString("-")
			case diffInsert:
				b.WriteString("+")
			}
			b.WriteString(op.text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// splitDiffLines splits into lines without inventing a trailing empty line,
// which would otherwise show up as a spurious change for every file that ends
// in a newline.
func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

type diffOpKind int

const (
	diffEqual diffOpKind = iota
	diffDelete
	diffInsert
)

type lineOp struct {
	kind diffOpKind
	text string
}

// diffLines is a standard LCS diff. It is O(n*m) in time, which is fine at the
// sizes a single tool call touches (a file body already in memory), and it runs
// only on the approval path.
func diffLines(a, b []string) []lineOp {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []lineOp
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, lineOp{diffEqual, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, lineOp{diffDelete, a[i]})
			i++
		default:
			ops = append(ops, lineOp{diffInsert, b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, lineOp{diffDelete, a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, lineOp{diffInsert, b[j]})
	}
	return ops
}

type diffHunk struct {
	oldStart, oldCount int // 1-based first old line, and how many it covers
	newStart, newCount int
	ops                []lineOp
}

// groupHunks splits the op stream into hunks separated by at most 2*context
// unchanged lines, trimming each end to `context` lines. Old/new start and
// count follow unified-diff conventions: 1-based, and a zero count means the
// range sits before the first line.
func groupHunks(ops []lineOp, context int) []diffHunk {
	// Positions of changes, plus the surrounding context window.
	type span struct{ start, end int }
	var spans []span
	for i, op := range ops {
		if op.kind == diffEqual {
			continue
		}
		start := i - context
		if start < 0 {
			start = 0
		}
		end := i + context + 1
		if end > len(ops) {
			end = len(ops)
		}
		if n := len(spans); n > 0 && start <= spans[n-1].end {
			if end > spans[n-1].end {
				spans[n-1].end = end
			}
			continue
		}
		spans = append(spans, span{start, end})
	}

	out := make([]diffHunk, 0, len(spans))
	for _, s := range spans {
		// 0-based index of the first old line within the file.
		oldIndex := 0
		for _, op := range ops[:s.start] {
			if op.kind != diffInsert {
				oldIndex++
			}
		}
		h := diffHunk{ops: ops[s.start:s.end], oldStart: oldIndex + 1, newStart: oldIndex + 1}
		for _, op := range h.ops {
			if op.kind != diffInsert {
				h.oldCount++
			}
			if op.kind != diffDelete {
				h.newCount++
			}
		}
		if h.oldCount == 0 {
			h.oldStart = oldIndex // pure insertion: anchor before the next line
		}
		if h.newCount == 0 {
			h.newStart = oldIndex
		}
		out = append(out, h)
	}
	return out
}

func rangeHeader(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}
