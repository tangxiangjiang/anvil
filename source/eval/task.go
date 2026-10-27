// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package eval provides scripted, recorded and opt-in live agent evaluations.
package eval

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

// Task describes the same workspace and check independently of the tier.
// Script and Recording are mutually exclusive. Live selects an upstream at run
// time, rather than changing the task's expectations.
type Task struct {
	Name      string
	UserMsg   string
	Setup     func(dir string) error
	Script    []Reply
	Recording string
	MaxSteps  int // 0 = 8 for normal fixtures
	Check     func(dir string, res runtime.Result) error
	// ExpectProviderError, when set, names the one upstream failure this task is about:
	// the scripted provider returns it, the run is expected to end on it, and Check
	// judges the result. Any other upstream failure is still a harness error, so a
	// swallowed auxiliary-call error cannot hide behind this field.
	ExpectProviderError func(error) bool
}

type Outcome struct {
	Result runtime.Result
	Score  Score
	// AgentID is set by the public Agent runner, so a task can look at what the
	// run left behind (for example its checkpoint) after the agent is closed.
	AgentID string
}

func (task Task) maxSteps() int {
	if task.MaxSteps <= 0 {
		return 8
	}
	return task.MaxSteps
}

func (task Task) validate() error {
	if task.Name == "" {
		return fmt.Errorf("task has no name")
	}
	if task.Recording != "" && len(task.Script) != 0 {
		return fmt.Errorf("task %s sets both Script and Recording", task.Name)
	}
	return nil
}

// ExecuteScript runs tier 1 against the tool loop, keeping its original path.
func ExecuteScript(ctx context.Context, dir string, task Task) (Outcome, error) {
	if err := task.validate(); err != nil {
		return Outcome{}, err
	}
	if task.Recording != "" {
		return Outcome{}, fmt.Errorf("recorded tasks require the anvileval Agent runner")
	}
	if task.Setup != nil {
		if err := task.Setup(dir); err != nil {
			return Outcome{}, fmt.Errorf("setup: %w", err)
		}
	}
	script := NewScript(task.Script)
	// The wrapper the SDK puts around a real client, on a fast clock, so a scripted
	// 429 is retried as a real one is and shows up as a retry event.
	retrying := llm.WithRetry(script, llm.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond})
	meter := &measuredCompleter{upstream: retrying}
	score := &scorer{}
	result := runtime.Loop(ctx, meter, tools.NewRegistry(dir, false), runtime.Config{
		CWD: dir, Model: "eval", UserMsg: task.UserMsg, MaxSteps: task.maxSteps(),
	}, score.observe)
	bytes, err := meter.totals()
	if err != nil && task.ExpectProviderError != nil && task.ExpectProviderError(err) {
		err = nil
	}
	if err == nil {
		err = script.Err()
	}
	if err == nil {
		err = checkTask(dir, task, result)
	}
	out := Outcome{Result: result, Score: score.finish(result, err == nil, bytes)}
	return out, err
}

func checkTask(dir string, task Task, result runtime.Result) error {
	if task.Check != nil {
		return task.Check(dir, result)
	}
	if result.Status != "finished" {
		return fmt.Errorf("status=%s code=%s error=%s", result.Status, result.Code, result.Err)
	}
	return nil
}

// Run is the test-facing scripted runner; each task prints one score.
func Run(t *testing.T, task Task) Score {
	t.Helper()
	out, err := ExecuteScript(t.Context(), t.TempDir(), task)
	t.Logf("%s score=%+v", task.Name, out.Score)
	if err != nil {
		t.Fatalf("%s: %v\nresult=%+v", task.Name, err, out.Result)
	}
	return out.Score
}

// RequestPredicate pins the existence of a tool definition without depending on
// the map iteration order of the registry.
func RequestPredicate(tool string) func(llm.Request) error {
	return func(req llm.Request) error {
		for _, definition := range req.Tools {
			if definition.Name == tool {
				return nil
			}
		}
		return fmt.Errorf("request has no %s tool", tool)
	}
}
