// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A worker's edits are captured under the parent's run, so one restore of the root run
// puts back what the worker changed.
func TestTreeCheckpointRestoresChildren(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pkg/a.txt": "old", "pkg/b.txt": "old"})
	opts := multiOptions(dir)
	opts.Checkpoints = &CheckpointOptions{}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	var rootRun string
	args := jsonBrief([]string{"pkg/"}, []string{"edit both"}, []string{"both say new"})
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if strings.Contains(systemOf(req), "The brief below is your task") {
			switch len(trailingToolResults(req)) {
			case 0:
				return callResponse("w1", "edit_file", editArgs("pkg/a.txt")), nil
			case 1:
				return callResponse("w2", "edit_file", editArgs("pkg/b.txt")), nil
			}
			return textResponse("[met] both say new"), nil
		}
		if len(trailingToolResults(req)) == 0 {
			rootRun = parent.currentRunID()
			return callResponse("d1", "delegate", args), nil
		}
		return textResponse("done"), nil
	})
	run, err := parent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if readFile(t, dir, "pkg/a.txt") != "new" || readFile(t, dir, "pkg/b.txt") != "new" {
		t.Fatal("the worker did not edit both files")
	}

	runs, err := ListCheckpoints(dir, parent.AgentID(), *opts.Checkpoints)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != rootRun || len(runs[0].Files) != 2 {
		t.Fatalf("the root run does not list the whole tree once: %+v (root run %s)", runs, rootRun)
	}
	restored, err := RestoreCheckpoint(dir, parent.AgentID(), rootRun, *opts.Checkpoints)
	if err != nil || len(restored) != 2 {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	for _, f := range restored {
		if f.Skipped {
			t.Fatalf("restore skipped %s: %s", f.Path, f.Reason)
		}
	}
	if readFile(t, dir, "pkg/a.txt") != "old" || readFile(t, dir, "pkg/b.txt") != "old" {
		t.Fatalf("restore left a=%q b=%q", readFile(t, dir, "pkg/a.txt"), readFile(t, dir, "pkg/b.txt"))
	}
}

// The same holds for a Complex run, across every Worker item.
func TestTreeCheckpointRestoresComplexWorkers(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pkg/a.txt": "old", "pkg/b.txt": "old"})
	opts := complexOptions(dir)
	opts.Checkpoints = &CheckpointOptions{}
	parent, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parent.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		switch stageOf(req) {
		case "decision":
			return textResponse(goodDecision), nil
		case "analyze":
			return textResponse(twoItemRoadmap()), nil
		case "worker":
			name := "pkg/a.txt"
			if strings.Contains(systemOf(req), "Goal: edit b") {
				name = "pkg/b.txt"
			}
			if len(trailingToolResults(req)) == 0 {
				return callResponse("w", "edit_file", editArgs(name)), nil
			}
			return textResponse("[met] done"), nil
		}
		return textResponse("?"), nil
	})
	run, err := parent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := ListCheckpoints(dir, parent.AgentID(), *opts.Checkpoints)
	if err != nil || len(runs) != 1 || len(runs[0].Files) != 2 {
		t.Fatalf("the Complex root run does not list both workers' files: %+v %v", runs, err)
	}
	if _, err := RestoreCheckpoint(dir, parent.AgentID(), runs[0].RunID, *opts.Checkpoints); err != nil {
		t.Fatal(err)
	}
	if readFile(t, dir, "pkg/a.txt") != "old" || readFile(t, dir, "pkg/b.txt") != "old" {
		t.Fatal("restoring the root run missed a worker's edit")
	}
}

// A single agent with no children writes the layout it always wrote: its own run, no
// child fields, nothing filed anywhere else.
func TestTreeCheckpointSingleAgentUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.txt": "old"})
	opts := subagentTestOptions(dir)
	opts.Checkpoints = &CheckpointOptions{}
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		if len(trailingToolResults(req)) == 0 {
			return callResponse("e", "edit_file", editArgs("a.txt")), nil
		}
		return textResponse("done"), nil
	})
	run, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := ListCheckpoints(dir, a.AgentID(), *opts.Checkpoints)
	if err != nil || len(runs) != 1 || len(runs[0].Files) != 1 {
		t.Fatalf("%+v %v", runs, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".anvil", "checkpoints"))
	if len(entries) != 1 || entries[0].Name() != a.AgentID() {
		t.Fatalf("a single agent filed checkpoints under %v", entries)
	}
}
