// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

func TestJournalSequenceIsDurableBeforeLiveDelivery(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = completerFunc(func(_ context.Context, _ llm.Request) (*llm.Response, error) {
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
	})

	run, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	// Send acknowledgement means run_start is already synced, even if the
	// execution goroutine has not reached its first provider event yet.
	backend := store.New(dir)
	recs, err := backend.Load(a.AgentID())
	if err != nil {
		t.Fatal(err)
	}
	accepted := false
	for _, rec := range recs {
		if rec.Type == store.TypeRunStart && rec.RunID == run.ID() {
			accepted = true
		}
	}
	if !accepted {
		t.Fatal("Send returned before run_start was durable")
	}

	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := backend.LoadRunEvents(a.AgentID(), run.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("run emitted no durable events")
	}
	for i, event := range events {
		if event.Seq != uint64(i+1) {
			t.Fatalf("event %d sequence=%d", i, event.Seq)
		}
		var decoded Event
		if err := json.Unmarshal(event.Event, &decoded); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if decoded.Type == EventReasoning {
			t.Fatal("reasoning reached the durable journal")
		}
	}
	runs, err := a.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].LastSeq != events[len(events)-1].Seq {
		t.Fatalf("runs=%+v events=%+v", runs, events)
	}
}

func TestRunLeaseRenewal(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
		HostID: "host-test", LeaseDuration: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	a.complete = completerFunc(func(_ context.Context, _ llm.Request) (*llm.Response, error) {
		close(started)
		<-release
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
	})
	run, err := a.Send(context.Background(), "wait")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	deadline := time.Now().Add(2 * time.Second)
	running := 0
	for running < 2 && time.Now().Before(deadline) {
		recs, loadErr := store.New(dir).Load(a.AgentID())
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		running = 0
		for _, rec := range recs {
			if rec.Type == store.TypeRunState && rec.RunID == run.ID() && rec.RunState == "running" {
				running++
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if running < 2 {
		t.Fatalf("running lease records=%d, want initial state plus renewal", running)
	}
	close(release)
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestJournalReplayOmitsReasoningAndKeepsSequencesDense(t *testing.T) {
	var mu sync.Mutex
	var seqs []uint64
	var events []Event
	run := newRun("run-private", func(error) {}, func(seq uint64, raw json.RawMessage) error {
		var event Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		mu.Lock()
		seqs = append(seqs, seq)
		events = append(events, event)
		mu.Unlock()
		return nil
	})
	run.emit(Event{Type: EventReasoning, Text: "private chain of thought"})
	run.emit(Event{Type: EventProgress, Text: "ephemeral spinner"})
	run.emit(Event{Type: EventAssistant, Text: "visible answer"})
	run.emit(Event{Type: EventDone})

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0].Type != EventAssistant || events[1].Type != EventDone {
		t.Fatalf("durable events=%+v", events)
	}
	if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
		t.Fatalf("sequences=%v", seqs)
	}
}

func TestJournalReplayRejectsUnknownVersionBeforeRunProjection(t *testing.T) {
	records := []store.Record{
		{Type: store.TypeRunStart, RunID: "run-1"},
		{
			Type: store.TypeRunEvent, RunID: "run-1", RunSeq: 1,
			JournalVersion: store.JournalSchemaVersion + 1,
			Event:          json.RawMessage(`{"Type":"assistant"}`),
		},
	}
	runs, err := runsFromRecords(records)
	if !errors.Is(err, store.ErrJournalVersion) {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	if runs != nil {
		t.Fatalf("unknown journal version exposed partial runs: %+v", runs)
	}
}
