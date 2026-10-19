// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/checkpoint"
	"github.com/anvil-dev/anvil/internal/tools"
)

// The pre-image belongs to the call that runs, not the one the model proposed:
// an approval that rewrites a path moves the change, and the undo has to follow
// it. Capturing the configured-then-abandoned path would leave the file that
// actually changed with no pre-image at all.
func TestCaptureUsesTheRewrittenArguments(t *testing.T) {
	dir := t.TempDir()
	cpRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "original.txt"), []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	reg.SetCheckpoints(cpRoot)
	reg.SetRunContext("agent-1", "run-1")
	reg.SetPermission(func(ctx context.Context, call tools.GatedCall) (json.RawMessage, error) {
		return json.RawMessage(`{"path":"moved.txt","content":"written"}`), nil
	})

	res := reg.Execute(context.Background(), "write_file", mustJSON(map[string]any{
		"path": "original.txt", "content": "model's idea",
	}))
	if res.IsError {
		t.Fatal(res.Content)
	}

	run, err := checkpoint.Get(cpRoot, "agent-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Files) != 1 || run.Files[0].Path != "moved.txt" {
		t.Fatalf("captured = %+v, want only moved.txt", run.Files)
	}
	if run.Files[0].Existed {
		t.Fatalf("moved.txt is new, want a create")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "original.txt")); err != nil || string(got) != "untouched" {
		t.Fatalf("original.txt = %q, %v; want untouched", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "moved.txt")); err != nil || string(got) != "written" {
		t.Fatalf("moved.txt = %q, %v; want written", got, err)
	}
}

func TestCaptureRecordsTheExistingContent(t *testing.T) {
	dir := t.TempDir()
	cpRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	reg.SetCheckpoints(cpRoot)
	reg.SetRunContext("agent-1", "run-1")

	if res := reg.Execute(context.Background(), "write_file", mustJSON(map[string]any{
		"path": "a.txt", "content": "after",
	})); res.IsError {
		t.Fatal(res.Content)
	}

	run, err := checkpoint.Get(cpRoot, "agent-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Files) != 1 || !run.Files[0].Existed || run.Files[0].Path != "a.txt" {
		t.Fatalf("captured = %+v, want a.txt as an existing file", run.Files)
	}
}

// edit_file is the other tool that carries a body, and the seam is the same one.
func TestCaptureCoversEditFile(t *testing.T) {
	dir := t.TempDir()
	cpRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	reg.SetCheckpoints(cpRoot)
	reg.SetRunContext("agent-1", "run-1")

	if res := reg.Execute(context.Background(), "edit_file", mustJSON(map[string]any{
		"path": "a.txt", "old_string": "world", "new_string": "anvil",
	})); res.IsError {
		t.Fatal(res.Content)
	}

	run, err := checkpoint.Get(cpRoot, "agent-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Files) != 1 || !run.Files[0].Existed || run.Files[0].Path != "a.txt" {
		t.Fatalf("captured = %+v, want a.txt", run.Files)
	}
	if got, err := os.ReadFile(filepath.Join(run.Dir, "files", "a.txt")); err != nil || string(got) != "hello world" {
		t.Fatalf("saved pre-image = %q, %v; want the prior bytes", got, err)
	}
}

func TestCaptureIsOffWithoutARunAndWithoutARoot(t *testing.T) {
	dir := t.TempDir()
	cpRoot := t.TempDir()

	// A root but no run: a call outside a run belongs to no checkpoint.
	reg := tools.NewRegistry(dir, false)
	reg.SetCheckpoints(cpRoot)
	if res := reg.Execute(context.Background(), "write_file", mustJSON(map[string]any{
		"path": "a.txt", "content": "x",
	})); res.IsError {
		t.Fatal(res.Content)
	}
	if runs, err := checkpoint.List(cpRoot, "agent-1"); err != nil || len(runs) != 0 {
		t.Fatalf("runs = %+v, %v; want none", runs, err)
	}

	// A run but no root: the same default the SDK gives an embedder that never
	// asked for checkpoints.
	other := tools.NewRegistry(dir, false)
	other.SetRunContext("agent-1", "run-1")
	if res := other.Execute(context.Background(), "write_file", mustJSON(map[string]any{
		"path": "b.txt", "content": "y",
	})); res.IsError {
		t.Fatal(res.Content)
	}
	if _, err := os.Stat(filepath.Join(dir, ".anvil", "checkpoints")); !os.IsNotExist(err) {
		t.Fatalf("capture off must write nothing, stat err = %v", err)
	}
}
