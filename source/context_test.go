// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

// compactScript answers from a list and records every request, so a test can
// tell the summarizer call apart from the runs that surround it.
type compactScript struct {
	replies  []llm.Message
	requests []llm.Request
}

func (s *compactScript) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.replies) == 0 {
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "end"}}, nil
	}
	m := s.replies[0]
	s.replies = s.replies[1:]
	return &llm.Response{Message: m}, nil
}

func seedMessages(t *testing.T, a *Agent, contents []string, persist bool) {
	t.Helper()
	for i, c := range contents {
		role := llm.RoleUser
		if i%2 == 1 {
			role = llm.RoleAssistant
		}
		m := llm.Message{Role: role, Content: c}
		a.history = append(a.history, m)
		if persist {
			a.persist(store.Record{Type: store.TypeMessage, Role: string(role), Content: c})
		}
	}
}

func compactEvents(events []Event) []Event {
	var found []Event
	for _, ev := range events {
		if ev.Type == EventCompact {
			found = append(found, ev)
		}
	}
	return found
}

func TestSendCompactsBeforeFirstRequest(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey:       "k",
		Model:        Model{ID: "m"},
		Local:        LocalOptions{CWD: dir},
		SystemPrompt: "s",
		Context:      ContextOptions{WindowTokens: 1000, AutoCompactAt: 0.25},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	seed := make([]string, 10)
	for i := range seed {
		seed[i] = fmt.Sprintf("%s-%02d", strings.Repeat("m", 90), i)
	}
	seedMessages(t, a, seed, true)

	script := &compactScript{replies: []llm.Message{
		{Role: llm.RoleAssistant, Content: "S"},
		{Role: llm.RoleAssistant, Content: "ok"},
	}}
	a.complete = script

	run, err := a.Send(context.Background(), strings.Repeat("p", 100))
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "ok" {
		t.Fatalf("%+v", res)
	}
	if got := compactEvents(events); len(got) != 1 {
		t.Fatalf("want one compact event, got %d in %+v", len(got), events)
	} else if got[0].Compact == nil || got[0].Compact.Reason != "auto" || got[0].Compact.Window != 1000 {
		t.Fatalf("compact event = %+v", got[0].Compact)
	}

	if len(script.requests) != 2 {
		t.Fatalf("want a summarizer call then the run, got %d requests", len(script.requests))
	}
	if !strings.Contains(script.requests[0].Messages[0].Content, "Summarize the following") {
		t.Fatalf("first request is not the summarizer: %+v", script.requests[0])
	}
	msgs := script.requests[1].Messages
	if len(msgs) < 3 {
		t.Fatalf("compacted request too short: %+v", msgs)
	}
	if msgs[0].Role != llm.RoleSystem || msgs[1].Name != compress.SummaryMarker || msgs[1].Role != llm.RoleUser {
		t.Fatalf("first request after compaction = %+v", msgs[:3])
	}
	if msgs[2].Role != llm.RoleUser {
		t.Fatalf("tail does not start at a user turn: %+v", msgs[2])
	}

	// The replay still shows the pre-summary turns, plus a summary divider.
	tr, err := LoadTranscript(context.Background(), a.AgentID(), TranscriptOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	var summaries, pre int
	for _, entry := range tr.Entries {
		switch entry.Kind {
		case TranscriptSummary:
			summaries++
		case TranscriptMessage:
			if strings.Contains(entry.Content, "-00") {
				pre++
			}
		}
	}
	if summaries != 1 || pre != 1 {
		t.Fatalf("transcript summaries=%d pre-summary turns=%d, want 1 and 1", summaries, pre)
	}
}

func TestSendCompactsBetweenSteps(t *testing.T) {
	dir := t.TempDir()
	probe := func(ctx context.Context, args json.RawMessage) (string, error) {
		return strings.Repeat("r", 400), nil
	}
	a, err := Create(context.Background(), Options{
		APIKey:       "k",
		Model:        Model{ID: "m"},
		Local:        LocalOptions{CWD: dir},
		SystemPrompt: "s",
		Context:      ContextOptions{WindowTokens: 1500, AutoCompactAt: 0.2},
		Tools: []ToolSpec{{
			Name:        "probe",
			Description: "returns a fixed body",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			Handler:     probe,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	seed := make([]string, 6)
	for i := range seed {
		seed[i] = fmt.Sprintf("%s-%02d", strings.Repeat("m", 90), i)
	}
	seedMessages(t, a, seed, false)

	call := llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "probe", Args: json.RawMessage(`{}`)}},
	}
	script := &compactScript{replies: []llm.Message{
		call,
		{Role: llm.RoleAssistant, Content: "S"},
		{Role: llm.RoleAssistant, Content: "done"},
	}}
	a.complete = script

	run, err := a.Send(context.Background(), strings.Repeat("p", 100))
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "done" {
		t.Fatalf("%+v", res)
	}
	got := compactEvents(events)
	if len(got) != 1 {
		t.Fatalf("want one compact event, got %d in %+v", len(got), events)
	}
	if got[0].Compact == nil || got[0].Compact.Reason != "auto" || got[0].Compact.Summarized == 0 {
		t.Fatalf("compact event = %+v", got[0].Compact)
	}

	if len(script.requests) != 3 {
		t.Fatalf("want run step, summarizer, run step; got %d requests", len(script.requests))
	}
	if !strings.Contains(script.requests[1].Messages[0].Content, "Summarize the following") {
		t.Fatalf("second request is not the summarizer: %+v", script.requests[1])
	}
	msgs := script.requests[2].Messages
	if len(msgs) < 3 || msgs[1].Name != compress.SummaryMarker || msgs[2].Role != llm.RoleUser {
		t.Fatalf("request after mid-run compaction = %+v", msgs)
	}
}

func TestAgentCompactPersistsSummaryAndTail(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey:       "k",
		Model:        Model{ID: "m"},
		Local:        LocalOptions{CWD: dir},
		SystemPrompt: "s",
		Context:      ContextOptions{WindowTokens: 10000, KeepRecent: 4},
	}
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()

	contents := make([]string, 10)
	for i := range contents {
		contents[i] = fmt.Sprintf("message-%02d", i)
	}
	seedMessages(t, a, contents, true)
	a.complete = &compactScript{replies: []llm.Message{{Role: llm.RoleAssistant, Content: "COMPACTED"}}}

	res, err := a.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted || res.Summarized != 6 {
		t.Fatalf("compact result = %+v", res)
	}
	if !strings.Contains(res.Summary, "COMPACTED") {
		t.Fatalf("summary = %q", res.Summary)
	}
	if res.Before <= res.After || res.After <= 0 {
		t.Fatalf("before/after = %d/%d", res.Before, res.After)
	}
	if len(a.history) != 5 || a.history[0].Name != compress.SummaryMarker || a.history[1].Content != contents[6] {
		t.Fatalf("tail = %+v", a.history)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	st := store.New(dir)
	recs, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	summaries, after := 0, 0
	for i, rec := range recs {
		if rec.Type != store.TypeSummary {
			continue
		}
		summaries++
		after = 0
		for _, later := range recs[i+1:] {
			if later.Type == store.TypeMessage {
				after++
			}
		}
	}
	if summaries != 1 || after != 4 {
		t.Fatalf("store summaries=%d tail records=%d, want 1 and 4", summaries, after)
	}
	st.Close()

	resumed, err := Resume(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if len(resumed.history) != 5 {
		t.Fatalf("resumed history = %+v", resumed.history)
	}
	if resumed.history[0].Name != compress.SummaryMarker || resumed.history[1].Content != contents[6] {
		t.Fatalf("resumed history = %+v", resumed.history)
	}
}

func TestAgentCompactRefusesWhileRunning(t *testing.T) {
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	seedMessages(t, a, []string{"u1", "a1", "u2", "a2"}, false)

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	_, err = a.Compact(context.Background())
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "run is in progress") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if len(a.history) != 4 {
		t.Fatalf("history changed on refusal: %+v", a.history)
	}
}

func TestAgentCompactNoopOnOneTurn(t *testing.T) {
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
	seedMessages(t, a, []string{"only"}, true)
	a.complete = &compactScript{replies: []llm.Message{{Role: llm.RoleAssistant, Content: "S"}}}

	res, err := a.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Compacted {
		t.Fatalf("compacted one turn: %+v", res)
	}

	st := store.New(dir)
	recs, err := st.Load(a.AgentID())
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range recs {
		if rec.Type == store.TypeSummary {
			t.Fatal("a no-op compaction wrote a summary record")
		}
	}
	st.Close()
}

func TestContextUsageReportsEstimateAndMark(t *testing.T) {
	a, err := Create(context.Background(), Options{
		APIKey:       "k",
		Model:        Model{ID: "m"},
		Local:        LocalOptions{CWD: t.TempDir()},
		SystemPrompt: "sys",
		Context:      ContextOptions{WindowTokens: 1000, AutoCompactAt: 0.5, KeepRecent: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	seedMessages(t, a, []string{strings.Repeat("x", 20), strings.Repeat("y", 20), strings.Repeat("z", 20)}, false)

	u := a.ContextUsage()
	if u.Messages != 3 {
		t.Fatalf("Messages = %d, want 3", u.Messages)
	}
	if u.WindowTokens != 1000 || u.CompactAtTokens != 500 {
		t.Fatalf("window/mark = %d/%d, want 1000/500", u.WindowTokens, u.CompactAtTokens)
	}
	if u.EstimatedTokens <= 0 {
		t.Fatalf("EstimatedTokens = %d", u.EstimatedTokens)
	}
	if u.EstimatedTokens != compress.Estimate([]llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: strings.Repeat("x", 20)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("y", 20)},
		{Role: llm.RoleUser, Content: strings.Repeat("z", 20)},
	}) {
		t.Fatal("estimate does not include the system prompt and history")
	}
}

func TestUnknownWindowTurnsCompactionOff(t *testing.T) {
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	u := a.ContextUsage()
	if u.WindowTokens != 0 || u.CompactAtTokens != 0 {
		t.Fatalf("unknown window reported a mark: %+v", u)
	}
}

func TestTranscriptSummaryProjection(t *testing.T) {
	recs := []store.Record{
		{Type: store.TypeMessage, RunID: "r1", Role: "user", Content: "hello"},
		{Type: store.TypeSummary, RunID: "r2", Role: "user", Content: "[conversation summary]\nS"},
		{Type: store.TypeMessage, RunID: "r2", Role: "user", Content: "again"},
		{Type: store.TypeMessage, RunID: "r2", Role: "assistant", Content: "ok"},
	}
	tr := transcriptFromRecords(recs, 0)
	if len(tr.Entries) != 4 {
		t.Fatalf("entries = %+v", tr.Entries)
	}
	if tr.Entries[1].Kind != TranscriptSummary || tr.Entries[1].Content != "[conversation summary]\nS" || tr.Entries[1].RunID != "r2" {
		t.Fatalf("summary entry = %+v", tr.Entries[1])
	}
	if tr.Dropped != 0 {
		t.Fatalf("Dropped = %d", tr.Dropped)
	}
}
