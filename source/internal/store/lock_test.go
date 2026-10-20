// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store_test

import (
	"errors"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
)

// TestSessionLockIsExclusive is the core claim: while one handle owns an agent
// id, a second claim on the same id fails instead of succeeding and
// interleaving its runs into the same transcript.
func TestSessionLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)

	first, err := st.TryLock("agent-locktest01")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	if _, err := st.TryLock("agent-locktest01"); !errors.Is(err, store.ErrSessionBusy) {
		t.Fatalf("second lock should report busy, got %v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	second, err := st.TryLock("agent-locktest01")
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = second.Release()
}

// TestSessionLockIsPerAgent pins the boundary: one workspace routinely has
// several conversations open, and they must not contend.
func TestSessionLockIsPerAgent(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)

	a, err := st.TryLock("agent-lock-a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()

	b, err := st.TryLock("agent-lock-b")
	if err != nil {
		t.Fatalf("a different agent must not contend: %v", err)
	}
	defer b.Release()

	// A different directory must not contend either.
	other := store.New(t.TempDir())
	c, err := other.TryLock("agent-lock-a")
	if err != nil {
		t.Fatalf("a different workspace must not contend: %v", err)
	}
	_ = c.Release()
}

// TestSessionLockReleaseTwiceIsSafe keeps Close idempotent, which the agent
// relies on because Close is documented as safe to call concurrently.
func TestSessionLockReleaseTwiceIsSafe(t *testing.T) {
	st := store.New(t.TempDir())
	l, err := st.TryLock("agent-lock-double")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

// TestSessionLockRejectsTraversal keeps the lock file inside the store dir.
func TestSessionLockRejectsTraversal(t *testing.T) {
	st := store.New(t.TempDir())
	if _, err := st.TryLock("../escape"); err == nil {
		t.Fatal("expected an invalid id to be refused")
	}
}
