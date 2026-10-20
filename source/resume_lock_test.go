// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"testing"
)

// TestResumeRefusesASecondLiveHandle is the regression guard for the poisoned
// session: a second live handle on one agent id used to be allowed, and the two
// then interleaved their runs into a single transcript — which is how an
// assistant turn ended up with a tool_call nobody answered, breaking every
// later turn. The editor makes this easy to reach, because closing a chat tab
// does not stop the turn it was showing.
func TestResumeRefusesASecondLiveHandle(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	}

	first, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := first.AgentID()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// One live handle.
	open, err := Resume(context.Background(), id, opts)
	if err != nil {
		t.Fatalf("first resume: %v", err)
	}

	// A second one on the same id must be refused, and the message must say
	// what to do rather than surface as a mysterious host error.
	second, err := Resume(context.Background(), id, opts)
	if err == nil {
		_ = second.Close()
		t.Fatal("a second live handle on one agent id was allowed")
	}
	if !strings.Contains(err.Error(), "already open") {
		t.Fatalf("error does not explain the conflict: %v", err)
	}
	if ae, ok := err.(*Error); !ok || ae.Retryable {
		t.Fatalf("want a non-retryable *Error, got %T %v", err, err)
	}

	// Closing the first releases the claim, so the window that reopened it can
	// resume normally.
	if err := open.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Resume(context.Background(), id, opts)
	if err != nil {
		t.Fatalf("resume after close: %v", err)
	}
	_ = again.Close()
}

// TestResumeOfDistinctSessionsIsUnaffected pins the scope: two conversations in
// one workspace are the ordinary case and must both open.
func TestResumeOfDistinctSessionsIsUnaffected(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	}
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	idA, idB := a.AgentID(), b.AgentID()
	_ = a.Close()
	_ = b.Close()

	openA, err := Resume(context.Background(), idA, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer openA.Close()
	openB, err := Resume(context.Background(), idB, opts)
	if err != nil {
		t.Fatalf("a second conversation must open: %v", err)
	}
	defer openB.Close()
}
