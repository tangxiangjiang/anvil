// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

// reasoningServer streams the given SSE chunks and hands back the request body
// it received, so a test can assert on both directions of the call.
func reasoningServer(t *testing.T, chunks ...string) (*httptest.Server, *string) {
	t.Helper()
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &sent
}

func streamReasoning(t *testing.T, srv *httptest.Server) ([]llm.Delta, *llm.Response) {
	t.Helper()
	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	var deltas []llm.Delta
	resp, err := c.CompleteStream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, func(d llm.Delta) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	return deltas, resp
}

// The whole of the decode fix: encoding/json ignores keys no field names, so
// before this the reasoning arrived on the socket and was dropped in silence.
func TestOpenAIStreamReasoningContent(t *testing.T) {
	srv, _ := reasoningServer(t,
		`{"choices":[{"delta":{"reasoning_content":"Let me "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"think."}}]}`,
		`{"choices":[{"delta":{"content":"42"}}]}`,
	)
	deltas, resp := streamReasoning(t, srv)

	var reasoning, text strings.Builder
	for _, d := range deltas {
		reasoning.WriteString(d.Reasoning)
		text.WriteString(d.Text)
	}
	if reasoning.String() != "Let me think." {
		t.Fatalf("reasoning=%q", reasoning.String())
	}
	if text.String() != "42" {
		t.Fatalf("text=%q", text.String())
	}
	// Reasoning is display-only, so it must not reach the assembled Message:
	// that is the value the loop appends to the conversation and re-sends.
	if resp.Message.Content != "42" {
		t.Fatalf("content=%q", resp.Message.Content)
	}
}

// OpenRouter spells the same field without the suffix.
func TestOpenAIStreamReasoningAlias(t *testing.T) {
	srv, _ := reasoningServer(t, `{"choices":[{"delta":{"reasoning":"hmm"}}]}`)
	deltas, _ := streamReasoning(t, srv)
	if len(deltas) != 1 || deltas[0].Reasoning != "hmm" {
		t.Fatalf("%+v", deltas)
	}
}

// A provider that sends neither field must keep working exactly as before.
func TestOpenAIStreamWithoutReasoningIsUnchanged(t *testing.T) {
	srv, _ := reasoningServer(t, `{"choices":[{"delta":{"content":"plain"}}]}`)
	deltas, resp := streamReasoning(t, srv)
	if len(deltas) != 1 || deltas[0].Reasoning != "" || deltas[0].Text != "plain" {
		t.Fatalf("%+v", deltas)
	}
	if resp.Message.Content != "plain" {
		t.Fatalf("content=%q", resp.Message.Content)
	}
}

// Insurance against a future field-by-field refactor of the encoder: the moment
// a reasoning field is added to a struct shared with toOpenAIMessages, model
// chain-of-thought starts being sent back upstream. The request must not carry
// it in any spelling.
func TestOpenAIStreamRequestCarriesNoReasoning(t *testing.T) {
	srv, sent := reasoningServer(t, `{"choices":[{"delta":{"content":"ok"}}]}`)
	streamReasoning(t, srv)

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*sent), &body); err != nil {
		t.Fatalf("request body: %v (%s)", err, *sent)
	}
	for _, key := range []string{"reasoning", "reasoning_content", "reasoning_effort", "include_reasoning", "thinking"} {
		if _, ok := body[key]; ok {
			t.Fatalf("request body carries %q: %s", key, *sent)
		}
	}
	// Nor nested in a message, which is where it would actually leak.
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(body["messages"], &msgs); err != nil {
		t.Fatalf("messages: %v", err)
	}
	for i, m := range msgs {
		for _, key := range []string{"reasoning", "reasoning_content", "thinking"} {
			if _, ok := m[key]; ok {
				t.Fatalf("messages[%d] carries %q: %s", i, key, *sent)
			}
		}
	}
}

// reasoningStream emits reasoning deltas and (optionally) text deltas, and
// reports the full answer in the Response the way a real adapter does.
type reasoningStream struct {
	reasoning []string
	text      []string
	answer    string
}

func (s *reasoningStream) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return nil, fmt.Errorf("should use stream")
}

func (s *reasoningStream) CompleteStream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	for _, r := range s.reasoning {
		onDelta(llm.Delta{Reasoning: r})
	}
	for _, x := range s.text {
		onDelta(llm.Delta{Text: x})
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: s.answer}}, nil
}

func runStream(t *testing.T, c llm.StreamCompleter) []runtime.Event {
	t.Helper()
	var events []runtime.Event
	res := runtime.Loop(context.Background(), c, tools.NewRegistry(t.TempDir(), false), runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "hi", MaxSteps: 4,
	}, func(ev runtime.Event) { events = append(events, ev) })
	if res.Status != "finished" {
		t.Fatalf("%+v", res)
	}
	return events
}

// A delta carrying both must yield both, reasoning first -- never one instead of
// the other, which would drop visible text.
func TestRuntimeEmitsReasoningBeforeText(t *testing.T) {
	events := runStream(t, &reasoningStream{
		reasoning: []string{"why "}, text: []string{"AB"}, answer: "AB",
	})
	var order []string
	for _, ev := range events {
		if ev.Type == "reasoning" || ev.Type == "assistant" {
			order = append(order, ev.Type)
		}
	}
	if strings.Join(order, ",") != "reasoning,assistant" {
		t.Fatalf("order=%v events=%+v", order, events)
	}
}

// streamed must stay keyed to text alone. Setting it for a reasoning delta would
// suppress the non-streaming fallback and lose the whole answer.
func TestRuntimeReasoningDoesNotSuppressTextFallback(t *testing.T) {
	events := runStream(t, &reasoningStream{
		reasoning: []string{"thinking only"}, answer: "the answer",
	})
	var assistant []string
	var reasoning []string
	for _, ev := range events {
		switch ev.Type {
		case "assistant":
			assistant = append(assistant, ev.Text)
		case "reasoning":
			reasoning = append(reasoning, ev.Text)
		}
	}
	if strings.Join(assistant, "") != "the answer" {
		t.Fatalf("assistant=%v events=%+v", assistant, events)
	}
	if strings.Join(reasoning, "") != "thinking only" {
		t.Fatalf("reasoning=%v", reasoning)
	}
}

// A reasoner emits far more tokens than its answer, and the run's event buffer
// drops silently when it fills. Coalescing bounds the reasoning event count by
// size instead of by token rate; the text is never held back.
func TestRuntimeCoalescesReasoning(t *testing.T) {
	chunks := make([]string, 10)
	for i := range chunks {
		chunks[i] = strings.Repeat("x", 50) // 500 chars total
	}
	events := runStream(t, &reasoningStream{reasoning: chunks, text: []string{"ok"}, answer: "ok"})

	var reasoning []string
	textAt := -1
	for i, ev := range events {
		switch ev.Type {
		case "reasoning":
			reasoning = append(reasoning, ev.Text)
		case "assistant":
			if textAt < 0 {
				textAt = i
			}
		}
	}
	if len(reasoning) == 0 || len(reasoning) >= len(chunks) {
		t.Fatalf("expected coalescing, got %d events from %d chunks: %v", len(reasoning), len(chunks), reasoning)
	}
	if joined := strings.Join(reasoning, ""); joined != strings.Repeat("x", 500) {
		t.Fatalf("reasoning lost or duplicated: %d chars", len(joined))
	}
	// The last flush is the round boundary, so every reasoning event precedes
	// the answer.
	for i, ev := range events {
		if ev.Type == "reasoning" && i > textAt {
			t.Fatalf("reasoning after text at %d: %+v", i, events)
		}
	}
}
