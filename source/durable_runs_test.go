// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestRunEventsReportsRetainedPrefixAsTruncated(t *testing.T) {
	dir := t.TempDir()
	backend := store.New(dir)
	agentID, runID := "agent-retained", "run-retained"
	for _, rec := range []store.Record{
		{Type: store.TypeMeta, AgentID: agentID},
		{Type: store.TypeRunStart, RunID: runID},
	} {
		if err := backend.Append(agentID, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendRunState(backend, agentID, store.Record{
		RunID: runID, RunState: "running", HostID: "host-live",
	}); err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= 5; seq++ {
		if err := backend.AppendRunEventLimited(
			agentID, runID, seq,
			json.RawMessage(`{"type":"assistant","text":"tail"}`),
			store.JournalLimit{MaxEvents: 2},
		); err != nil {
			t.Fatal(err)
		}
	}
	page, err := GetRunEvents(context.Background(), agentID, runID, 0, 10, ListOptions{
		Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Truncated || page.NextSeq != 5 || len(page.Events) != 2 ||
		page.Events[0].Seq != 4 || page.Events[1].Seq != 5 {
		t.Fatalf("page=%+v", page)
	}
}

func TestTaskHistoryIsReadOnlyLatestProjection(t *testing.T) {
	dir := t.TempDir()
	backend := store.New(dir)
	agentID := "agent-history"
	if err := backend.Append(agentID, store.Record{Type: store.TypeMeta, AgentID: agentID}); err != nil {
		t.Fatal(err)
	}
	first := store.Record{
		TaskID: "task-1", TaskSeq: 1, TaskKind: "shell", TaskTitle: "build",
		TaskState: "running", AgentID: agentID, ParentRunID: "run-1",
		HostID: "host-old", StartedAt: time.Now().UTC(),
	}
	if err := store.AppendTaskState(backend, agentID, first); err != nil {
		t.Fatal(err)
	}
	last := first
	last.TaskSeq, last.TaskState, last.Cause = 2, "interrupted", store.LostHostCause
	last.EndedAt = time.Now().UTC()
	if err := store.AppendTaskState(backend, agentID, last); err != nil {
		t.Fatal(err)
	}
	history, err := TaskHistory(context.Background(), agentID, "run-1", ListOptions{
		Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Seq != 2 || history[0].State != "interrupted" {
		t.Fatalf("history=%+v", history)
	}
	// Reading durable history must not create a live manager or a cancellation
	// capability; only an Agent handle can expose those.
	if backend.Exists("task-1") {
		t.Fatal("task history invented a task-backed agent store")
	}
}
