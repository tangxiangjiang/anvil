// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/lsp"
)

// audit #7: a rename rewrites the files its workspace edit names, not only the file it was
// asked about. Each of those files must be checkpointed before the edit is applied, so the
// run can be undone (the test checks the capture helper the rename uses).
func TestAuditRenameCapturesEveryTargetFile(t *testing.T) {
	cwd := t.TempDir()
	cpDir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := NewRegistry(cwd, false)
	reg.SetCheckpoints(cpDir)
	reg.SetRunContext("agent-7", "run-7")
	edit := &lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		"file:///" + filepath.ToSlash(filepath.Join(cwd, "a.txt")): {},
		"file:///" + filepath.ToSlash(filepath.Join(cwd, "b.txt")): {},
	}}
	if err := reg.captureWorkspaceEdit(edit); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(cpDir, "agent-7", "run-7", "files", name)); err != nil {
			t.Fatalf("%s was not checkpointed before the rename: %v", name, err)
		}
	}
}
