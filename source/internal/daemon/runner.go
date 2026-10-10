// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/worker"
)

// JobRunner executes an assigned job and streams events.
type JobRunner interface {
	Run(ctx context.Context, job JobAssignBody, cwd string, emit func(JobEventBody)) JobDoneBody
}

// EmbeddedRunner runs Anvil in-process (default when WorkerBin is empty).
type EmbeddedRunner struct {
	PluginDirs []string
	Deny       []string

	mu  sync.RWMutex
	LLM LLMConfig
}

// SetLLM replaces worker LLM defaults (scheduler provision / live config).
func (r *EmbeddedRunner) SetLLM(llm LLMConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.LLM = llm
	r.mu.Unlock()
}

func (r *EmbeddedRunner) llmSnapshot() LLMConfig {
	if r == nil {
		return LLMConfig{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.LLM
}

// Run implements JobRunner.
func (r *EmbeddedRunner) Run(ctx context.Context, job JobAssignBody, cwd string, emit func(JobEventBody)) JobDoneBody {
	if r == nil {
		return JobDoneBody{JobID: job.JobID, Status: "error", Error: "nil runner"}
	}
	if job.ForceStub {
		return runStub(ctx, job, emit)
	}
	opts := worker.OptionsFromEnv()
	applyLLMDefaults(&opts, r.llmSnapshot())
	opts.Local.CWD = cwd
	if job.MaxSteps > 0 {
		opts.MaxSteps = job.MaxSteps
	}
	if job.Model != "" {
		opts.Model.ID = job.Model
	}
	if job.Provider != "" {
		opts.Provider = anvil.Provider(job.Provider)
	}
	opts.Plugins = append([]string(nil), job.Plugins...)
	opts.PluginDirs = append([]string(nil), r.PluginDirs...)
	// Daemon already intersected allowlist; pin Available to resolved set so Init cannot expand.
	opts.PluginPolicy = &anvil.PluginPolicy{
		Available: append([]string(nil), job.Plugins...),
		Deny:      append([]string(nil), r.Deny...),
	}
	if len(job.Plugins) == 0 {
		opts.PluginPolicy = &anvil.PluginPolicy{Available: []string{}, Deny: append([]string(nil), r.Deny...)}
	}
	done := worker.RunJob(ctx, opts, worker.Job{
		JobID:    job.JobID,
		Prompt:   job.Prompt,
		ResumeID: job.ResumeID,
	}, func(ev worker.Event) {
		emit(JobEventBody{
			JobID:      job.JobID,
			Type:       string(ev.Type),
			Text:       ev.Text,
			ToolCallID: ev.ToolCallID,
			Name:       ev.Name,
			Args:       ev.Args,
			Content:    ev.Content,
			IsError:    ev.IsError,
			Step:       ev.Step,
			MaxSteps:   ev.MaxSteps,
			Code:       string(ev.Code),
		})
	})
	out := JobDoneBody{
		JobID:  done.JobID,
		Status: done.Status,
		Result: done.Result,
		Error:  done.Error,
		Code:   done.Code,
		Steps:  done.Steps,
	}
	if done.Usage != nil {
		out.Usage = &TokenUsage{
			InputTokens:  done.Usage.InputTokens,
			OutputTokens: done.Usage.OutputTokens,
			TotalTokens:  done.Usage.TotalTokens,
		}
	}
	return out
}

// applyLLMDefaults fills Options from agentd llm config when env left them empty.
// Provisioned APIKey (scheduler) wins over env when set.
func applyLLMDefaults(opts *anvil.Options, llm LLMConfig) {
	if opts == nil {
		return
	}
	if opts.BaseURL == "" && llm.BaseURL != "" {
		opts.BaseURL = llm.BaseURL
	}
	if llm.APIKey != "" {
		opts.APIKey = llm.APIKey
	} else if opts.APIKey == "" {
		keyEnv := llm.APIKeyEnv
		if keyEnv == "" {
			keyEnv = "ANVIL_API_KEY"
		}
		opts.APIKey = os.Getenv(keyEnv)
	}
	if opts.Model.ID == "" && llm.Model != "" {
		opts.Model.ID = llm.Model
	}
	if llm.Provider != "" && os.Getenv("ANVIL_PROVIDER") == "" {
		opts.Provider = anvil.Provider(llm.Provider)
	}
	if opts.MaxSteps == 0 && llm.MaxSteps > 0 {
		opts.MaxSteps = llm.MaxSteps
	}
}

func runStub(ctx context.Context, job JobAssignBody, emit func(JobEventBody)) JobDoneBody {
	emit(JobEventBody{JobID: job.JobID, Type: string(anvil.EventAssistant), Text: "stub:" + job.Prompt})
	select {
	case <-ctx.Done():
		return JobDoneBody{JobID: job.JobID, Status: string(anvil.StatusCancelled), Code: string(anvil.CodeCancelled), Error: context.Cause(ctx).Error()}
	case <-time.After(10 * time.Millisecond):
	}
	emit(JobEventBody{JobID: job.JobID, Type: string(anvil.EventDone), Text: "stub:" + job.Prompt})
	return JobDoneBody{
		JobID: job.JobID, Status: string(anvil.StatusFinished), Result: "stub:" + job.Prompt, Steps: 1,
		Usage: &TokenUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	}
}

// EnvPassthrough lists LLM-related env names workers inherit.
func EnvPassthrough() []string {
	return []string{
		"ANVIL_API_KEY", "ANVIL_BASE_URL", "ANVIL_MODEL", "ANVIL_PROVIDER",
		"ANVIL_MAX_STEPS", "ANVIL_MAX_WALL",
	}
}

// LookPathWorker resolves WorkerBin or empty for embedded.
func LookPathWorker(bin string) (string, error) {
	if bin == "" {
		return "", nil
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("worker_bin: %w", err)
	}
	return bin, nil
}
