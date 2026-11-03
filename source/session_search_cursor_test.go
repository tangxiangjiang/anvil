// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestSessionPaginationParity(t *testing.T) {
	for _, pageSize := range []int{1, 2, 50} {
		t.Run(fmt.Sprintf("page-%d", pageSize), func(t *testing.T) {
			jsonDir := t.TempDir()
			sqliteDir := t.TempDir()
			seedSessionPaginationCorpus(t, jsonDir, store.KindJSONL)
			seedSessionPaginationCorpus(t, sqliteDir, store.KindSQLite)
			now := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
			restore := replaceSessionSnapshotsForTest(func() time.Time { return now })
			jsonHits := collectSessionSearchPages(t, SessionSearchOptions{
				Local: LocalOptions{CWD: jsonDir, Store: store.KindJSONL},
				Query: "needle", Limit: pageSize,
			})
			restore()
			restore = replaceSessionSnapshotsForTest(func() time.Time { return now })
			sqliteHits := collectSessionSearchPages(t, SessionSearchOptions{
				Local: LocalOptions{CWD: sqliteDir, Store: store.KindSQLite},
				Query: "needle", Limit: pageSize,
			})
			restore()
			if !reflect.DeepEqual(jsonHits, sqliteHits) {
				t.Fatalf("JSONL=%v SQLite=%v", jsonHits, sqliteHits)
			}
			if len(jsonHits) != 6 {
				t.Fatalf("hits=%v", jsonHits)
			}
		})
	}
}

func TestSessionCursorValidation(t *testing.T) {
	dir := t.TempDir()
	seedSessionPaginationCorpus(t, dir, store.KindJSONL)
	now := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
	restore := replaceSessionSnapshotsForTest(func() time.Time { return now })
	defer restore()
	base := SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: "needle", Limit: 1,
	}
	first, err := SearchSessions(context.Background(), base)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}

	tampered := base
	tampered.Cursor = first.NextCursor + "x"
	assertSessionCursorCause(t, tampered, ErrSessionSearchCursorInvalid)

	changed := base
	changed.Query = "different"
	changed.Cursor = first.NextCursor
	assertSessionCursorCause(t, changed, ErrSessionSearchCursorMismatch)

	wrongStore := base
	wrongStore.Local.Store = store.KindSQLite
	wrongStore.Cursor = first.NextCursor
	assertSessionCursorCause(t, wrongStore, ErrSessionSearchCursorMismatch)

	wrongWorkspace := base
	wrongWorkspace.Local.CWD = t.TempDir()
	wrongWorkspace.Cursor = first.NextCursor
	assertSessionCursorCause(t, wrongWorkspace, ErrSessionSearchCursorMismatch)

	envelope, err := defaultSessionSearchSnapshots.decode(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Version = 99
	unknown := base
	unknown.Cursor = signSessionCursorForTest(t, defaultSessionSearchSnapshots, envelope)
	assertSessionCursorCause(t, unknown, ErrSessionSearchCursorInvalid)

	// A restart loses bounded snapshot state even if a test preserves the
	// process secret; no traversal silently starts over from page one.
	secret := append([]byte(nil), defaultSessionSearchSnapshots.secret...)
	defaultSessionSearchSnapshots = newSessionSearchSnapshotStore(secret, func() time.Time { return now })
	restarted := base
	restarted.Cursor = first.NextCursor
	assertSessionCursorCause(t, restarted, ErrSessionSearchCursorExpired)
}

func TestSessionCursorExpiry(t *testing.T) {
	dir := t.TempDir()
	seedSessionPaginationCorpus(t, dir, store.KindJSONL)
	now := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
	restore := replaceSessionSnapshotsForTest(func() time.Time { return now })
	defer restore()
	opts := SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: "needle", Limit: 1,
	}
	first, err := SearchSessions(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(sessionSearchSnapshotTTL + time.Second)
	opts.Cursor = first.NextCursor
	assertSessionCursorCause(t, opts, ErrSessionSearchCursorExpired)
}

