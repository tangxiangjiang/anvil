// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestOpenAIStreamDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{}}]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	var parts []string
	resp, err := c.CompleteStream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, func(d llm.Delta) { parts = append(parts, d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "Hello" {
		t.Fatalf("content=%q", resp.Message.Content)
	}
	if strings.Join(parts, "") != "Hello" || len(parts) < 2 {
		t.Fatalf("parts=%v", parts)
	}
}

func TestOpenAIStreamToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"ping\",\"arguments\":\"\"}}]}}]}\n\n")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{}\"}}]}}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := c.CompleteStream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
		Tools:    []llm.ToolDef{{Name: "ping", Parameters: json.RawMessage(`{}`)}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "ping" {
		t.Fatalf("%+v", resp.Message.ToolCalls)
	}
}

type streamMock struct {
	n int
}

func (s *streamMock) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return nil, fmt.Errorf("should use stream")
}

func (s *streamMock) CompleteStream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	s.n++
	if s.n == 1 {
		onDelta(llm.Delta{Text: "ABCD"})
		onDelta(llm.Delta{Text: "EFGH"})
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ABCDEFGH"}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
}

func TestRuntimeEmitsStreamDeltas(t *testing.T) {
	var texts []string
	var estimates []*llm.Usage
	res := runtime.Loop(context.Background(), &streamMock{}, tools.NewRegistry(t.TempDir(), false), runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "hi", MaxSteps: 4,
	}, func(ev runtime.Event) {
		if ev.UsageEstimated {
			estimates = append(estimates, ev.Usage)
		}
		if ev.Type == "assistant" {
			texts = append(texts, ev.Text)
		}
	})
	if res.Status != "finished" {
		t.Fatalf("%+v", res)
	}
	if strings.Join(texts, "") != "ABCDEFGH" || len(texts) < 2 {
		t.Fatalf("texts=%v", texts)
	}
	if len(estimates) < 3 || estimates[0].InputTokens <= 0 {
		t.Fatalf("live estimates=%+v", estimates)
	}
	if estimates[len(estimates)-1].OutputTokens <= estimates[1].OutputTokens {
		t.Fatalf("output estimate did not increase: %+v", estimates)
	}
}
