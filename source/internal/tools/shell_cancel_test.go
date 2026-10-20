// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// slowCommand returns an argv that keeps running well past any sane test budget
// on the current platform, and ignores a plain SIGTERM where that is possible.
func slowCommand() string {
	if runtime.GOOS == "windows" {
		// ping with a large count: a real child process that does not die when
		// its parent is asked nicely.
		return "ping -n 60 127.0.0.1"
	}
	return "sleep 60"
}

// TestShellCancelReturnsPromptly is the regression guard for "stopped" not
// stopping: cancelling a run must return from the tool call quickly, not after
// the command's own timeout.
func TestShellCancelReturnsPromptly(t *testing.T) {
	r := NewRegistry(t.TempDir(), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The tool's own timeout is far away; only the caller's cancel can end this.
	args, _ := json.Marshal(map[string]any{
		"command":         slowCommand(),
		"timeout_seconds": 120,
	})

	type outcome struct {
		res Result
		dur time.Duration
	}
	done := make(chan outcome, 1)
	go func() {
		start := time.Now()
		res := r.Execute(ctx, "shell", args)
		done <- outcome{res: res, dur: time.Since(start)}
	}()

	// Let the command actually start, then cancel.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case got := <-done:
		if got.dur > 5*time.Second {
			t.Fatalf("cancel took %s — the command outlived the run", got.dur)
		}
		if !got.res.IsError {
			t.Fatalf("a cancelled command must report an error: %+v", got.res)
		}
		if !strings.Contains(got.res.Content, "cancelled") {
			t.Fatalf("result does not say it was cancelled: %q", got.res.Content)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Execute never returned after cancel")
	}
}

// TestShellCancelStillProducesAToolResult pins the invariant the transcript
// depends on: a cancelled call returns a Result rather than an error, because the
// runtime turns a Result into the tool message that answers the call. Returning
// nothing is what left the dangling tool_call.
func TestShellCancelStillProducesAToolResult(t *testing.T) {
	r := NewRegistry(t.TempDir(), true)
	ctx, cancel := context.WithCancel(context.Background())
	args, _ := json.Marshal(map[string]any{"command": slowCommand()})
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	res := r.Execute(ctx, "shell", args)
	if res.Content == "" {
		t.Fatal("a cancelled command produced no content at all")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("this test must exercise cancellation, not a deadline")
	}
}

// TestShellNormalCommandStillWorks keeps the success path honest after moving
// off CombinedOutput.
func TestShellNormalCommandStillWorks(t *testing.T) {
	r := NewRegistry(t.TempDir(), true)
	cmd := "echo anvil-shell-ok"
	if runtime.GOOS != "windows" {
		cmd = "echo anvil-shell-ok"
	}
	args, _ := json.Marshal(map[string]any{"command": cmd})
	res := r.Execute(context.Background(), "shell", args)
	if res.IsError {
		t.Fatalf("plain echo failed: %+v", res)
	}
	if !strings.Contains(res.Content, "anvil-shell-ok") {
		t.Fatalf("output not captured: %q", res.Content)
	}
}

// TestShellTimeoutStillReported checks the timeout wording survives the rewrite.
func TestShellTimeoutStillReported(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a real short timeout")
	}
	r := NewRegistry(t.TempDir(), true)
	args, _ := json.Marshal(map[string]any{
		"command":         slowCommand(),
		"timeout_seconds": 1,
	})
	start := time.Now()
	res := r.Execute(context.Background(), "shell", args)
	if !res.IsError {
		t.Fatalf("expected a timeout error: %+v", res)
	}
	if !strings.Contains(res.Content, "killed after") {
		t.Fatalf("timeout note missing: %q", res.Content)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("timeout path took %s", d)
	}
}
