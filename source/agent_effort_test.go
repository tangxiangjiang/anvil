// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestCreateRefusesUnknownEffort(t *testing.T) {
	for _, effort := range []Effort{"max", "HIGH", "1024", " low"} {
		_, err := Create(context.Background(), Options{
			APIKey: "k", Model: Model{ID: "m"}, Effort: effort, Local: LocalOptions{CWD: t.TempDir()},
		})
		if err == nil || !strings.Contains(err.Error(), "unsupported effort") {
			t.Fatalf("effort %q: err=%v", effort, err)
		}
	}
	for _, effort := range []Effort{EffortDefault, EffortLow, EffortMedium, EffortHigh} {
		a, err := Create(context.Background(), Options{
			APIKey: "k", Model: Model{ID: "m"}, Effort: effort, Local: LocalOptions{CWD: t.TempDir()},
		})
		if err != nil {
			t.Fatalf("effort %q refused: %v", effort, err)
		}
		a.Close()
	}
}

// Effort configured on the agent reaches every request of the run, including a
// child's, because a child inherits its parent's unless told otherwise.
func TestEffortReachesRequestsAndChildren(t *testing.T) {
	var mu sync.Mutex
	var seen []llm.Effort
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Effort: EffortMedium,
		Local:     LocalOptions{CWD: t.TempDir()},
		Subagents: SubagentOptions{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		mu.Lock()
		seen = append(seen, req.Effort)
		mu.Unlock()
		switch len(trailingToolResults(req)) {
		case 0:
			if strings.Contains(req.Messages[len(req.Messages)-1].Content, "child work") {
				return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "child done"}}, nil
			}
			return callResponse("c1", "spawn_agent", `{"prompt":"child work","description":"child"}`), nil
		default:
			return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
		}
	})
	run, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 3 {
		t.Fatalf("expected parent, child and parent requests, got %d", len(seen))
	}
	for i, effort := range seen {
		if effort != llm.EffortMedium {
			t.Fatalf("request %d carried effort %q, want medium (a child inherits its parent's)", i, effort)
		}
	}
}

// The default is empty, so a session that says nothing sends nothing.
func TestEmptyEffortReachesRequestsEmpty(t *testing.T) {
	a, err := Create(context.Background(), Options{APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var got llm.Effort = "unset"
	a.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		got = req.Effort
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}, nil
	})
	run, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != llm.EffortDefault {
		t.Fatalf("effort=%q, want empty", got)
	}
}

// MaxOutputTokens and a cut answer reach the caller through the public surface: the
// option is sent on every request, a child inherits it, and a length stop ends the run
// finished with Truncated set, one truncated event, and no second request.
func TestMaxOutputTokensAndTruncationReachTheCaller(t *testing.T) {
	var mu sync.Mutex
	var limits []int
	requests := 0
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, MaxOutputTokens: 1234, Local: LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		limits = append(limits, req.MaxOutputTokens)
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "partial"}, StopReason: llm.StopLength}, nil
	})
	run, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	var types []EventType
	for ev := range run.Stream(context.Background()) {
		types = append(types, ev.Type)
	}
	res, err := run.Wait(context.Background())
	if err != nil || res.Status != StatusFinished || !res.Truncated || res.Result != "partial" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 || len(limits) != 1 || limits[0] != 1234 {
		t.Fatalf("requests=%d limits=%v", requests, limits)
	}
	truncated, done := -1, -1
	for i, typ := range types {
		switch typ {
		case EventTruncated:
			truncated = i
		case EventDone:
			done = i
		}
	}
	if truncated < 0 || done < 0 || truncated > done {
		t.Fatalf("want a truncated event before done, got %v", types)
	}
}

func TestProviderCodesReachRunResult(t *testing.T) {
	cases := map[int]Code{429: CodeProviderBusy, 400: CodeRequestRejected, 401: CodeAuth, 500: CodeProvider}
	for status, want := range cases {
		a, err := Create(context.Background(), Options{
			APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: t.TempDir()},
		})
		if err != nil {
			t.Fatal(err)
		}
		a.complete = completerFunc(func(context.Context, llm.Request) (*llm.Response, error) {
			return nil, llm.NewHTTPError(status, "provider error: unknown parameter reasoning_effort")
		})
		run, _ := a.Send(context.Background(), "go")
		res, _ := run.Wait(context.Background())
		a.Close()
		if res.Status != StatusError || res.Code != want || !strings.Contains(res.Err, "reasoning_effort") {
			t.Fatalf("status %d: res=%+v, want code %q with the provider text", status, res, want)
		}
	}
}
