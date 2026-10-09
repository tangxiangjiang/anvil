// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store

import (
	"fmt"
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
