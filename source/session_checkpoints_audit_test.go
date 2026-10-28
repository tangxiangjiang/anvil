// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/checkpoint"
)

// audit #13: restore writes the saved pre-image over whatever is at the path now,
// so a later edit by the user is overwritten. The decision: a file changed after
// the run is skipped and reported; restore never overwrites it silently.
func TestAuditRestoreSkipsFileChangedAfterRun(t *testing.T) {
	cwd := t.TempDir()
	cp := filepath.Join(cwd, ".anvil", "checkpoints")
	const agent, run = "agent-13", "run-13"
	resolve := func(rel string) (string, error) { return filepath.Join(cwd, filepath.FromSlash(rel)), nil }
	// The run captured a.txt (pre-image "before"), then wrote "run-wrote".
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.Capture(cp, agent, run, "a.txt", filepath.Join(cwd, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("run-wrote"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The run ends and records its post-state; then the user edits the file.
	if err := checkpoint.Finish(cp, agent, run, resolve); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("user-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreCheckpoint(cwd, agent, run, CheckpointOptions{Dir: cp}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, "a.txt"))
	if string(got) != "user-edit" {
		t.Fatalf("restore overwrote the user's later edit: %q", got)
	}
}

// audit #13, branch: a checkpoint with no recorded post-state cannot tell whether the
// user changed the file. It is restored, and the result says so.
func TestAuditRestoreWarnsWhenNoPostStateWasRecorded(t *testing.T) {
	cwd := t.TempDir()
	cp := filepath.Join(cwd, ".anvil", "checkpoints")
	const agent, run = "agent-13w", "run-13w"
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.Capture(cp, agent, run, "a.txt", filepath.Join(cwd, "a.txt")); err != nil {
		t.Fatal(err)
	}
	// No Finish: this is a checkpoint from before post-state existed.
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("user-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := RestoreCheckpoint(cwd, agent, run, CheckpointOptions{Dir: cp})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Warning == "" {
		t.Fatalf("a checkpoint with no post-state restored without a warning: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(cwd, "a.txt")); string(got) != "before" {
		t.Fatalf("the file was not restored to its pre-image: %q", got)
	}
}

// audit #13, branch: a file the user did not change is restored with neither a skip
// nor a warning.
func TestAuditRestoreIsSilentWhenFileUnchanged(t *testing.T) {
	cwd := t.TempDir()
	cp := filepath.Join(cwd, ".anvil", "checkpoints")
	const agent, run = "agent-13u", "run-13u"
	resolve := func(rel string) (string, error) { return filepath.Join(cwd, filepath.FromSlash(rel)), nil }
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.Capture(cp, agent, run, "a.txt", filepath.Join(cwd, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("run-wrote"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.Finish(cp, agent, run, resolve); err != nil {
		t.Fatal(err)
	}
	res, err := RestoreCheckpoint(cwd, agent, run, CheckpointOptions{Dir: cp})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Skipped || res[0].Warning != "" {
		t.Fatalf("an unchanged file was reported as skipped or warned: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(cwd, "a.txt")); string(got) != "before" {
		t.Fatalf("an unchanged file was not restored: %q", got)
	}
}
