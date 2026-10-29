// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
)

// quick keeps real sleeps negligible; the policy's shape, not its clock, is under test.
var quick = llm.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}

type scriptedStatus struct {
	status int
	header map[string]string
	body   string
}

// statusServer answers the n-th request with the n-th script entry (the last one repeats)
// and counts requests. It speaks both wires: a 200 body is chosen by path.
func statusServer(t *testing.T, script ...scriptedStatus) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(script) {
			i = len(script) - 1
		}
		s := script[i]
		for k, v := range s.header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		w.Write([]byte(s.body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

const okBody = `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`

func openAIFor(srv *httptest.Server) llm.Completer {
	return llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
}

type retryRecord struct {
	mu     sync.Mutex
	events []llm.RetryInfo
}

func (r *retryRecord) ctx(parent context.Context) context.Context {
	return llm.WithRetryObserver(parent, func(i llm.RetryInfo) {
		r.mu.Lock()
		r.events = append(r.events, i)
		r.mu.Unlock()
	})
}

func TestRetry429ThenOK(t *testing.T) {
	srv, n := statusServer(t,
		// A failed attempt may carry a usage block; it must not be counted.
		scriptedStatus{status: 429, body: `{"error":{"message":"slow down"},"usage":{"prompt_tokens":999,"completion_tokens":999,"total_tokens":1998}}`},
		scriptedStatus{status: 200, body: okBody},
	)
	rec := &retryRecord{}
	resp, err := llm.WithRetry(openAIFor(srv), quick).Complete(rec.ctx(context.Background()), goldenHistory())
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("requests=%d, want 2", n.Load())
	}
	if resp.Message.Content != "ok" || resp.Usage == nil || resp.Usage.InputTokens != 7 || resp.Usage.TotalTokens != 10 {
		t.Fatalf("usage must be the successful attempt's only: %+v", resp.Usage)
	}
	if len(rec.events) != 1 || rec.events[0].Attempt != 1 || !strings.Contains(rec.events[0].Message, "slow down") {
		t.Fatalf("retry events=%+v", rec.events)
	}
}

func TestRetryDecisionTable(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		requests int32
	}{
		{"400 is wrong request", 400, 1},
		{"401", 401, 1},
		{"403", 403, 1},
		{"404", 404, 1},
		{"422", 422, 1},
		{"429", 429, 3},
		{"500", 500, 3},
		{"502", 502, 3},
		{"503", 503, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, n := statusServer(t, scriptedStatus{status: tc.status, body: `{"error":{"message":"nope"}}`})
			_, err := llm.WithRetry(openAIFor(srv), quick).Complete(context.Background(), goldenHistory())
			if err == nil {
				t.Fatal("expected an error")
			}
			if n.Load() != tc.requests {
				t.Fatalf("requests=%d, want %d", n.Load(), tc.requests)
			}
		})
	}
}

func TestRetry400NoRetry(t *testing.T) {
	srv, n := statusServer(t, scriptedStatus{status: 400, body: `{"error":{"message":"bad"}}`})
	rec := &retryRecord{}
	_, err := llm.WithRetry(openAIFor(srv), quick).Complete(rec.ctx(context.Background()), goldenHistory())
	if err == nil || n.Load() != 1 || len(rec.events) != 0 {
		t.Fatalf("err=%v requests=%d events=%d", err, n.Load(), len(rec.events))
	}
}

func TestRetryTransportErrorIsRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens: a transport error with Status 0
	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: url, Model: "m"})
	rec := &retryRecord{}
	_, err := llm.WithRetry(c, quick).Complete(rec.ctx(context.Background()), goldenHistory())
	if err == nil || len(rec.events) != 2 {
		t.Fatalf("err=%v events=%d, want 2 retries after the first of 3 attempts", err, len(rec.events))
	}
}

func TestRetryMaxAttemptsOneIsOff(t *testing.T) {
	srv, n := statusServer(t, scriptedStatus{status: 503, body: `{"error":{"message":"down"}}`})
	_, err := llm.WithRetry(openAIFor(srv), llm.RetryPolicy{MaxAttempts: 1}).Complete(context.Background(), goldenHistory())
	if err == nil || n.Load() != 1 {
		t.Fatalf("err=%v requests=%d, want exactly one", err, n.Load())
	}
}

