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

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

// audit #21: Send(WithForce) waits on the previous run with the caller's context;
// a context that is already done returns at once, and the new Send then fails
// while the previous run is still active.
func TestAuditForceSendWithCancelledContextStillStarts(t *testing.T) {
	t.Skip("audit: open, see audit.md #21")
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
	t.Skip("audit: open, see audit.md #19")
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
	t.Skip("audit: open, see audit.md #18")
	dir := t.TempDir()
	opts := Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	}
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()
	a.complete = blockingCompleter{}
	if _, err := a.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// While the run is still active, another handle must not take the session.
	if b, err := Resume(context.Background(), id, opts); err == nil {
		b.Close()
		t.Fatal("Resume succeeded while the first run was still active, so the lock was released early")
	}
}

// audit #20: Send reads a.closed without the lock while Close writes it under the
// lock. Only a race detector sees this, so the test runs the two concurrently and
// relies on `go test -race` to report it.
func TestAuditSendAndCloseDoNotRace(t *testing.T) {
	// Needs a C compiler for -race (not present on this machine); run with
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
	t.Skip("audit: open, see audit.md #5")
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
	runs, err := ListCheckpoints(cpDir, a.AgentID(), CheckpointOptions{})
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
