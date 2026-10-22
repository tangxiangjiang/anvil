// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/lsp"
)

// audit #2: insideCWD is lexical, so an in-workspace symlink to a file outside
// the workspace is treated as inside and written through by a workspace edit.
func TestAuditWorkspaceEditDoesNotWriteThroughSymlink(t *testing.T) {
	// Reproduced on AlmaLinux (Linux file URIs round-trip through uriToPath). On
	// Windows the test cannot reach the symlink: a "file://C:/" URI is parsed
	// as a relative path, so the edit fails before the symlink is followed.
	t.Skip("audit: open, see audit.md #2 (reproduce on Linux)")
	parent := t.TempDir()
	cwd := filepath.Join(parent, "ws")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cwd, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	edit := &lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		"file://" + filepath.ToSlash(link): {{
			Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 4}},
			NewText: "gone",
		}},
	}}
	if _, err := lsp.ApplyWorkspaceEdit(cwd, edit); err == nil {
		t.Fatal("an edit through a symlink to a file outside the workspace was applied")
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "keep" {
		t.Fatalf("file outside the workspace was overwritten: %q", got)
	}
}

// audit #8: a multi-file workspace edit that fails part way returns no list of
// the files it already wrote and does not report them, so the caller cannot tell
// which files changed.
func TestAuditWorkspaceEditReportsPartialWrites(t *testing.T) {
	t.Skip("audit: open, see audit.md #8")
	cwd := t.TempDir()
	first := filepath.Join(cwd, "a.txt")
	second := filepath.Join(cwd, "b.txt")
	if err := os.WriteFile(first, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("two"), 0o444); err != nil {
		t.Fatal(err)
	}
	rng := lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 3}}
	edit := &lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		"file:///" + filepath.ToSlash(first):  {{Range: rng, NewText: "ONE"}},
		"file:///" + filepath.ToSlash(second): {{Range: rng, NewText: "TWO"}},
	}}
	written, err := lsp.ApplyWorkspaceEdit(cwd, edit)
	got, _ := os.ReadFile(first)
	if err != nil && len(written) == 0 && string(got) != "one" {
		t.Fatalf("first file changed but the failed edit reported no written files: err=%v", err)
	}
}
