// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
	_ "modernc.org/sqlite"
)

func TestJournalSequenceAcrossRestartAndDuplicateDetection(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			first := openBackend(t, dir, kind)
			if err := first.AppendRunEvent("agent-seq", "run-1", 1, json.RawMessage(`{"type":"assistant","text":"one"}`)); err != nil {
				t.Fatal(err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := openBackend(t, dir, kind)
			defer reopened.Close()
			if err := reopened.AppendRunEvent("agent-seq", "run-1", 2, json.RawMessage(`{"type":"assistant","text":"two"}`)); err != nil {
				t.Fatal(err)
			}
			if err := reopened.AppendRunEvent("agent-seq", "run-2", 1, json.RawMessage(`{"type":"assistant","text":"independent"}`)); err != nil {
				t.Fatalf("per-run sequence did not restart at one: %v", err)
			}
			for _, seq := range []uint64{2, 4} {
				err := reopened.AppendRunEvent("agent-seq", "run-1", seq, json.RawMessage(`{"type":"assistant"}`))
				if !errors.Is(err, store.ErrJournalSequence) {
					t.Fatalf("sequence %d: got %v, want ErrJournalSequence", seq, err)
				}
			}
			events, err := reopened.LoadRunEvents("agent-seq", "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
				t.Fatalf("events=%+v", events)
			}
		})
	}
}

func TestJournalReplayRejectsUnknownVersionBeforeReturningState(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			backend := openBackend(t, t.TempDir(), kind)
			defer backend.Close()
			if err := backend.AppendRunEvent("agent-version", "run-1", 1, json.RawMessage(`{"type":"assistant"}`)); err != nil {
				t.Fatal(err)
			}
			if err := backend.Append("agent-version", store.Record{
				Type:           store.TypeRunEvent,
				RunID:          "run-1",
				RunSeq:         2,
				JournalVersion: store.JournalSchemaVersion + 1,
				Event:          json.RawMessage(`{"type":"future"}`),
			}); err != nil {
				t.Fatal(err)
			}
			events, err := backend.LoadRunEvents("agent-version", "run-1")
			if !errors.Is(err, store.ErrJournalVersion) {
				t.Fatalf("got events=%v err=%v, want ErrJournalVersion", events, err)
			}
			if events != nil {
				t.Fatalf("unknown version exposed partial replay: %+v", events)
			}
		})
	}
}

func TestJournalTailRecoveryAndInteriorCorruption(t *testing.T) {
	t.Run("jsonl partial final write", func(t *testing.T) {
		dir := t.TempDir()
		backend := openBackend(t, dir, store.KindJSONL)
		if err := backend.AppendRunEvent("agent-tail", "run-1", 1, json.RawMessage(`{"type":"assistant"}`)); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, ".anvil", "agents", "agent-tail.jsonl")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(`{"type":"run_event","run_id":"run-1","run_seq":2`); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		reopened := openBackend(t, dir, store.KindJSONL)
		defer reopened.Close()
		events, err := reopened.LoadRunEvents("agent-tail", "run-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].Seq != 1 {
			t.Fatalf("events=%+v", events)
		}
		if err := reopened.AppendRunEvent("agent-tail", "run-1", 2, json.RawMessage(`{"type":"done"}`)); err != nil {
			t.Fatalf("append after recovered tail: %v", err)
		}
		events, err = reopened.LoadRunEvents("agent-tail", "run-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 2 || events[1].Seq != 2 {
			t.Fatalf("recovered events=%+v", events)
		}
	})

	t.Run("jsonl interior corruption", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".anvil", "agents", "agent-bad.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data := strings.Join([]string{
			`{"type":"run_event","run_id":"run-1","run_seq":1,"journal_version":1,"event":{"type":"assistant"}}`,
			`{broken}`,
			`{"type":"run_event","run_id":"run-1","run_seq":2,"journal_version":1,"event":{"type":"done"}}`,
			"",
		}, "\n")
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		backend := openBackend(t, dir, store.KindJSONL)
		defer backend.Close()
		if _, err := backend.LoadRunEvents("agent-bad", "run-1"); err == nil {
			t.Fatal("interior corruption was accepted")
		}
	})

	t.Run("sqlite malformed payload", func(t *testing.T) {
		dir := t.TempDir()
		backend := openBackend(t, dir, store.KindSQLite)
		if err := backend.AppendRunEvent("agent-bad", "run-1", 1, json.RawMessage(`{"type":"assistant"}`)); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(dir, ".anvil", "agents.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO records(agent_id, payload) VALUES(?, ?)`, "agent-bad", "{broken}"); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		reopened := openBackend(t, dir, store.KindSQLite)
		defer reopened.Close()
		if _, err := reopened.LoadRunEvents("agent-bad", "run-1"); err == nil {
			t.Fatal("malformed SQLite payload was accepted")
		}
	})
}

