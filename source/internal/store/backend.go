// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Backend kinds for LocalOptions.Store.
const (
	KindJSONL  = "jsonl"
	KindSQLite = "sqlite"
)

// Backend persists agent session records.
type Backend interface {
	Append(agentID string, rec Record) error
	Load(agentID string) ([]Record, error)
	ListIDs() ([]string, error)
	Exists(agentID string) bool
	// TryLock claims exclusive write ownership of agentID for this process, or
	// reports ErrSessionBusy. A caller that resumes a session must hold the
	// claim for as long as it may write, because two writers interleave their
	// runs into one transcript. Backends that cannot lock return a lock whose
	// Release is a no-op rather than failing the open.
	TryLock(agentID string) (*SessionLock, error)
	// Delete forgets an agent's records. It reports ErrNotFound when there is
	// nothing to forget, rather than succeeding on a no-op: a caller that names
	// the wrong id should hear about it.
	Delete(agentID string) error
	Close() error
}

// Open returns a store backend for cwd. kind is jsonl (default) or sqlite.
func Open(cwd, kind string) (Backend, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = KindJSONL
	}
	switch kind {
	case KindJSONL:
		return New(cwd), nil
	case KindSQLite:
		return OpenSQLite(cwd)
	default:
		return nil, fmt.Errorf("unknown store backend %q", kind)
	}
}

// Close implements Backend for JSONL (no-op).
func (s *Store) Close() error { return nil }

// TryLock claims exclusive write ownership of one agent id.
//
// The lock lives beside the transcript, in the same directory, so it travels
// with the session it protects and a deleted session takes its lock with it.
func (s *Store) TryLock(agentID string) (*SessionLock, error) {
	if s == nil {
		return nil, errors.New("nil store")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	return AcquireSessionLock(s.dir, agentID)
}
