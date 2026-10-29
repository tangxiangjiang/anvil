// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var fastRetry = RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}

// sseText is a streamed assistant reply with a usage chunk, which is what the runtime asks for.
func sseText(content string) string {
	return "data: " + chunkJSON(content) + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}` + "\n\n" +
		"data: [DONE]\n\n"
}

func chunkJSON(content string) string {
	quoted, _ := json.Marshal(content)
	return `{"choices":[{"delta":{"content":` + string(quoted) + `}}]}`
}

func sseSpawn(prompt string) string {
	args, _ := json.Marshal(map[string]string{"prompt": prompt, "description": "child"})
	quoted, _ := json.Marshal(string(args))
	return `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"spawn_agent","arguments":` +
		string(quoted) + `}}]}}]}` + "\n\n" + "data: [DONE]\n\n"
}

// A 429 then a 200 is one run that finishes, with one retry event on the stream and the
// usage of the successful attempt only.
func TestRunRetriesAndReportsEvent(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"slow down"},"usage":{"prompt_tokens":900,"total_tokens":900}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseText("done")))
	}))
	defer srv.Close()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, BaseURL: srv.URL, Retry: fastRetry,
		Local: LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	run, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for ev := range run.Stream(context.Background()) {
		events = append(events, ev)
	}
	res, err := run.Wait(context.Background())
	if err != nil || res.Status != StatusFinished {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	var retries []Event
	for _, ev := range events {
		if ev.Type == EventRetry {
			retries = append(retries, ev)
		}
	}
	if len(retries) != 1 || retries[0].Attempt != 1 || !strings.Contains(retries[0].Text, "slow down") {
		t.Fatalf("retry events=%+v", retries)
	}
	if res.Usage == nil || res.Usage.InputTokens != 5 {
		t.Fatalf("usage must come from the successful attempt only: %+v", res.Usage)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

// MaxAttempts 1 restores today's behaviour: the first 429 ends the run.
func TestRunWithRetryOffFailsOnFirst429(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv.Close()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, BaseURL: srv.URL, Retry: RetryPolicy{MaxAttempts: 1},
		Local: LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	run, _ := a.Send(context.Background(), "go")
	res, _ := run.Wait(context.Background())
	// A 429 that is not retried is still a busy provider, not a generic failure.
	if res.Status != StatusError || res.Code != CodeProviderBusy || requests.Load() != 1 {
		t.Fatalf("res=%+v requests=%d", res, requests.Load())
	}
}

// The wrapper sits on the top-level client only. A child reuses it, so a provider that
// is down costs three requests under a subagent, not nine.
func TestSubagentRetryOnce(t *testing.T) {
	var mu sync.Mutex
	var childRequests, parentRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1<<20)
		n, _ := r.Body.Read(body)
		text := string(body[:n])
		mu.Lock()
		defer mu.Unlock()
		// The child's request is the one whose last user message is the spawn prompt and that
		// has no tool result yet; the parent's carries the spawn_agent result.
		if strings.Contains(text, "child work") && !strings.Contains(text, "tool_call_id") {
			childRequests++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"message":"down"}}`))
			return
		}
		parentRequests++
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(text, "tool_call_id") {
			w.Write([]byte(sseText("parent done")))
			return
		}
		w.Write([]byte(sseSpawn("child work")))
	}))
	defer srv.Close()
	opts := subagentTestOptions(t.TempDir())
	opts.BaseURL = srv.URL
	opts.Retry = fastRetry
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	run, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatalf("the parent should finish after the child's spawn fails: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if childRequests != 3 {
		t.Fatalf("child requests=%d, want 3 (one wrapper), not 9 (a wrapper under a wrapper); parent=%d", childRequests, parentRequests)
	}
}