func TestRetryZeroPolicyMeansThreeAttempts(t *testing.T) {
	p := llm.RetryPolicy{}.Resolved()
	if p.MaxAttempts != 3 || p.BaseDelay != 500*time.Millisecond || p.MaxDelay != 8*time.Second {
		t.Fatalf("defaults=%+v", p)
	}
	if got := (llm.RetryPolicy{MaxAttempts: 1}).Resolved().MaxAttempts; got != 1 {
		t.Fatalf("MaxAttempts 1 must stay 1, got %d", got)
	}
}

func TestRetryFinalErrorNamesAttempts(t *testing.T) {
	srv, _ := statusServer(t, scriptedStatus{status: 503, body: `{"error":{"message":"down"}}`})
	_, err := llm.WithRetry(openAIFor(srv), quick).Complete(context.Background(), goldenHistory())
	if err == nil || !strings.Contains(err.Error(), "3 attempts") {
		t.Fatalf("final error should name the attempts: %v", err)
	}
	var httpErr *llm.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 503 {
		t.Fatalf("the provider error must stay reachable: %v", err)
	}
}

func TestRetryAfterHeader(t *testing.T) {
	policy := llm.RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 200 * time.Millisecond}
	t.Run("seconds replace the computed delay", func(t *testing.T) {
		srv, _ := statusServer(t,
			scriptedStatus{status: 429, header: map[string]string{"Retry-After": "0"}, body: `{"error":{"message":"x"}}`},
			scriptedStatus{status: 200, body: okBody})
		rec := &retryRecord{}
		if _, err := llm.WithRetry(openAIFor(srv), policy).Complete(rec.ctx(context.Background()), goldenHistory()); err != nil {
			t.Fatal(err)
		}
		if len(rec.events) != 1 || rec.events[0].Delay != 0 {
			t.Fatalf("Retry-After: 0 should give a zero delay: %+v", rec.events)
		}
	})
	t.Run("a long value is capped by MaxDelay", func(t *testing.T) {
		srv, _ := statusServer(t,
			scriptedStatus{status: 429, header: map[string]string{"Retry-After": "3600"}, body: `{"error":{"message":"x"}}`},
			scriptedStatus{status: 200, body: okBody})
		rec := &retryRecord{}
		start := time.Now()
		if _, err := llm.WithRetry(openAIFor(srv), policy).Complete(rec.ctx(context.Background()), goldenHistory()); err != nil {
			t.Fatal(err)
		}
		if len(rec.events) != 1 || rec.events[0].Delay != 200*time.Millisecond {
			t.Fatalf("delay=%+v want the 200ms cap", rec.events)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("slept past the cap")
		}
	})
	t.Run("an HTTP date is understood", func(t *testing.T) {
		when := time.Now().Add(100 * time.Millisecond).UTC().Format(http.TimeFormat)
		srv, _ := statusServer(t,
			scriptedStatus{status: 429, header: map[string]string{"Retry-After": when}, body: `{"error":{"message":"x"}}`},
			scriptedStatus{status: 200, body: okBody})
		rec := &retryRecord{}
		if _, err := llm.WithRetry(openAIFor(srv), policy).Complete(rec.ctx(context.Background()), goldenHistory()); err != nil {
			t.Fatal(err)
		}
		if len(rec.events) != 1 || rec.events[0].Delay > 200*time.Millisecond {
			t.Fatalf("delay=%+v", rec.events)
		}
	})
	t.Run("an unparseable value falls back to backoff", func(t *testing.T) {
		srv, _ := statusServer(t,
			scriptedStatus{status: 429, header: map[string]string{"Retry-After": "soon"}, body: `{"error":{"message":"x"}}`},
			scriptedStatus{status: 200, body: okBody})
		if _, err := llm.WithRetry(openAIFor(srv), policy).Complete(context.Background(), goldenHistory()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRetryCtxCancel(t *testing.T) {
	srv, _ := statusServer(t, scriptedStatus{status: 503, body: `{"error":{"message":"down"}}`})
	slow := llm.RetryPolicy{MaxAttempts: 5, BaseDelay: 10 * time.Second, MaxDelay: 10 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := llm.WithRetry(openAIFor(srv), slow).Complete(ctx, goldenHistory())
		done <- err
	}()
	time.Sleep(100 * time.Millisecond) // inside the first backoff sleep
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context.Canceled", err)
		}
		if time.Since(start) > 500*time.Millisecond {
			t.Fatalf("returned %v after cancel", time.Since(start))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a cancelled context did not stop the retry sleep")
	}
}

func TestRetryDoesNotStartWhenCancelled(t *testing.T) {
	srv, n := statusServer(t, scriptedStatus{status: 200, body: okBody})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := llm.WithRetry(openAIFor(srv), quick).Complete(ctx, goldenHistory()); err == nil {
		t.Fatal("expected an error")
	}
	if n.Load() != 0 {
		t.Fatalf("sent %d requests on a cancelled context", n.Load())
	}
}

// streamServer fails (or not) according to the script: each entry is a full raw response.
func streamServer(t *testing.T, bodies ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		body := bodies[i]
		if strings.HasPrefix(body, "STATUS:") {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"message":"busy"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestRetryStreamBeforeAnyDeltaIsRetried(t *testing.T) {
	srv, n := streamServer(t, "STATUS:503",
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	var got strings.Builder
	resp, err := llm.WithRetry(c, quick).(llm.StreamCompleter).CompleteStream(context.Background(), goldenHistory(),
		func(d llm.Delta) { got.WriteString(d.Text) })
	if err != nil || n.Load() != 2 || got.String() != "hi" || resp.Message.Content != "hi" {
		t.Fatalf("err=%v requests=%d text=%q", err, n.Load(), got.String())
	}
}

// A failure after the user has seen text must not be replayed: the answer would appear twice.
func TestRetryMidStreamNoRetry(t *testing.T) {
	// One delta, then the connection dies with a malformed frame the adapter treats as an error.
	srv, n := streamServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {bad json\n\n")
	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	var seen strings.Builder
	_, err := llm.WithRetry(c, quick).(llm.StreamCompleter).CompleteStream(context.Background(), goldenHistory(),
		func(d llm.Delta) { seen.WriteString(d.Text) })
	if err == nil {
		t.Fatal("expected the mid-stream failure to surface")
	}
	if n.Load() != 1 {
		t.Fatalf("requests=%d: a stream that already showed text was retried", n.Load())
	}
	if seen.String() != "partial" {
		t.Fatalf("seen=%q", seen.String())
	}
	if !llm.IsMidStream(err) {
		t.Fatalf("the error must be marked mid-stream so the run can report provider_mid_stream: %v", err)
	}
}

func TestRetryWrapperKeepsStreaming(t *testing.T) {
	if _, ok := llm.WithRetry(llm.NewOpenAI(llm.Config{}), quick).(llm.StreamCompleter); !ok {
		t.Fatal("a streaming completer lost streaming through the wrapper")
	}
	plain := plainCompleter{}
	if _, ok := llm.WithRetry(plain, quick).(llm.StreamCompleter); ok {
		t.Fatal("the wrapper claims streaming its inner completer does not have")
	}
}

type plainCompleter struct{}

func (plainCompleter) Complete(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{}, nil
}

func TestRetryJitterStaysWithinBounds(t *testing.T) {
	p := llm.RetryPolicy{MaxAttempts: 6, BaseDelay: 100 * time.Millisecond, MaxDelay: 800 * time.Millisecond}
	for attempt := 1; attempt <= 5; attempt++ {
		ceiling := p.BaseDelay << (attempt - 1)
		if ceiling > p.MaxDelay {
			ceiling = p.MaxDelay
		}
		for i := 0; i < 200; i++ {
			d := llm.BackoffDelay(p, attempt)
			if d < 0 || d > ceiling {
				t.Fatalf("attempt %d: delay %v outside [0,%v]", attempt, d, ceiling)
			}
		}
	}
}
