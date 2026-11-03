// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestSessionHistoryContextParityAndTarget(t *testing.T) {
	jsonDir := t.TempDir()
	sqliteDir := t.TempDir()
	seedSessionSearchCorpus(t, jsonDir, store.KindJSONL)
	seedSessionSearchCorpus(t, sqliteDir, store.KindSQLite)

	load := func(dir, kind string) SessionHistoryContextResult {
		t.Helper()
		result, err := SessionHistoryContext(context.Background(), SessionHistoryContextOptions{
			Local: LocalOptions{CWD: dir, Store: kind}, AgentID: "agent-alpha",
			RecordPos: 3, Before: 1, After: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	jsonResult := load(jsonDir, store.KindJSONL)
	sqliteResult := load(sqliteDir, store.KindSQLite)
	if !reflect.DeepEqual(jsonResult, sqliteResult) {
		t.Fatalf("JSONL=%+v\nSQLite=%+v", jsonResult, sqliteResult)
	}
	if len(jsonResult.Entries) != 3 || jsonResult.TargetIndex != 1 {
		t.Fatalf("result=%+v", jsonResult)
	}
	if got := jsonResult.Entries[jsonResult.TargetIndex].Content; got != "I will inspect the engine." {
		t.Fatalf("target=%q", got)
	}
	if !jsonResult.ClippedAfter {
		t.Fatalf("expected a bounded window: %+v", jsonResult)
	}
}

func TestSessionHistoryContextAnchorsNonTranscriptRecordAndBoundsSides(t *testing.T) {
	dir := t.TempDir()
	seedSessionSearchCorpus(t, dir, store.KindJSONL)
	result, err := SessionHistoryContext(context.Background(), SessionHistoryContextOptions{
		Local: LocalOptions{CWD: dir}, AgentID: "agent-alpha",
		RecordPos: 6, Before: 99, After: 99,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) > 41 {
		t.Fatalf("unbounded context: %d entries", len(result.Entries))
	}
	if result.TargetIndex < 0 || result.TargetIndex >= len(result.Entries) {
		t.Fatalf("target index=%d entries=%d", result.TargetIndex, len(result.Entries))
	}
}

func TestSessionHistoryContextRejectsExpiredAnchorAndCancellation(t *testing.T) {
	dir := t.TempDir()
	seedSessionSearchCorpus(t, dir, store.KindJSONL)
	_, err := SessionHistoryContext(context.Background(), SessionHistoryContextOptions{
		Local: LocalOptions{CWD: dir}, AgentID: "agent-alpha", RecordPos: 999,
	})
	var anchorErr *SessionHistoryAnchorError
	if !errors.As(err, &anchorErr) {
		t.Fatalf("err=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = SessionHistoryContext(ctx, SessionHistoryContextOptions{
		Local: LocalOptions{CWD: dir}, AgentID: "agent-alpha", RecordPos: 2,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