func TestSessionPaginationSnapshotInsertAndDelete(t *testing.T) {
	dir := t.TempDir()
	seedSessionPaginationCorpus(t, dir, store.KindJSONL)
	now := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
	restore := replaceSessionSnapshotsForTest(func() time.Time { return now })
	defer restore()
	opts := SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: "needle", Limit: 1,
	}
	first, err := SearchSessions(context.Background(), opts)
	if err != nil || len(first.Hits) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	backend := store.New(dir)
	if err := backend.Append("agent-new", store.Record{
		Type: store.TypeMeta, AgentID: "agent-new", Time: now, Title: "new",
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Append("agent-new", store.Record{
		Type: store.TypeMessage, RunID: "run-new", Role: "user",
		Time: now, Content: "needle inserted after snapshot",
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete("agent-b"); err != nil {
		t.Fatal(err)
	}

	opts.Cursor = first.NextCursor
	var rest []string
	sawEmptyWithNext := false
	for opts.Cursor != "" {
		page, err := SearchSessions(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Hits) == 0 && page.NextCursor != "" {
			sawEmptyWithNext = true
		}
		for _, hit := range page.Hits {
			rest = append(rest, sessionHitKey(hit))
			if hit.AgentID == "agent-new" || hit.AgentID == "agent-b" {
				t.Fatalf("snapshot included inserted/deleted hit: %+v", hit)
			}
		}
		opts.Cursor = page.NextCursor
	}
	if !sawEmptyWithNext {
		t.Fatal("deleted snapshot member did not produce the documented empty page")
	}
	if len(rest) == 0 {
		t.Fatal("surviving snapshot members were lost")
	}
}

func collectSessionSearchPages(t *testing.T, opts SessionSearchOptions) []string {
	t.Helper()
	seen := map[string]struct{}{}
	var out []string
	for pages := 0; pages < 100; pages++ {
		result, err := SearchSessions(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range result.Hits {
			key := sessionHitKey(hit)
			if _, duplicate := seen[key]; duplicate {
				t.Fatalf("duplicate hit %s", key)
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
		if result.NextCursor == "" {
			return out
		}
		opts.Cursor = result.NextCursor
	}
	t.Fatal("pagination did not terminate")
	return nil
}

func seedSessionPaginationCorpus(t *testing.T, dir, kind string) {
	t.Helper()
	backend, err := store.Open(dir, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	base := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for agentIndex, agentID := range []string{"agent-a", "agent-b", "agent-c"} {
		if err := backend.Append(agentID, store.Record{
			Type: store.TypeMeta, AgentID: agentID, CWD: dir,
			Time: base, Title: agentID,
		}); err != nil {
			t.Fatal(err)
		}
		for messageIndex := 0; messageIndex < 2; messageIndex++ {
			if err := backend.Append(agentID, store.Record{
				Type: store.TypeMessage, RunID: "run-" + agentID, Role: "user",
				Time:    base.Add(time.Duration(10-agentIndex*2-messageIndex) * time.Minute),
				Content: fmt.Sprintf("needle %s %d", agentID, messageIndex),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func sessionHitKey(hit SessionSearchHit) string {
	return fmt.Sprintf("%s/%d/%s", hit.AgentID, hit.RecordPos, hit.Kind)
}

func assertSessionCursorCause(t *testing.T, opts SessionSearchOptions, cause error) {
	t.Helper()
	result, err := SearchSessions(context.Background(), opts)
	if !errors.Is(err, cause) {
		t.Fatalf("result=%+v err=%v want=%v", result, err, cause)
	}
	if len(result.Hits) != 0 || result.NextCursor != "" || result.Scanned != 0 || result.Partial {
		t.Fatalf("cursor error returned partial page: %+v", result)
	}
}

func replaceSessionSnapshotsForTest(now func() time.Time) func() {
	prior := defaultSessionSearchSnapshots
	defaultSessionSearchSnapshots = newSessionSearchSnapshotStore(
		[]byte("0123456789abcdef0123456789abcdef"), now,
	)
	return func() { defaultSessionSearchSnapshots = prior }
}

func signSessionCursorForTest(
	t *testing.T,
	s *sessionSearchSnapshotStore,
	envelope sessionSearchCursorEnvelope,
) string {
	t.Helper()
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
