// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

const (
	sessionSearchCursorVersion = 1
	sessionSearchSnapshotTTL   = 15 * time.Minute
	maxSessionSearchSnapshots  = 32
	maxSessionSnapshotHits     = 10_000
)

var (
	ErrSessionSearchCursorInvalid  = errors.New("invalid session search cursor")
	ErrSessionSearchCursorMismatch = errors.New("session search cursor does not match query")
	ErrSessionSearchCursorExpired  = errors.New("session search cursor expired")
)

// SessionSearchCursorError is returned before any partial page when a cursor
// is malformed, stale, or bound to a different local search.
type SessionSearchCursorError struct {
	Cause   error
	Message string
}

func (e *SessionSearchCursorError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Cause.Error()
	}
	return e.Cause.Error() + ": " + e.Message
}

func (e *SessionSearchCursorError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type sessionSearchBinding struct {
	Workspace string
	Backend   string
	Query     string
}

type sessionSearchSnapshot struct {
	id      string
	binding sessionSearchBinding
	created time.Time
	expires time.Time
	ranked  []rankedSessionHit
	scanned int
	partial bool
}

type sessionSearchCursorAnchor struct {
	Score     int    `json:"score"`
	Timestamp int64  `json:"timestamp"`
	AgentID   string `json:"agent_id"`
	RecordPos uint64 `json:"record_pos"`
	Kind      string `json:"kind"`
}

type sessionSearchCursorEnvelope struct {
	Version    int                       `json:"version"`
	SnapshotID string                    `json:"snapshot_id"`
	Workspace  string                    `json:"workspace"`
	Backend    string                    `json:"backend"`
	Query      string                    `json:"query"`
	Offset     int                       `json:"offset"`
	Expires    int64                     `json:"expires"`
	Last       sessionSearchCursorAnchor `json:"last"`
}

type sessionSearchSnapshotStore struct {
	mu        sync.Mutex
	secret    []byte
	snapshots map[string]*sessionSearchSnapshot
	now       func() time.Time
}

var defaultSessionSearchSnapshots = newSessionSearchSnapshotStore(nil, time.Now)

func newSessionSearchSnapshotStore(secret []byte, now func() time.Time) *sessionSearchSnapshotStore {
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic("initialize session search cursor secret: " + err.Error())
		}
	}
	if now == nil {
		now = time.Now
	}
	return &sessionSearchSnapshotStore{
		secret: append([]byte(nil), secret...), snapshots: map[string]*sessionSearchSnapshot{}, now: now,
	}
}

func sessionSearchIdentity(cwd string, opts SessionSearchOptions, query compiledSessionSearch) sessionSearchBinding {
	workspace := filepath.Clean(cwd)
	if runtime.GOOS == "windows" {
		workspace = strings.ToLower(workspace)
	}
	workspaceSum := sha256.Sum256([]byte(workspace))
	backend := strings.ToLower(strings.TrimSpace(opts.Local.Store))
	if backend == "" {
		backend = store.KindJSONL
	}
	type bindingQuery struct {
		AgentID     string
		Positive    []string
		Negative    []string
		Roles       []string
		Kinds       []string
		Statuses    []string
		After       int64
		Before      int64
		IncludeArgs bool
	}
	positive := canonicalSessionTerms(query.positive)
	negative := canonicalSessionTerms(query.negative)
	payload, _ := json.Marshal(bindingQuery{
		AgentID: opts.AgentID, Positive: positive, Negative: negative,
		Roles: sortedSearchSet(query.roles), Kinds: sortedSearchSet(query.kinds),
		Statuses: sortedSearchSet(query.statuses), After: query.after.UTC().UnixNano(),
		Before: query.before.UTC().UnixNano(), IncludeArgs: opts.IncludeArgs,
	})
	querySum := sha256.Sum256(payload)
	return sessionSearchBinding{
		Workspace: hex.EncodeToString(workspaceSum[:16]),
		Backend:   backend,
		Query:     hex.EncodeToString(querySum[:16]),
	}
}

func canonicalSessionTerms(terms []sessionSearchTerm) []string {
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		prefix := "term:"
		if term.phrase {
			prefix = "phrase:"
		}
		out = append(out, prefix+term.text)
	}
	sort.Strings(out)
	return out
}

func sortedSearchSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s *sessionSearchSnapshotStore) first(
	binding sessionSearchBinding,
	ranked []rankedSessionHit,
	result SessionSearchResult,
	limit int,
) (SessionSearchResult, error) {
	if len(ranked) <= limit {
		return sessionSearchPage(ranked, result, limit), nil
	}
	if len(ranked) > maxSessionSnapshotHits {
		ranked = ranked[:maxSessionSnapshotHits]
		result.Partial = true
	}
	now := s.now().UTC().Truncate(time.Second)
	id, err := randomSessionSnapshotID()
	if err != nil {
		return SessionSearchResult{}, newError("create search snapshot", false, err)
	}
	snapshot := &sessionSearchSnapshot{
		id: id, binding: binding, created: now, expires: now.Add(sessionSearchSnapshotTTL),
		ranked: append([]rankedSessionHit(nil), ranked...), scanned: result.Scanned, partial: result.Partial,
	}
	s.mu.Lock()
	s.purgeLocked(now)
	s.evictLocked()
	s.snapshots[id] = snapshot
	s.mu.Unlock()

	page := sessionSearchPage(ranked[:limit], result, limit)
	page.NextCursor, err = s.encode(snapshot, limit)
	if err != nil {
		s.mu.Lock()
		delete(s.snapshots, id)
		s.mu.Unlock()
		return SessionSearchResult{}, err
	}
	return page, nil
}

func (s *sessionSearchSnapshotStore) resume(
	ctx context.Context,
	cursor string,
	binding sessionSearchBinding,
	limit int,
	backend store.Backend,
) (SessionSearchResult, error) {
	if err := s.validate(cursor, binding); err != nil {
		return SessionSearchResult{}, err
	}
	envelope, err := s.decode(cursor)
	if err != nil {
		return SessionSearchResult{}, err
	}
	now := s.now().UTC()
	if envelope.Expires <= now.Unix() {
		return SessionSearchResult{}, cursorError(ErrSessionSearchCursorExpired, "cursor TTL elapsed")
	}
	if envelope.Workspace != binding.Workspace || envelope.Backend != binding.Backend ||
		envelope.Query != binding.Query {
		return SessionSearchResult{}, cursorError(ErrSessionSearchCursorMismatch, "workspace, backend, or query changed")
	}

	s.mu.Lock()
	s.purgeLocked(now)
	snapshot := s.snapshots[envelope.SnapshotID]
	if snapshot == nil {
		s.mu.Unlock()
		return SessionSearchResult{}, cursorError(ErrSessionSearchCursorExpired, "snapshot is unavailable")
	}
	if snapshot.binding != binding || !snapshot.expires.Equal(time.Unix(envelope.Expires, 0)) {
		s.mu.Unlock()
		return SessionSearchResult{}, cursorError(ErrSessionSearchCursorMismatch, "snapshot binding changed")
	}
	ranked := snapshot.ranked
	scanned := snapshot.scanned
	partial := snapshot.partial
	s.mu.Unlock()

	if envelope.Offset <= 0 || envelope.Offset > len(ranked) {
		return SessionSearchResult{}, cursorError(ErrSessionSearchCursorInvalid, "offset is outside snapshot")
	}
	if envelope.Last != sessionCursorAnchor(ranked[envelope.Offset-1]) {
		return SessionSearchResult{}, cursorError(ErrSessionSearchCursorInvalid, "sort anchor does not match snapshot")
	}
	end := min(envelope.Offset+limit, len(ranked))
	result := SessionSearchResult{Hits: []SessionSearchHit{}, Scanned: scanned, Partial: partial}
	exists := map[string]bool{}
	checked := map[string]bool{}
	for _, item := range ranked[envelope.Offset:end] {
		if err := ctx.Err(); err != nil {
			return SessionSearchResult{}, err
		}
		if !checked[item.hit.AgentID] {
			exists[item.hit.AgentID] = backend.Exists(item.hit.AgentID)
			checked[item.hit.AgentID] = true
		}
		if exists[item.hit.AgentID] {
			result.Hits = append(result.Hits, item.hit)
		}
	}
	if end < len(ranked) {
		result.NextCursor, err = s.encode(snapshot, end)
		if err != nil {
			return SessionSearchResult{}, err
		}
	} else {
		s.mu.Lock()
		delete(s.snapshots, snapshot.id)
		s.mu.Unlock()
	}
	return result, nil
}

