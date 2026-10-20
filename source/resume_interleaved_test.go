// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

// TestResumeOfInterleavedTranscriptStillSendsValidHistory is the end-to-end
// guard for the reported failure, built from the shape the real store file had:
// two runs interleaved into one transcript, one of them ending with a
// tool_call whose result never landed. Resuming that session and sending a
// message must reach the provider with a history the provider accepts.
func TestResumeOfInterleavedTranscriptStillSendsValidHistory(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	}

	seed, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := seed.AgentID()
	st := seed.store

	appendMsg := func(r store.Record) {
		r.Type = store.TypeMessage
		if err := st.Append(id, r); err != nil {
			t.Fatal(err)
		}
	}
	// run A: a call that gets its answer.
	appendMsg(store.Record{Role: "user", Content: "start", RunID: "run-a"})
	appendMsg(store.Record{Role: "assistant", RunID: "run-a", ToolCalls: []store.ToolCall{
		{ID: "call_a1", Name: "shell", Args: json.RawMessage(`{}`)},
	}})
	// run B's prompt lands before run A's result: the interleaving the editor
	// produces when a second session resumes the same id.
	appendMsg(store.Record{Role: "user", Content: "why did it stop?", RunID: "run-b"})
	appendMsg(store.Record{Role: "tool", ToolCallID: "call_a1", Name: "shell",
		Content: "ok", RunID: "run-a"})
	// run A's final turn: this call never got an answer at all.
	appendMsg(store.Record{Role: "assistant", RunID: "run-a", ToolCalls: []store.ToolCall{
		{ID: "call_a2", Name: "shell", Args: json.RawMessage(`{}`)},
	}})
	_ = seed.Close()

	reopened, err := Resume(context.Background(), id, opts)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	defer reopened.Close()

	rec := &recordingCompleter{}
	reopened.complete = rec

	run, err := reopened.Send(context.Background(), "continue")
	if err != nil {
		t.Fatal(err)
	}
	for range run.Stream(context.Background()) {
	}

	if len(rec.requests) == 0 {
		t.Fatal("the resumed session made no request")
	}
	sent := rec.requests[0].Messages
	for i, m := range sent {
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for k, tc := range m.ToolCalls {
			j := i + 1 + k
			if j >= len(sent) || sent[j].Role != llm.RoleTool || sent[j].ToolCallID != tc.ID {
				var shape []string
				for _, x := range sent {
					shape = append(shape, string(x.Role)+":"+x.ToolCallID)
				}
				t.Fatalf("the resumed session would have been rejected by the provider: "+
					"call %s is unanswered\n%v", tc.ID, shape)
			}
		}
	}
	// The hole is reported as an interruption, not as a success.
	var sawInterrupted bool
	for _, m := range sent {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "interrupted") {
			sawInterrupted = true
		}
	}
	if !sawInterrupted {
		t.Fatal("the lost call was not reported to the model as interrupted")
	}
}

// recordingCompleter answers every request with a plain final answer and keeps
// what it was sent.
type recordingCompleter struct {
	requests []llm.Request
}

func (c *recordingCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	c.requests = append(c.requests, req)
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}, nil
}
