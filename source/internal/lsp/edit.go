// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ApplyWorkspaceEdit applies documentChanges/changes to files under cwd.
// Any URI outside cwd → error and no writes (fail closed after validation).
// Returns absolute paths written.
func ApplyWorkspaceEdit(cwd string, edit *WorkspaceEdit) (written []string, err error) {
	if edit == nil {
		return nil, fmt.Errorf("nil workspace edit")
	}
	type fileEdits struct {
		abs   string
		edits []TextEdit
	}
	byPath := map[string]*fileEdits{}

	add := func(uri string, edits []TextEdit) error {
		abs, err := uriToPath(uri)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		abs = filepath.Clean(abs)
		if !insideCWD(cwd, abs) {
			return fmt.Errorf("workspace edit path escapes cwd: %s", uri)
		}
		fe := byPath[abs]
		if fe == nil {
			fe = &fileEdits{abs: abs}
			byPath[abs] = fe
		}
		fe.edits = append(fe.edits, edits...)
		return nil
	}

	if len(edit.DocumentChanges) > 0 {
		for _, dc := range edit.DocumentChanges {
			if err := add(dc.TextDocument.URI, dc.Edits); err != nil {
				return nil, err
			}
		}
	} else {
		for uri, edits := range edit.Changes {
			if err := add(uri, edits); err != nil {
				return nil, err
			}
		}
	}
	if len(byPath) == 0 {
		return nil, fmt.Errorf("workspace edit had no applicable changes")
	}

	// Phase one: compute every file's new content in memory. A failure here writes
	// nothing, so the edit is all-or-nothing for everything that can be checked
	// ahead (audit #8).
	type plannedFile struct {
		abs     string
		content string
	}
	planned := make([]plannedFile, 0, len(byPath))
	for _, fe := range byPath {
		data, err := os.ReadFile(fe.abs)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", fe.abs, err)
		}
		updated, err := applyTextEdits(string(data), fe.edits)
		if err != nil {
			return nil, err
		}
		planned = append(planned, plannedFile{abs: fe.abs, content: updated})
	}
	// Phase two: write. A write can still fail after an earlier one succeeded, so
	// the error reports which files already changed rather than hiding them.
	for _, pf := range planned {
		if err := os.WriteFile(pf.abs, []byte(pf.content), 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w (files already written: %v)", pf.abs, err, written)
		}
		written = append(written, pf.abs)
	}
	return written, nil
}

func applyTextEdits(content string, edits []TextEdit) (string, error) {
	// Apply from end to start so offsets stay valid.
	sort.Slice(edits, func(i, j int) bool {
		a, b := edits[i].Range.Start, edits[j].Range.Start
		if a.Line != b.Line {
			return a.Line > b.Line
		}
		return a.Character > b.Character
	})
	lines := splitKeepEnd(content)
	for _, e := range edits {
		start := posOffset(lines, e.Range.Start)
		end := posOffset(lines, e.Range.End)
		if start < 0 || end < 0 || start > end || end > len(content) {
			return "", fmt.Errorf("edit range out of bounds")
		}
		content = content[:start] + e.NewText + content[end:]
		lines = splitKeepEnd(content)
	}
	return content, nil
}

func splitKeepEnd(s string) []string {
	if s == "" {
		return []string{""}
	}
	return strings.SplitAfter(s, "\n")
}

func posOffset(lines []string, p Position) int {
	if p.Line < 0 || p.Line >= len(lines) {
		// allow end-of-last-line
		if p.Line == len(lines) && p.Character == 0 {
			off := 0
			for _, l := range lines {
				off += len(l)
			}
			return off
		}
		return -1
	}
	off := 0
	for i := 0; i < p.Line; i++ {
		off += len(lines[i])
	}
	line := lines[p.Line]
	// character is UTF-16 in LSP; approximate with runes for ASCII-heavy Go.
	runes := []rune(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
	if p.Character > len(runes) {
		p.Character = len(runes)
	}
	off += len(string(runes[:p.Character]))
	return off
}
