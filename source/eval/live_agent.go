//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"fmt"
	"os"
	"time"

	anvil "github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/tools"
)

// RunLive is explicitly opt-in. It records a configured real provider, but its
// result is a report, not a baseline gate. No shell, verify command, executable
// hook, MCP server or other network tool is enabled. Tasks must use controlled
// fixture setup rather than an existing repository containing private data.
func RunLive(ctx context.Context, dir string, task Task, path string, settings anvil.Options) (Outcome, error) {
	if os.Getenv("ANVIL_EVAL_LIVE") != "1" {
		return Outcome{}, fmt.Errorf("live eval requires ANVIL_EVAL_LIVE=1")
	}
	model := os.Getenv("ANVIL_MODEL")
	key := os.Getenv("ANVIL_API_KEY")
	if model == "" || key == "" {
		return Outcome{}, fmt.Errorf("live eval requires ANVIL_MODEL and ANVIL_API_KEY")
	}
	provider := os.Getenv("ANVIL_PROVIDER")
	if provider == "" {
		provider = "openai"
	}
	config := llm.Config{APIKey: key, Model: model, BaseURL: os.Getenv("ANVIL_BASE_URL")}
	var upstream llm.Completer
	switch provider {
	case "openai":
		upstream = llm.NewOpenAI(config)
	case "anthropic":
		upstream = llm.NewAnthropic(config)
	default:
		return Outcome{}, fmt.Errorf("unsupported live eval provider")
	}
	if len(settings.Test.Command) != 0 || len(settings.Build.Command) != 0 {
		return Outcome{}, fmt.Errorf("live verification commands require a separately authorized OS sandbox")
	}
	allowed := []string{"read_file", "list_dir", "glob", "edit_file", "write_file", "apply_patch", "spawn_agent", "run_workflow"}
	if len(settings.BuiltinTools) != 0 {
		selected := tools.ExpandToolSelectors(settings.BuiltinTools)
		filtered := make([]string, 0, len(allowed))
		for _, name := range allowed {
			if selected[name] {
				filtered = append(filtered, name)
			}
		}
		allowed = filtered
		if len(allowed) == 0 {
			return Outcome{}, fmt.Errorf("live task has no safe selected tools")
		}
	}
	recorder, err := NewRecorder(path, upstream, "provider")
	if err != nil {
		return Outcome{}, err
	}
	opts := anvil.Options{Provider: anvil.Provider(provider), APIKey: key, Model: anvil.Model{ID: model}, BaseURL: config.BaseURL,
		BuiltinTools:     allowed,
		PermissionRules:  append([]anvil.PermissionRule(nil), settings.PermissionRules...),
		Permission:       settings.Permission,
		PermissionGroups: append([]string(nil), settings.PermissionGroups...),
		DisallowedTools:  append([]string(nil), settings.DisallowedTools...),
		Subagents:        anvil.SubagentOptions{Enabled: settings.Subagents.Enabled, MaxAgents: 4, MaxDepth: 2},
		MaxWallTime:      60 * time.Second,
	}
	out, runErr := ExecuteAgent(ctx, dir, task, recorder, opts)
	closeErr := recorder.Close()
	if runErr != nil {
		return out, runErr
	}
	return out, closeErr
}
