// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil"
)

func main() {
	apiKey := os.Getenv("ANVIL_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANVIL_API_KEY is required")
		os.Exit(1)
	}
	baseURL := os.Getenv("ANVIL_BASE_URL")
	model := os.Getenv("ANVIL_MODEL")
	if model == "" {
		model = "private/deepseek-v4-pro"
	}
	provider := anvil.Provider(os.Getenv("ANVIL_PROVIDER"))
	if provider == "" {
		provider = anvil.ProviderOpenAI
	}
	cwd := "."
	if len(os.Args) > 1 {
		cwd = os.Args[1]
	}
	msg := "List Go files with glob and summarize the module purpose in one sentence."
	if len(os.Args) > 2 {
		msg = os.Args[2]
	}
	maxSteps := 128
	if v := strings.TrimSpace(os.Getenv("ANVIL_MAX_STEPS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			fmt.Fprintln(os.Stderr, "ANVIL_MAX_STEPS must be a positive integer")
			os.Exit(2)
		}
		maxSteps = n
	}
	var maxWall time.Duration
	if v := strings.TrimSpace(os.Getenv("ANVIL_MAX_WALL")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			fmt.Fprintln(os.Stderr, "ANVIL_MAX_WALL must be a duration like 10m")
			os.Exit(2)
		}
		maxWall = d
	}

	// Host context deadline (code=context_deadline) is separate from MaxWallTime
	// (code=max_wall_time). Default host budget is 30m.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	agent, err := anvil.Create(ctx, anvil.Options{
		Provider:    provider,
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       anvil.Model{ID: model},
		Local:       anvil.LocalOptions{CWD: cwd},
		MaxSteps:    maxSteps,
		MaxWallTime: maxWall,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	defer agent.Close()

	fmt.Printf("agent=%s max_steps=%d cwd=%s\n", agent.AgentID(), maxSteps, cwd)
	fmt.Println("---")

	run, err := agent.Send(ctx, msg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "send:", err)
		os.Exit(1)
	}
	fmt.Println(anvil.FormatRunStart(agent.AgentID(), run.ID()))

	step := 0
	for ev := range run.Stream(ctx) {
		switch ev.Type {
		case anvil.EventAssistant:
			if ev.Text == "" {
				continue
			}
			fmt.Print(ev.Text)
		case anvil.EventToolCall:
			step++
			fmt.Printf("\n\n===== step %d tool_call: %s =====\n", step, ev.Name)
			if len(ev.Args) > 0 {
				fmt.Println(string(ev.Args))
			}
		case anvil.EventToolResult:
			label := "ok"
			if ev.IsError {
				label = "error"
			}
			fmt.Printf("----- tool_result %s (%s) -----\n", ev.Name, label)
			fmt.Println(truncate(ev.Content, 4000))
		case anvil.EventError:
			if ev.Code != "" {
				fmt.Printf("\n[error code=%s] %s\n", ev.Code, ev.Text)
			} else {
				fmt.Printf("\n[error] %s\n", ev.Text)
			}
		case anvil.EventDone:
			if ev.Text != "" {
				fmt.Printf("\n\n===== done =====\n%s\n", ev.Text)
			}
		}
	}

	result, err := run.Wait(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wait:", err)
		os.Exit(1)
	}
	fmt.Println("---")
	fmt.Println(anvil.FormatRunEnd(result))
	if result.Usage != nil {
		fmt.Printf("usage: in=%d out=%d total=%d\n",
			result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.TotalTokens)
	}
	if result.Err != "" {
		fmt.Println("err:", result.Err)
	}
	if result.Status == anvil.StatusError {
		os.Exit(2)
	}
}

func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + fmt.Sprintf("\n… truncated (%d runes total)", len(runes))
}