func TestJournalConformanceAcrossBackends(t *testing.T) {
	type projected struct {
		RunID string
		Seq   uint64
		Event string
	}
	got := make(map[string][]projected)
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		backend := openBackend(t, t.TempDir(), kind)
		for seq, raw := range []string{
			`{"type":"assistant","text":"hello"}`,
			`{"type":"tool_call","name":"read_file"}`,
			`{"type":"compact","before":10,"after":4}`,
		} {
			if err := backend.AppendRunEvent("agent-same", "run-1", uint64(seq+1), json.RawMessage(raw)); err != nil {
				t.Fatal(err)
			}
		}
		events, err := backend.LoadRunEvents("agent-same", "run-1")
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			got[kind] = append(got[kind], projected{
				RunID: event.RunID,
				Seq:   event.Seq,
				Event: string(event.Event),
			})
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(got[store.KindJSONL]) != len(got[store.KindSQLite]) {
		t.Fatalf("jsonl=%+v sqlite=%+v", got[store.KindJSONL], got[store.KindSQLite])
	}
	for i := range got[store.KindJSONL] {
		if got[store.KindJSONL][i] != got[store.KindSQLite][i] {
			t.Fatalf("event %d: jsonl=%+v sqlite=%+v", i, got[store.KindJSONL][i], got[store.KindSQLite][i])
		}
	}
}

func TestJournalBoundedRetentionAcrossBackends(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			backend := openBackend(t, dir, kind)
			if err := backend.Append("agent-bounded", store.Record{
				Type: store.TypeMessage, Role: "user", Content: "keep the transcript",
			}); err != nil {
				t.Fatal(err)
			}
			limit := store.JournalLimit{MaxEvents: 2, MaxBytes: 1 << 20}
			for seq := uint64(1); seq <= 5; seq++ {
				raw := json.RawMessage(`{"type":"assistant","text":"visible"}`)
				if err := backend.AppendRunEventLimited("agent-bounded", "run-1", seq, raw, limit); err != nil {
					t.Fatalf("append %d: %v", seq, err)
				}
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := openBackend(t, dir, kind)
			defer reopened.Close()
			events, err := reopened.LoadRunEvents("agent-bounded", "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 || events[0].Seq != 4 || events[1].Seq != 5 {
				t.Fatalf("retained events=%+v", events)
			}
			records, err := reopened.Load("agent-bounded")
			if err != nil {
				t.Fatal(err)
			}
			highWater, err := store.JournalHighWater(records)
			if err != nil || highWater["run-1"] != 5 {
				t.Fatalf("high water=%v err=%v", highWater, err)
			}
			gaps := 0
			messages := 0
			for _, rec := range records {
				if rec.Type == store.TypeRunGap {
					gaps++
					if rec.RunSeq != 4 {
						t.Fatalf("gap starts at %d, want 4", rec.RunSeq)
					}
				}
				if rec.Type == store.TypeMessage && rec.Content == "keep the transcript" {
					messages++
				}
			}
			if gaps != 1 || messages != 1 {
				t.Fatalf("gaps=%d messages=%d records=%+v", gaps, messages, records)
			}
			if err := reopened.AppendRunEventLimited(
				"agent-bounded", "run-1", 6,
				json.RawMessage(`{"type":"done"}`), limit,
			); err != nil {
				t.Fatalf("append after retained prefix: %v", err)
			}
			events, err = reopened.LoadRunEvents("agent-bounded", "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 || events[0].Seq != 5 || events[1].Seq != 6 {
				t.Fatalf("events after append=%+v", events)
			}
		})
	}
}

func TestJournalByteLimitKeepsNewestOversizedEvent(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			backend := openBackend(t, t.TempDir(), kind)
			defer backend.Close()
			limit := store.JournalLimit{MaxBytes: 16}
			for seq := uint64(1); seq <= 3; seq++ {
				if err := backend.AppendRunEventLimited(
					"agent-bytes", "run-1", seq,
					json.RawMessage(`{"type":"assistant","text":"larger than the cap"}`), limit,
				); err != nil {
					t.Fatal(err)
				}
			}
			events, err := backend.LoadRunEvents("agent-bytes", "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Seq != 3 {
				t.Fatalf("events=%+v", events)
			}
		})
	}
}

func openBackend(t *testing.T, dir, kind string) store.Backend {
	t.Helper()
	backend, err := store.Open(dir, kind)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}
