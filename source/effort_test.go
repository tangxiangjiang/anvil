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
