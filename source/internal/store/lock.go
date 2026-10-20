// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrSessionBusy reports that another live handle already owns this agent id.
//
// The store is an append-only file, and its readers reconstruct a conversation
// from record order. Two writers on one id therefore do not merely race — they
// interleave two runs into one transcript, which is how an assistant turn loses
// the tool result it was waiting for and poisons every later turn. One writer at
// a time is the invariant; this error is what tells a second caller it lost.
var ErrSessionBusy = errors.New("session is already open")

// SessionLock is an exclusive claim on one agent id.
//
// It is held for as long as the handle that acquired it lives, and released
// either by Release or by the process exiting — the underlying OS lock is what
// makes a crashed window recoverable instead of leaving a lock nobody owns.
type SessionLock struct {
	f *lockFile
}

// AcquireSessionLock claims agentID under dir, or reports ErrSessionBusy.
//
// The lock is advisory and per agent id: different sessions in the same
// directory never contend, which matters because one workspace routinely has
// several conversations open.
func AcquireSessionLock(dir, agentID string) (*SessionLock, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("store dir is required")
	}
	if err := safeID(agentID); err != nil {
		return nil, err
	}
	lf, err := lockExclusive(filepath.Join(dir, safeName(agentID)+".lock"))
	if err != nil {
		if errors.Is(err, errLockBusy) {
			return nil, fmt.Errorf("%w: %s", ErrSessionBusy, agentID)
		}
		return nil, fmt.Errorf("lock session %s: %w", agentID, err)
	}
	return &SessionLock{f: lf}, nil
}

// Release drops the claim. Safe to call twice.
func (l *SessionLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	return f.release()
}
