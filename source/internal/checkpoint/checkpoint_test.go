// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCaptureRecordsTheFirstPreImageOnly(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	file := filepath.Join(workspace, "a.txt")
	write(t, file, "before")

	if err := Capture(root, "agent-1", "run-1", "a.txt", file); err != nil {
		t.Fatal(err)
	}
	// A run's own earlier write must not become the "before" of its later one.
	write(t, file, "after one")
	if err := Capture(root, "agent-1", "run-1", "a.txt", file); err != nil {
		t.Fatal(err)
	}

	run, err := Get(root, "agent-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Files) != 1 {
		t.Fatalf("entries = %d, want 1", len(run.Files))
	}
	entry := run.Files[0]
	if !entry.Existed || entry.Path != "a.txt" || entry.Size != int64(len("before")) {
		t.Fatalf("entry = %+v", entry)
	}
	sum := sha256.Sum256([]byte("before"))
	if entry.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha = %s, want the first pre-image", entry.SHA256)
	}
	if got := read(t, filepath.Join(run.Dir, "files", "a.txt")); got != "before" {
		t.Fatalf("saved = %q, want %q", got, "before")
	}
}

func TestCaptureMarksACreate(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()

	if err := Capture(root, "agent-1", "run-1", "new/dir/b.txt", filepath.Join(workspace, "new", "dir", "b.txt")); err != nil {
		t.Fatal(err)
	}

	run, err := Get(root, "agent-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Files) != 1 || run.Files[0].Existed {
		t.Fatalf("entries = %+v, want one create", run.Files)
	}
	if _, err := os.Stat(filepath.Join(run.Dir, "files", "new", "dir", "b.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a create has no saved body, stat err = %v", err)
	}
}

func TestCaptureIgnoresDirectoriesAndUnsafeNames(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Capture(root, "agent-1", "run-1", "sub", filepath.Join(workspace, "sub")); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(root, "agent-1", "run-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a directory alone makes no manifest, err = %v", err)
	}
	if err := Capture(root, "agent-1", "run-1", "../escape", filepath.Join(workspace, "x")); err == nil {
		t.Fatal("a traversal path must be refused")
	}
	if err := Capture(root, "..", "run-1", "a", filepath.Join(workspace, "a")); err == nil {
		t.Fatal("a traversal agent id must be refused")
	}
}

func TestListIsNewestFirst(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for i, run := range []struct {
		id string
		at time.Time
	}{
		{"run-a", base},
		{"run-c", base.Add(time.Hour)},
		{"run-b", base},
	} {
		dir := filepath.Join(root, "agent-1", run.id)
		if err := saveManifest(dir, Manifest{AgentID: "agent-1", RunID: run.id, CreatedAt: run.at, Files: []Entry{{Path: string(rune('a' + i)), Existed: true}}}); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := List(root, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(runs))
	for _, run := range runs {
		got = append(got, run.RunID)
	}
	want := []string{"run-c", "run-b", "run-a"}
	if len(got) != len(want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runs = %v, want %v", got, want)
		}
	}
}

