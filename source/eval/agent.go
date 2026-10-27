//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"fmt"
	"math"
	"time"

	anvil "github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/runtime"
)

// ExecuteAgent exercises Create, Send, persistence and child execution in an
// isolated workspace. Callers choose the upstream: replay, synthetic or live.
// The caller supplies a fresh directory; the function does not remove it.
func ExecuteAgent(ctx context.Context, dir string, task Task, upstream llm.Completer, opts anvil.Options) (Outcome, error) {
	if err := task.validate(); err != nil {
		return Outcome{}, err
	}
	if upstream == nil {
		return Outcome{}, fmt.Errorf("eval completer is nil")
	}
	if task.Setup != nil {
		if err := task.Setup(dir); err != nil {
			return Outcome{}, fmt.Errorf("setup: %w", err)
		}
	}
	allowShell := false
	opts.Local = anvil.LocalOptions{CWD: dir, Store: "jsonl", AllowShell: &allowShell}
	if opts.Model.ID == "" {
		opts.Model.ID = "eval"
	}
	if opts.APIKey == "" {
		opts.APIKey = "eval-offline-unused-key"
	}
	opts.MaxSteps = task.maxSteps()
	if opts.MaxWallTime <= 0 {
		opts.MaxWallTime = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opts.MaxWallTime+5*time.Second)
	defer cancel()
	score := &scorer{}
	childCallback := opts.Subagents.OnEvent
	opts.Subagents.OnEvent = func(ev anvil.SubagentEvent) {
		score.noteAgent(ev.AgentID)
		score.observe(runtimeEvent(ev.Event))
		if ev.Event.Type == anvil.EventError && string(ev.Event.Code) == runtime.CodeLoopDetected {
			score.mu.Lock()
			score.childLoopStops++
			score.mu.Unlock()
		}
		if childCallback != nil {
			childCallback(ev)
		}
	}
	meter := &measuredCompleter{upstream: upstream}
	agent, err := anvil.CreateForEval(ctx, opts, meter)
	if err != nil {
		return Outcome{}, err
	}
	defer agent.Close()
	run, err := agent.Send(ctx, task.UserMsg)
	if err != nil {
		return Outcome{}, err
	}
	rootToolResults := 0
	stream := run.Stream(ctx)
streamLoop:
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				break streamLoop
			}
			if ev.Type == anvil.EventToolResult {
				rootToolResults++
			}
			score.observe(runtimeEvent(ev))
		case <-ctx.Done():
			_ = run.Cancel(context.Background())
			return Outcome{}, ctx.Err()
		}
	}
	publicResult, waitErr := run.Wait(ctx)
	if publicResult == nil {
		return Outcome{}, fmt.Errorf("Agent.Wait returned no result: %v", waitErr)
	}
	result := runtime.Result{ID: publicResult.ID, Status: string(publicResult.Status), Result: publicResult.Result,
		Steps: publicResult.Steps, Err: publicResult.Err, Code: string(publicResult.Code)}
	transcript, transcriptErr := anvil.LoadTranscript(ctx, agent.AgentID(), anvil.TranscriptOptions{
		Local: opts.Local, Limit: math.MaxInt,
	})
	if transcriptErr != nil {
		return Outcome{}, fmt.Errorf("verify event coverage: %w", transcriptErr)
	}
	storedToolResults := 0
	for _, entry := range transcript.Entries {
		if entry.Role == "" {
			continue
		}
		message := llm.Message{Role: llm.Role(entry.Role), Content: entry.Content, Name: entry.Name, ToolCallID: entry.ToolCallID}
		for _, call := range entry.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, llm.ToolCall{ID: call.ID, Name: call.Name, Args: call.Args})
		}
		result.Messages = append(result.Messages, message)
		if entry.Role == "tool" && entry.RunID == run.ID() {
			storedToolResults++
		}
	}
	bytes, harnessErr := meter.totals()
	if harnessErr == nil {
		if verify, ok := upstream.(interface{ Verify() error }); ok {
			harnessErr = verify.Verify()
		}
	}
	if harnessErr == nil && rootToolResults != storedToolResults {
		harnessErr = fmt.Errorf("incomplete event stream: received %d tool results, transcript has %d", rootToolResults, storedToolResults)
	}
	if harnessErr == nil && waitErr != nil {
		// Wait also returns the runtime failure as an error. A task may expect
		// that outcome (loop guard/max steps), but never a separate store error.
		if task.Check == nil || result.Status != "error" || waitErr.Error() != result.Err {
			harnessErr = waitErr
		}
	}
	if harnessErr == nil {
		harnessErr = checkTask(dir, task, result)
	}
	return Outcome{Result: result, Score: score.finish(result, harnessErr == nil, bytes), AgentID: agent.AgentID()}, harnessErr
}

// RunRecording is tier 2's default entry point. It never reaches a provider.
func RunRecording(ctx context.Context, dir string, task Task, opts anvil.Options) (Outcome, error) {
	if task.Recording == "" {
		return Outcome{}, fmt.Errorf("task %s has no recording", task.Name)
	}
	replay, err := LoadReplay(task.Recording)
	if err != nil {
		return Outcome{}, err
	}
	return ExecuteAgent(ctx, dir, task, replay, opts)
}

func runtimeEvent(ev anvil.Event) runtime.Event {
	out := runtime.Event{Type: string(ev.Type), Name: ev.Name, Content: ev.Content, IsError: ev.IsError, Code: string(ev.Code)}
	if ev.Rule != nil {
		switch ev.Rule.Effect {
		case "deny":
			out.Rule = &permissions.Decision{Matched: true, Effect: permissions.Deny}
		case "ask":
			out.Rule = &permissions.Decision{Matched: true, Effect: permissions.Ask}
		case "allow":
			out.Rule = &permissions.Decision{Matched: true, Effect: permissions.Allow}
		}
	}
	return out
}