func (s *sessionSearchSnapshotStore) validate(cursor string, binding sessionSearchBinding) error {
	envelope, err := s.decode(cursor)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if envelope.Expires <= now.Unix() {
		return cursorError(ErrSessionSearchCursorExpired, "cursor TTL elapsed")
	}
	if envelope.Workspace != binding.Workspace || envelope.Backend != binding.Backend ||
		envelope.Query != binding.Query {
		return cursorError(ErrSessionSearchCursorMismatch, "workspace, backend, or query changed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(now)
	snapshot := s.snapshots[envelope.SnapshotID]
	if snapshot == nil {
		return cursorError(ErrSessionSearchCursorExpired, "snapshot is unavailable")
	}
	if snapshot.binding != binding || !snapshot.expires.Equal(time.Unix(envelope.Expires, 0)) {
		return cursorError(ErrSessionSearchCursorMismatch, "snapshot binding changed")
	}
	if envelope.Offset <= 0 || envelope.Offset > len(snapshot.ranked) {
		return cursorError(ErrSessionSearchCursorInvalid, "offset is outside snapshot")
	}
	if envelope.Last != sessionCursorAnchor(snapshot.ranked[envelope.Offset-1]) {
		return cursorError(ErrSessionSearchCursorInvalid, "sort anchor does not match snapshot")
	}
	return nil
}

func (s *sessionSearchSnapshotStore) encode(snapshot *sessionSearchSnapshot, offset int) (string, error) {
	if snapshot == nil || offset <= 0 || offset > len(snapshot.ranked) {
		return "", cursorError(ErrSessionSearchCursorInvalid, "cannot encode invalid offset")
	}
	envelope := sessionSearchCursorEnvelope{
		Version: sessionSearchCursorVersion, SnapshotID: snapshot.id,
		Workspace: snapshot.binding.Workspace, Backend: snapshot.binding.Backend,
		Query: snapshot.binding.Query, Offset: offset, Expires: snapshot.expires.Unix(),
		Last: sessionCursorAnchor(snapshot.ranked[offset-1]),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *sessionSearchSnapshotStore) decode(cursor string) (sessionSearchCursorEnvelope, error) {
	var envelope sessionSearchCursorEnvelope
	parts := strings.Split(cursor, ".")
	if len(parts) != 2 {
		return envelope, cursorError(ErrSessionSearchCursorInvalid, "malformed envelope")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return envelope, cursorError(ErrSessionSearchCursorInvalid, "invalid payload encoding")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return envelope, cursorError(ErrSessionSearchCursorInvalid, "invalid signature encoding")
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return envelope, cursorError(ErrSessionSearchCursorInvalid, "signature mismatch")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, cursorError(ErrSessionSearchCursorInvalid, "invalid payload")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return envelope, cursorError(ErrSessionSearchCursorInvalid, "trailing payload data")
	}
	if envelope.Version != sessionSearchCursorVersion {
		return envelope, cursorError(
			ErrSessionSearchCursorInvalid, fmt.Sprintf("unsupported version %d", envelope.Version),
		)
	}
	if envelope.SnapshotID == "" || envelope.Workspace == "" || envelope.Backend == "" ||
		envelope.Query == "" || envelope.Offset <= 0 || envelope.Expires <= 0 {
		return envelope, cursorError(ErrSessionSearchCursorInvalid, "missing required cursor state")
	}
	return envelope, nil
}

func sessionCursorAnchor(item rankedSessionHit) sessionSearchCursorAnchor {
	return sessionSearchCursorAnchor{
		Score: item.score, Timestamp: item.hit.Timestamp.UnixNano(),
		AgentID: item.hit.AgentID, RecordPos: item.hit.RecordPos, Kind: item.hit.Kind,
	}
}

func (s *sessionSearchSnapshotStore) purgeLocked(now time.Time) {
	for id, snapshot := range s.snapshots {
		if !snapshot.expires.After(now) {
			delete(s.snapshots, id)
		}
	}
}

func (s *sessionSearchSnapshotStore) evictLocked() {
	for len(s.snapshots) >= maxSessionSearchSnapshots {
		var oldest *sessionSearchSnapshot
		for _, snapshot := range s.snapshots {
			if oldest == nil || snapshot.created.Before(oldest.created) ||
				(snapshot.created.Equal(oldest.created) && snapshot.id < oldest.id) {
				oldest = snapshot
			}
		}
		if oldest == nil {
			return
		}
		delete(s.snapshots, oldest.id)
	}
}

func randomSessionSnapshotID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func cursorError(cause error, message string) error {
	return &SessionSearchCursorError{Cause: cause, Message: message}
}
