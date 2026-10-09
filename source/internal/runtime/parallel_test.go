package runtime_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

type slowTool struct {
	name string
	live *atomic.Int32
	max  *atomic.Int32
}

func (t *slowTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        t.name,
		Description: "slow",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (t *slowTool) Execute(ctx context.Context, args json.RawMessage) tools.Result {
	_ = ctx
	_ = args
	n := t.live.Add(1)
	for {
		cur := t.max.Load()
		if n <= cur || t.max.CompareAndSwap(cur, n) {
			break
		}
	}
	time.Sleep(80 * time.Millisecond)
	t.live.Add(-1)
	return tools.Result{Content: "ok-" + t.name}
}

type twoToolCompleter struct{ n int }

func (c *twoToolCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	if c.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "1", Name: "slow_a", Args: json.RawMessage(`{}`)},
				{ID: "2", Name: "slow_b", Args: json.RawMessage(`{}`)},
			},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
}

func TestParallelToolsOverlap(t *testing.T) {
	var live, max atomic.Int32
	reg := tools.NewRegistry(t.TempDir(), false)
	_ = reg.Register(&slowTool{name: "slow_a", live: &live, max: &max})
	_ = reg.Register(&slowTool{name: "slow_b", live: &live, max: &max})

	start := time.Now()
	res := runtime.Loop(context.Background(), &twoToolCompleter{}, reg, runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "go", MaxSteps: 4,
		ParallelTools: true,
	}, nil)
	elapsed := time.Since(start)
	if res.Status != "finished" {
		t.Fatalf("%+v", res)
	}
	if max.Load() < 2 {
		t.Fatalf("expected overlapping tools, maxConcurrent=%d", max.Load())
	}
	if elapsed > 140*time.Millisecond {
		t.Fatalf("expected parallel wall time, took %s", elapsed)
	}
}

func TestParallelToolsDefaultSequential(t *testing.T) {
	var live, max atomic.Int32
	reg := tools.NewRegistry(t.TempDir(), false)
	_ = reg.Register(&slowTool{name: "slow_a", live: &live, max: &max})
	_ = reg.Register(&slowTool{name: "slow_b", live: &live, max: &max})

	res := runtime.Loop(context.Background(), &twoToolCompleter{}, reg, runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "go", MaxSteps: 4,
		// ParallelTools false
	}, nil)
	if res.Status != "finished" {
		t.Fatalf("%+v", res)
	}
	if max.Load() != 1 {
		t.Fatalf("sequential should never overlap, maxConcurrent=%d", max.Load())
	}
}

func TestParallelToolResultOrder(t *testing.T) {
	var live, max atomic.Int32
	reg := tools.NewRegistry(t.TempDir(), false)
	_ = reg.Register(&slowTool{name: "slow_a", live: &live, max: &max})
	_ = reg.Register(&slowTool{name: "slow_b", live: &live, max: &max})

	var results []string
	runtime.Loop(context.Background(), &twoToolCompleter{}, reg, runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "go", MaxSteps: 4,
		ParallelTools: true,
	}, func(ev runtime.Event) {
		if ev.Type == "tool_result" {
			results = append(results, ev.Name)
		}
	})
	if len(results) != 2 || results[0] != "slow_a" || results[1] != "slow_b" {
		t.Fatalf("want start-order results, got %v", results)
	}
}
