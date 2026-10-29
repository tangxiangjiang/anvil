// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

type stopCompleter struct {
	calls atomic.Int32
	reply func(n int32) (*llm.Response, error)
}

func (s *stopCompleter) Complete(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return s.reply(s.calls.Add(1))
}

func runStop(t *testing.T, c llm.Completer) (runtime.Result, []runtime.Event) {
	t.Helper()
	var events []runtime.Event
	res := runtime.Loop(context.Background(), c, tools.NewRegistry(t.TempDir(), false), runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "write a long answer", MaxSteps: 6,
	}, func(ev runtime.Event) { events = append(events, ev) })
	return res, events
}

// A cut answer finishes the run and says so. Nothing is re-requested: an automatic
// continuation is a second completion nobody budgeted for.
func TestTruncatedOnLengthStop(t *testing.T) {
	c := &stopCompleter{reply: func(int32) (*llm.Response, error) {
		return &llm.Response{
			Message:    llm.Message{Role: llm.RoleAssistant, Content: "the answer was cut o"},
			StopReason: llm.StopLength,
		}, nil
	}}
	res, events := runStop(t, c)
	if res.Status != "finished" || !res.Truncated {
		t.Fatalf("status=%s truncated=%v", res.Status, res.Truncated)
	}
	if n := c.calls.Load(); n != 1 {
		t.Fatalf("model requests=%d, want exactly 1 (no continuation)", n)
	}
	count := 0
	for _, ev := range events {
		if ev.Type == "truncated" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("truncated events=%d, want 1", count)
	}
	if res.Result != "the answer was cut o" {
		t.Fatalf("the partial answer must be kept: %q", res.Result)
	}
}

func TestNotTruncatedWhenTheAnswerEnded(t *testing.T) {
	for _, reason := range []llm.StopReason{llm.StopEnd, llm.StopOther, ""} {
		c := &stopCompleter{reply: func(int32) (*llm.Response, error) {
			return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}, StopReason: reason}, nil
		}}
		res, events := runStop(t, c)
		if res.Truncated {
			t.Fatalf("reason %q marked truncated", reason)
		}
		for _, ev := range events {
			if ev.Type == "truncated" {
				t.Fatalf("reason %q emitted a truncated event", reason)
			}
		}
	}
}

// Truncated is about the answer the run ended with. A length stop on a round that went
// on to call a tool is not the end of the run, and the later answer is what counts.
func TestTruncatedOnlyDescribesTheFinalAnswer(t *testing.T) {
	c := &stopCompleter{reply: func(n int32) (*llm.Response, error) {
		if n == 1 {
			return &llm.Response{
				Message:    llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "list_dir", Args: []byte(`{"path":"."}`)}}},
				StopReason: llm.StopLength,
			}, nil
		}
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}, StopReason: llm.StopEnd}, nil
	}}
	res, _ := runStop(t, c)
	if res.Truncated || res.Status != "finished" {
		t.Fatalf("status=%s truncated=%v", res.Status, res.Truncated)
	}
}

// After retries are spent, the provider's failure kind becomes the run's code, with the
// provider's own text kept in the error.
func TestProviderCodes(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{429, runtime.CodeProviderBusy},
		{400, runtime.CodeRequestRejected},
		{401, runtime.CodeAuth},
		{403, runtime.CodeAuth},
		{500, runtime.CodeProvider},
		{0, runtime.CodeProvider},
	}
	for _, tc := range cases {
		c := &stopCompleter{reply: func(int32) (*llm.Response, error) {
			return nil, llm.NewHTTPError(tc.status, "provider error: detail from the provider")
		}}
		res, events := runStop(t, c)
		if res.Status != "error" || res.Code != tc.want {
			t.Fatalf("status %d: code=%q want %q", tc.status, res.Code, tc.want)
		}
		if res.Err == "" || !contains(res.Err, "detail from the provider") {
			t.Fatalf("status %d: provider text lost: %q", tc.status, res.Err)
		}
		var last runtime.Event
		for _, ev := range events {
			if ev.Type == "error" {
				last = ev
			}
		}
		if last.Code != tc.want {
			t.Fatalf("status %d: error event code=%q want %q", tc.status, last.Code, tc.want)
		}
	}
}

func TestProviderCodeForAnUnclassifiedError(t *testing.T) {
	c := &stopCompleter{reply: func(int32) (*llm.Response, error) { return nil, errors.New("something odd") }}
	res, _ := runStop(t, c)
	if res.Code != runtime.CodeProvider {
		t.Fatalf("code=%q, want the generic provider code", res.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
