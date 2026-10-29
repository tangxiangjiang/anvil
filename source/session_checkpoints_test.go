// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/checkpoint"
)

// seedCheckpoint writes a run the way the tool seam would, so the public
// listing and restore can be exercised without an LLM.
func seedCheckpoint(t *testing.T, dir, agentID, runID string, at time.Time, files map[string]string) {
	t.Helper()
	root := filepath.Join(dir, ".anvil", "checkpoints")
	for rel, content := range files {
		if content == "" {
			// A create: the file does not exist yet, and neither will its body.
			if err := checkpoint.Capture(root, agentID, runID, rel, filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
				t.Fatal(err)
			}
			continue
		}
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := checkpoint.Capture(root, agentID, runID, rel, abs); err != nil {
			t.Fatal(err)
		}
	}
	// Capture stamps "now"; rewrite the manifest so ordering is testable.
	runDir := root
	manifestFile := filepath.Join(runDir, agentID, runID, "manifest.json")
	data, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	var m checkpoint.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m.CreatedAt = at
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestFile, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListCheckpointsReportsRunsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	seedCheckpoint(t, dir, "agent-1", "run-a", base, map[string]string{"a.txt": "before"})
	seedCheckpoint(t, dir, "agent-1", "run-b", base.Add(time.Minute), map[string]string{"a.txt": "before b"})

	runs, err := ListCheckpoints(dir, "agent-1", CheckpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].RunID != "run-b" || runs[1].RunID != "run-a" {
		t.Fatalf("runs = %+v, want run-b then run-a", runs)
	}
	if len(runs[0].Files) != 1 || runs[0].Files[0].Path != "a.txt" || !runs[0].Files[0].Existed {
		t.Fatalf("files = %+v", runs[0].Files)
	}
}

func TestListCheckpointsEmptyIsNotAnError(t *testing.T) {
	runs, err := ListCheckpoints(t.TempDir(), "agent-1", CheckpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %+v, want none", runs)
	}
	if _, err := ListCheckpoints(t.TempDir(), "", CheckpointOptions{}); err == nil {
		t.Fatal("an agent id is required")
	}
}

func TestRestoreCheckpointPutsBackAndDeletes(t *testing.T) {
	dir := t.TempDir()
	seedCheckpoint(t, dir, "agent-1", "run-a", time.Now().UTC(), map[string]string{
		"a.txt":     "before",
		"new/b.txt": "",
	})
	// The run's changes.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new", "b.txt"), []byte("made"), 0o644); err != nil {
		t.Fatal(err)
	}

	restored, err := RestoreCheckpoint(dir, "agent-1", "run-a", CheckpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Fatalf("restored = %+v, want two", restored)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(got) != "before" {
		t.Fatalf("a.txt = %q, %v; want before", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("new/b.txt is a create and must be deleted, stat err = %v", err)
	}
}

func TestRestoreCheckpointRefusesAPathOutsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	// A manifest that names its way out, as a hand-edited one could.
	runDir := filepath.Join(dir, ".anvil", "checkpoints", "agent-1", "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := checkpoint.Manifest{
		AgentID: "agent-1", RunID: "run-a", CreatedAt: time.Now().UTC(),
		Files: []checkpoint.Entry{{Path: "../outside.txt", Existed: false}},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreCheckpoint(dir, "agent-1", "run-a", CheckpointOptions{}); err == nil {
		t.Fatal("a manifest path outside the workspace must be refused")
	}
}

func TestRestoreCheckpointMissingRun(t *testing.T) {
	if _, err := RestoreCheckpoint(t.TempDir(), "agent-1", "nope", CheckpointOptions{}); err == nil {
		t.Fatal("a run with no checkpoint cannot be restored")
	}
}

func TestCheckpointsHonourACustomDir(t *testing.T) {
	dir := t.TempDir()
	cpRoot := t.TempDir()
	if err := checkpoint.Capture(cpRoot, "agent-1", "run-a", "a.txt", filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	opts := CheckpointOptions{Dir: cpRoot}
	runs, err := ListCheckpoints(dir, "agent-1", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run-a" {
		t.Fatalf("runs = %+v, want run-a from the custom root", runs)
	}
	if runs, _ := ListCheckpoints(dir, "agent-1", CheckpointOptions{}); len(runs) != 0 {
		t.Fatalf("the default root is empty; got %+v", runs)
	}
}

func TestDeleteAgentForgetsItsCheckpoints(t *testing.T) {
	dir := t.TempDir()
	id := newStoredAgent(t, dir)
	seedCheckpoint(t, dir, id, "run-a", time.Now().UTC(), map[string]string{"a.txt": "before"})

	if err := DeleteAgent(context.Background(), id, ListOptions{Local: LocalOptions{CWD: dir}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".anvil", "checkpoints", id)); !os.IsNotExist(err) {
		t.Fatalf("checkpoints outlived the agent, stat err = %v", err)
	}
}
