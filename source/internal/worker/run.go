// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package worker runs Anvil Local jobs for agent-worker / embedded agentd.
package worker

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/anvil-dev/anvil"
)

// Job is one assignable unit.
type Job struct {
	JobID    string
	Prompt   string
	ResumeID string
}

// Event mirrors anvil.Event fields for streaming to the daemon.
type Event struct {
	Type       anvil.EventType
	Text       string
	ToolCallID string
	Name       string
	Args       json.RawMessage
	Content    string
	IsError    bool
	Step       int
	MaxSteps   int
	Code       anvil.Code
}

// Done is the terminal job result.
type Done struct {
	JobID   string
	AgentID string
	Status  string
	Result  string
	Error   string
	Code    string
	Steps   int
	Usage   *anvil.Usage
}

// OptionsFromEnv builds Create options from ANVIL_* environment variables.
func OptionsFromEnv() anvil.Options {
	opts := anvil.Options{
		APIKey:   os.Getenv("ANVIL_API_KEY"),
		BaseURL:  os.Getenv("ANVIL_BASE_URL"),
		Provider: anvil.ProviderOpenAI,
		Model:    anvil.Model{ID: os.Getenv("ANVIL_MODEL")},
		Local:    anvil.LocalOptions{},
	}
	switch os.Getenv("ANVIL_PROVIDER") {
	case "anthropic":
		opts.Provider = anvil.ProviderAnthropic
	case "openai", "":
		opts.Provider = anvil.ProviderOpenAI
	default:
		opts.Provider = anvil.Provider(os.Getenv("ANVIL_PROVIDER"))
	}
	if v := os.Getenv("ANVIL_MAX_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.MaxSteps = n
		}
	}
	if v := os.Getenv("ANVIL_MAX_WALL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			opts.MaxWallTime = d
		}
	}
	return opts
}

// RunJob creates or resumes an agent, sends the prompt, streams events, waits.
func RunJob(ctx context.Context, opts anvil.Options, job Job, emit func(Event)) Done {
	var (
		agent *anvil.Agent
		err   error
	)
	if job.ResumeID != "" {
		agent, err = anvil.Resume(ctx, job.ResumeID, opts)
	} else {
		agent, err = anvil.Create(ctx, opts)
	}
	if err != nil {
		emit(Event{Type: anvil.EventError, Text: err.Error(), IsError: true})
		return Done{JobID: job.JobID, Status: string(anvil.StatusError), Error: err.Error()}
	}
	agentID := agent.AgentID()
	defer func() { _ = agent.Close() }()

	run, err := agent.Send(ctx, job.Prompt)
	if err != nil {
		emit(Event{Type: anvil.EventError, Text: err.Error(), IsError: true})
		return Done{JobID: job.JobID, AgentID: agentID, Status: string(anvil.StatusError), Error: err.Error()}
	}

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		for ev := range run.Stream(ctx) {
			emit(Event{
				Type:       ev.Type,
				Text:       ev.Text,
				ToolCallID: ev.ToolCallID,
				Name:       ev.Name,
				Args:       ev.Args,
				Content:    ev.Content,
				IsError:    ev.IsError,
				Step:       ev.Step,
				MaxSteps:   ev.MaxSteps,
				Code:       ev.Code,
			})
		}
	}()

	go func() {
		<-ctx.Done()
		_ = run.Cancel(context.Background())
	}()

	res, err := run.Wait(ctx)
	<-streamDone
	if res == nil {
		msg := "wait failed"
		if err != nil {
			msg = err.Error()
		}
		return Done{JobID: job.JobID, AgentID: agentID, Status: string(anvil.StatusError), Error: msg}
	}
	return Done{
		JobID:   job.JobID,
		AgentID: agentID,
		Status:  string(res.Status),
		Result:  res.Result,
		Error:   res.Err,
		Code:    string(res.Code),
		Steps:   res.Steps,
		Usage:   res.Usage,
	}
}
