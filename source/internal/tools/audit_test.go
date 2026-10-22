// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/tools"
)

// audit #1: the grep fallback (used when rg is not on PATH) follows a symlink
// whose target is outside cwd and returns its matching lines.
func TestAuditGrepFallbackDoesNotFollowEscapingSymlink(t *testing.T) {
	parent := t.TempDir()
	cwd := filepath.Join(parent, "ws")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secret, []byte("top-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(cwd, "leak.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// With no rg on PATH, grep takes the Go fallback that walks the tree itself.
	t.Setenv("PATH", "")

	reg := tools.NewRegistry(cwd, false)
	res := reg.Execute(context.Background(), "grep", mustJSON(map[string]any{
		"pattern": "top-secret",
	}))
	if strings.Contains(res.Content, "top-secret") {
		t.Fatalf("grep returned content from outside cwd through a symlink: %q", res.Content)
	}
}

// audit #3: a dangling in-root symlink whose target is outside cwd resolves
// without error, so a write creates the outside file.
func TestAuditDanglingSymlinkEscapeIsRefused(t *testing.T) {
	parent := t.TempDir()
	cwd := filepath.Join(parent, "ws")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "new.txt") // deliberately absent
	if err := os.Symlink(outside, filepath.Join(cwd, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	reg := tools.NewRegistry(cwd, false)
	reg.Execute(context.Background(), "write_file", mustJSON(map[string]any{
		"path":    "link",
		"content": "escaped",
	}))
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("write through a dangling in-root symlink created the file outside cwd")
	}
}

// audit #4: a failed patch leaves earlier operations on disk and reports none.
func TestAuditPatchIsAllOrNothing(t *testing.T) {
	cwd := t.TempDir()
	reg := tools.NewRegistry(cwd, false)
	patch := "*** Begin Patch\n*** Add File: a.txt\n+one\n*** Update File: missing.txt\n@@\n-x\n+y\n*** End Patch"
	reg.Execute(context.Background(), "apply_patch", mustJSON(map[string]any{"patch": patch}))
	if _, err := os.Stat(filepath.Join(cwd, "a.txt")); err == nil {
		t.Fatal("a.txt was written although the patch failed; the patch is not all-or-nothing")
	}
}

// audit #9: when the first read of the rule file fails, the session has no last
// good policy, and Execute discards the read error, so a deny rule disappears and
// the call runs. write_file is used because shell is not registered on every
// platform; the rule must be what decides this call.
func TestAuditMalformedRuleFileStillDenies(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".anvil"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A rule file that does not parse; the deny below is meant to be in force.
	if err := os.WriteFile(filepath.Join(cwd, ".anvil", "permissions.json"),
		[]byte(`{"version":1,"rules":[{"effect":"deny","tool":"write_file","pattern":"*"`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := permissions.NewSource(cwd, "", t.TempDir(), t.TempDir(), nil, nil)
	reg := tools.NewRegistry(cwd, false)
	reg.SetPermissionRulesSource(src)
	res := reg.Execute(context.Background(), "write_file", mustJSON(map[string]any{
		"path":    "ran.txt",
		"content": "ran",
	}))
	if _, err := os.Stat(filepath.Join(cwd, "ran.txt")); err == nil {
		t.Fatalf("write_file ran although a deny rule was in force; result: %+v", res)
	}
}

// audit #6: captureBefore ignores the error of checkpoint.Capture, so a run whose
// pre-image could not be saved still writes, and nothing says the file is not
// restorable.
func TestAuditFailedCaptureIsNotSilent(t *testing.T) {
	cwd := t.TempDir()
	cpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make the run's directory unusable: a regular file where a directory must go.
	if err := os.MkdirAll(filepath.Join(cpDir, "agent-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cpDir, "agent-1", "run-1"), []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(cwd, false)
	reg.SetCheckpoints(cpDir)
	reg.SetRunContext("agent-1", "run-1")
	res := reg.Execute(context.Background(), "write_file", mustJSON(map[string]any{
		"path": "a.txt", "content": "new",
	}))
	if b, _ := os.ReadFile(filepath.Join(cwd, "a.txt")); string(b) == "new" && !res.IsError {
		// The write went through with no pre-image and no error: the caller is not told.
		t.Fatalf("write_file succeeded without a saved pre-image and reported no problem: %q", res.Content)
	}
}