func TestListSkipsADamagedRun(t *testing.T) {
	root := t.TempDir()
	if err := saveManifest(filepath.Join(root, "agent-1", "good"), Manifest{
		AgentID: "agent-1", RunID: "good", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "agent-1", "broken", "manifest.json"), "{not json")

	runs, err := List(root, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "good" {
		t.Fatalf("runs = %+v, want only the readable one", runs)
	}
}

func TestRestorePutsBackAndDeletes(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	kept := filepath.Join(workspace, "kept.txt")
	write(t, kept, "original")
	if err := Capture(root, "agent-1", "run-1", "kept.txt", kept); err != nil {
		t.Fatal(err)
	}
	if err := Capture(root, "agent-1", "run-1", "made.txt", filepath.Join(workspace, "made.txt")); err != nil {
		t.Fatal(err)
	}
	// The run changes the workspace the way a tool call would.
	write(t, kept, "changed")
	write(t, filepath.Join(workspace, "made.txt"), "a file the run made")

	resolve := func(rel string) (string, error) { return filepath.Join(workspace, filepath.FromSlash(rel)), nil }
	restored, err := Restore(root, "agent-1", "run-1", resolve)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, kept); got != "original" {
		t.Fatalf("kept = %q, want %q", got, "original")
	}
	if _, err := os.Stat(filepath.Join(workspace, "made.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a created file is deleted, stat err = %v", err)
	}
	if len(restored) != 2 {
		t.Fatalf("restored = %+v, want two", restored)
	}
	foundDeleted := false
	for _, r := range restored {
		if r.Path == "made.txt" && r.Deleted {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Fatalf("restored = %+v, want made.txt reported deleted", restored)
	}
}

func TestRestoreStopsWhenTheSandboxRefuses(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, "ok.txt"), "original")
	if err := Capture(root, "agent-1", "run-1", "ok.txt", filepath.Join(workspace, "ok.txt")); err != nil {
		t.Fatal(err)
	}
	// Hand-edit the manifest the way a tampered file on disk could.
	run, err := Get(root, "agent-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveManifest(run.Dir, Manifest{AgentID: "agent-1", RunID: "run-1", Files: []Entry{
		{Path: "ok.txt", Existed: true},
		{Path: "../outside.txt", Existed: true},
	}}); err != nil {
		t.Fatal(err)
	}

	resolve := func(rel string) (string, error) {
		if rel == "../outside.txt" {
			return "", errors.New("path escapes cwd")
		}
		return filepath.Join(workspace, filepath.FromSlash(rel)), nil
	}
	restored, err := Restore(root, "agent-1", "run-1", resolve)
	if err == nil {
		t.Fatal("a path the resolver refuses must stop the restore")
	}
	if len(restored) != 1 || restored[0].Path != "ok.txt" {
		t.Fatalf("restored = %+v, want only the resolvable file", restored)
	}
}

func TestRestoreMissingRun(t *testing.T) {
	if _, err := Restore(t.TempDir(), "agent-1", "nope", func(string) (string, error) { return "", nil }); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestPruneKeepsNewestAndDropsOld(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		id := "run-" + string(rune('a'+i))
		dir := filepath.Join(root, "agent-1", id)
		at := now.Add(-time.Duration(4-i) * time.Hour)
		if i == 0 {
			at = now.Add(-31 * 24 * time.Hour)
		}
		if err := saveManifest(dir, Manifest{AgentID: "agent-1", RunID: id, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}

	if err := Prune(root, "agent-1", 2, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	runs, err := List(root, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(runs))
	for _, run := range runs {
		got = append(got, run.RunID)
	}
	want := []string{"run-d", "run-c"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("runs = %v, want %v", got, want)
	}
}

func TestPruneZeroMeansDefaults(t *testing.T) {
	// The two numbers the phase promises are the ones the zero values mean.
	if DefaultKeep != 100 || DefaultMaxAge != 30*24*time.Hour {
		t.Fatalf("defaults = %d, %v; want 100 runs and 30 days", DefaultKeep, DefaultMaxAge)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "agent-1", "run-old")
	if err := saveManifest(dir, Manifest{
		AgentID: "agent-1", RunID: "run-old",
		CreatedAt: time.Now().UTC().Add(-(DefaultMaxAge + time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	if err := Prune(root, "agent-1", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(root, "agent-1", "run-old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a run past the default age is dropped, err = %v", err)
	}
}

func TestRemoveForgetsEverything(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "agent-1", "run-1", "manifest.json"), "{}")
	write(t, filepath.Join(root, "agent-2", "run-1", "manifest.json"), "{}")

	if err := Remove(root, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "agent-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agent-1 still there, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agent-2", "run-1", "manifest.json")); err != nil {
		t.Fatalf("another agent is untouched: %v", err)
	}
}
