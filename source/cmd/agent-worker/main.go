// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Command agent-worker runs a single Anvil Local job (usually under agentd).
// MVP 0.11: agentd embeds the runner by default; this binary supports
// --job JSON for isolated process experiments and smoke.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/worker"
)

type jobFile struct {
	JobID    string `json:"job_id"`
	CWD      string `json:"cwd"`
	Prompt   string `json:"prompt"`
	ResumeID string `json:"resume_id,omitempty"`
	MaxSteps int    `json:"max_steps,omitempty"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func main() {
	var (
		jobPath = flag.String("job", "", "path to job JSON (default: stdin)")
		showVer = flag.Bool("version", false, "print version")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `agent-worker — run one Anvil Local job

Usage:
  agent-worker --job job.json
  echo '{"job_id":"j1","cwd":"/repo","prompt":"hi"}' | agent-worker

Env: ANVIL_API_KEY, ANVIL_BASE_URL, ANVIL_MODEL, ANVIL_PROVIDER, …

Events are written as JSON lines to stdout; a final {"type":"job_done",...} line ends the stream.

`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println(version())
		return
	}

	var (
		raw []byte
		err error
	)
	if *jobPath != "" {
		raw, err = os.ReadFile(*jobPath)
	} else {
		raw, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var jf jobFile
	if err := json.Unmarshal(raw, &jf); err != nil {
		fmt.Fprintln(os.Stderr, "parse job:", err)
		os.Exit(1)
	}
	if jf.JobID == "" || jf.CWD == "" || jf.Prompt == "" {
		fmt.Fprintln(os.Stderr, "job_id, cwd, and prompt are required")
		os.Exit(2)
	}

	opts := worker.OptionsFromEnv()
	opts.Local.CWD = jf.CWD
	if jf.MaxSteps > 0 {
		opts.MaxSteps = jf.MaxSteps
	}
	if jf.Model != "" {
		opts.Model.ID = jf.Model
	}
	if jf.Provider != "" {
		opts.Provider = anvil.Provider(jf.Provider)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	enc := json.NewEncoder(os.Stdout)
	done := worker.RunJob(ctx, opts, worker.Job{
		JobID:    jf.JobID,
		Prompt:   jf.Prompt,
		ResumeID: jf.ResumeID,
	}, func(ev worker.Event) {
		_ = enc.Encode(map[string]any{
			"type":         string(ev.Type),
			"text":         ev.Text,
			"tool_call_id": ev.ToolCallID,
			"name":         ev.Name,
			"args":         ev.Args,
			"content":      ev.Content,
			"is_error":     ev.IsError,
			"step":         ev.Step,
			"max_steps":    ev.MaxSteps,
			"code":         string(ev.Code),
		})
	})
	_ = enc.Encode(map[string]any{
		"type":   "job_done",
		"job_id": done.JobID,
		"status": done.Status,
		"result": done.Result,
		"error":  done.Error,
		"code":   done.Code,
		"steps":  done.Steps,
	})
	if done.Status == "error" {
		os.Exit(2)
	}
}

func version() string {
	if i, ok := debug.ReadBuildInfo(); ok && i.Main.Version != "" && i.Main.Version != "(devel)" {
		return i.Main.Version
	}
	return "0.11.0-dev"
}
