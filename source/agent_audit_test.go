// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

// audit #21: Send(WithForce) waits on the previous run with the caller's context;
// a context that is already done returns at once, and the new Send then fails
// while the previous run is still active.
func TestAuditForceSendWithCancelledContextStillStarts(t *testing.T) {
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = blockingCompleter{}
	if _, err := a.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	done, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = a.Send(done, "second", WithForce())
	if err != nil {
		t.Fatalf("Send(WithForce) with a cancelled context failed: %v", err)
	}
}

// audit #19: persist discards the Append error, so a failed write is reported to
// no one and Wait returns finished.
func TestAuditPersistFailureIsReported(t *testing.T) {
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = fixedResponse{resp: &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}}
	a.store = &failingStore{Backend: a.store}
	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err == nil && res.Status == "finished" {
		t.Fatal("a failed append was not reported: the run finished with no error")
	}
}

// failingStore wraps a real backend and fails every Append, so the run's writes
// are the only thing that breaks.
type failingStore struct {
	store.Backend
}

func (failingStore) Append(string, store.Record) error {
	return errors.New("disk full")
}

// audit #18: Close does not cancel the active run and releases the store lock
// while the run is still writing, so a Resume of the same id succeeds and the
// first run's run_end never reaches the file.
func TestAuditCloseDoesNotLeaveRunWritingUnlocked(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	}
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	a.complete = blockingCompleter{}
	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Close must not return while the run can still write: once it returns, the
	// run has ended, so nothing can append to the file after the claim is released.
	select {
	case <-run.done:
	default:
		t.Fatal("Close returned while the run was still active, so it could write after the store was released")
	}
}

// audit #20: Send reads a.closed without the lock while Close writes it under the
// lock. Only a race detector sees this, so the test runs the two concurrently and
// relies on `go test -race` to report it.
func TestAuditSendAndCloseDoNotRace(t *testing.T) {
	// Reproduced under -race on AlmaLinux 10.2 (cgo). Skipped here because this
	// Windows machine has no C compiler. Run with
	// CGO_ENABLED=1 and a gcc on PATH to reproduce.
	t.Skip("audit: open, see audit.md #20 (requires -race and cgo)")
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.complete = fixedResponse{resp: &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Close()
	}()
	a.Send(context.Background(), "hi")
	<-done
}

// audit #5: captureBefore drops in-workspace names that begin with "..", so the
// pre-image of "..env" never reaches the run's checkpoint.
func TestAuditCheckpointCapturesDotDotNames(t *testing.T) {
	cwd := t.TempDir()
	cpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "..env"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:       LocalOptions{CWD: cwd},
		Checkpoints: &CheckpointOptions{Dir: cpDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	args, _ := json.Marshal(map[string]any{"path": "..env", "content": "new"})
	a.complete = &auditScript{replies: []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "write_file", Args: args}}},
		{Role: llm.RoleAssistant, Content: "done"},
	}}
	run, err := a.Send(context.Background(), "edit it")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := ListCheckpoints(cpDir, a.AgentID(), CheckpointOptions{Dir: cpDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		for _, f := range r.Files {
			if f.Path == "..env" {
				return
			}
		}
	}
	t.Fatalf("the pre-image of ..env is not in any checkpoint: %+v", runs)
}

// auditScript replies with the given messages in order, then with a plain "end".
type auditScript struct {
	replies []llm.Message
	n       int
}

func (s *auditScript) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if s.n >= len(s.replies) {
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "end"}}, nil
	}
	m := s.replies[s.n]
	s.n++
	return &llm.Response{Message: m}, nil
}

// audit #29: buildRegistry discards the error from index.Build, so Create succeeds
// when the project index cannot be built, and nothing says the index is missing.
func TestAuditIndexBuildFailureIsReported(t *testing.T) {
	cwd := t.TempDir()
	// The agent store lives in .anvil/agents and must work; only the index
	// directory .anvil/index is blocked, by a regular file where it must go.
	if err := os.MkdirAll(filepath.Join(cwd, ".anvil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".anvil", "index"), []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: cwd, EnsureIndex: true},
	})
	if err == nil {
		a.Close()
		t.Fatal("Create succeeded although the project index could not be built")
	}
}

// audit #23: createAgent appends the meta record before buildSystemPrompt runs, so a
// failed prompt closes the store but leaves a meta-only file that lists as a session.
func TestAuditFailedCreateLeavesNoSession(t *testing.T) {
	cwd := t.TempDir()
	// AGENTS.md as a directory: reading it fails, so buildSystemPrompt fails.
	if err := os.Mkdir(filepath.Join(cwd, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := Options{APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: cwd, LoadRules: true}}
	if _, err := Create(context.Background(), opts); err == nil {
		t.Fatal("Create succeeded although the project rules could not be loaded")
	}
	infos, err := List(context.Background(), ListOptions{Local: LocalOptions{CWD: cwd}})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("a failed Create left %d session(s) behind: %+v", len(infos), infos)
	}
}

// audit #27: ForkAgent rewrites only AgentID on meta records, so a forked subagent
// keeps ParentAgentID and Kind="subagent". Deleting the original parent then removes
// the fork too, through subagentDescendants.
func TestAuditForkOfSubagentDoesNotBecomeAChild(t *testing.T) {
	cwd := t.TempDir()
	lo := LocalOptions{CWD: cwd}
	st := store.New(cwd)
	defer st.Close()
	parent := "agent-parent-27"
	child := "agent-child-27"
	now := time.Now()
	if err := st.Append(parent, store.Record{Type: store.TypeMeta, AgentID: parent, CWD: cwd, Time: now}); err != nil {
		t.Fatal(err)
	}
	for _, rec := range []store.Record{
		{Type: store.TypeMeta, AgentID: child, CWD: cwd, Time: now, ParentAgentID: parent, Kind: "subagent"},
		{Type: store.TypeRunStart, AgentID: child, RunID: "run-1", Time: now},
		{Type: store.TypeRunEnd, AgentID: child, RunID: "run-1", Status: "finished", Time: now},
	} {
		if err := st.Append(child, rec); err != nil {
			t.Fatal(err)
		}
	}
	newID, err := ForkAgent(context.Background(), child, "run-1", ListOptions{Local: lo})
	if err != nil {
		t.Fatalf("ForkAgent: %v", err)
	}
	if err := DeleteAgent(context.Background(), parent, ListOptions{Local: lo}); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if _, err := Get(context.Background(), newID, ListOptions{Local: lo}); err != nil {
		t.Fatalf("a fork of a subagent was deleted with the parent: %v", err)
	}
}
