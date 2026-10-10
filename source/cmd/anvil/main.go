// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "prompt":
		err = cmdPrompt(args)
	case "chat":
		err = cmdChat(args)
	case "list":
		err = cmdList(args)
	case "get":
		err = cmdGet(args)
	case "models":
		err = cmdModels(args)
	case "index":
		err = cmdIndex(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if isRunError(err) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

type runExitError struct{ msg string }

func (e *runExitError) Error() string { return e.msg }

func isRunError(err error) bool {
	_, ok := err.(*runExitError)
	return ok
}

func usage() {
	fmt.Fprintf(os.Stderr, `anvil — local Agent SDK CLI

Usage:
  anvil prompt [flags] <cwd> <message>
  anvil chat [flags] [--resume agent-id] <cwd>
  anvil list [flags] <cwd>
  anvil get [flags] <cwd> <agent-id>
  anvil models [flags]
  anvil index build|status|clear [flags] <cwd>

Flags (also via env / optional .anvil/config JSON):
  --provider  openai|anthropic     (ANVIL_PROVIDER)
  --model     model id             (ANVIL_MODEL)
  --base-url  API root             (ANVIL_BASE_URL)
  --max-steps N                    (ANVIL_MAX_STEPS, default 128)
  --max-wall  duration             (ANVIL_MAX_WALL, e.g. 10m; 0 = unset)
  --store     jsonl|sqlite         (default jsonl)
  --api-key   key                  (ANVIL_API_KEY; preferred over config)

Timeouts: prompt wraps the run in a 30m host context deadline (code=context_deadline).
Options.MaxWallTime / --max-wall is separate (code=max_wall_time). Explicit cancel → cancelled.

Exit codes: 0 ok, 1 startup/config, 2 run error (see RunResult.Code)
`)
}

type cliFlags struct {
	provider string
	model    string
	baseURL  string
	apiKey   string
	maxSteps int
	maxWall  time.Duration
	store    string
	resume   string
	cwd      string
	message  string
	rest     []string
}

func parseFlags(args []string) (cliFlags, error) {
	f := cliFlags{maxSteps: -1, maxWall: -1}
	for len(args) > 0 {
		a := args[0]
		if !strings.HasPrefix(a, "-") {
			break
		}
		args = args[1:]
		switch {
		case a == "--resume" && len(args) > 0:
			f.resume = args[0]
			args = args[1:]
		case a == "--provider" && len(args) > 0:
			f.provider = args[0]
			args = args[1:]
		case a == "--model" && len(args) > 0:
			f.model = args[0]
			args = args[1:]
		case a == "--base-url" && len(args) > 0:
			f.baseURL = args[0]
			args = args[1:]
		case a == "--api-key" && len(args) > 0:
			f.apiKey = args[0]
			args = args[1:]
		case a == "--max-steps" && len(args) > 0:
			n, err := strconv.Atoi(args[0])
			if err != nil || n <= 0 {
				return f, fmt.Errorf("--max-steps must be a positive integer")
			}
			f.maxSteps = n
			args = args[1:]
		case a == "--max-wall" && len(args) > 0:
			d, err := time.ParseDuration(args[0])
			if err != nil || d < 0 {
				return f, fmt.Errorf("--max-wall must be a duration like 10m")
			}
			f.maxWall = d
			args = args[1:]
		case a == "--store" && len(args) > 0:
			f.store = args[0]
			args = args[1:]
		default:
			return f, fmt.Errorf("unknown flag %s", a)
		}
	}
	f.rest = args
	return f, nil
}

type fileConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	MaxSteps int    `json:"max_steps"`
	Store    string `json:"store"`
	APIKey   string `json:"api_key"` // discouraged; env preferred
}

func loadConfig(cwd string) fileConfig {
	path := filepath.Join(cwd, ".anvil", "config")
	data, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}
	}
	var c fileConfig
	_ = json.Unmarshal(data, &c)
	return c
}

func buildOptions(cwd string, f cliFlags) (anvil.Options, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return anvil.Options{}, err
	}
	cfg := loadConfig(abs)

	apiKey := firstNonEmpty(f.apiKey, os.Getenv("ANVIL_API_KEY"), cfg.APIKey)
	provider := firstNonEmpty(f.provider, os.Getenv("ANVIL_PROVIDER"), cfg.Provider, string(anvil.ProviderOpenAI))
	model := firstNonEmpty(f.model, os.Getenv("ANVIL_MODEL"), cfg.Model, "private/deepseek-v4-pro")
	baseURL := firstNonEmpty(f.baseURL, os.Getenv("ANVIL_BASE_URL"), cfg.BaseURL)
	storeKind := firstNonEmpty(f.store, cfg.Store, "jsonl")

	maxSteps := f.maxSteps
	if maxSteps < 0 {
		if v := strings.TrimSpace(os.Getenv("ANVIL_MAX_STEPS")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return anvil.Options{}, fmt.Errorf("ANVIL_MAX_STEPS must be a positive integer")
			}
			maxSteps = n
		} else if cfg.MaxSteps > 0 {
			maxSteps = cfg.MaxSteps
		} else {
			maxSteps = 0 // SDK default 128
		}
	}

	var maxWall time.Duration
	if f.maxWall >= 0 {
		maxWall = f.maxWall
	} else if v := strings.TrimSpace(os.Getenv("ANVIL_MAX_WALL")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return anvil.Options{}, fmt.Errorf("ANVIL_MAX_WALL must be a duration like 10m")
		}
		maxWall = d
	}

	return anvil.Options{
		Provider:    anvil.Provider(provider),
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       anvil.Model{ID: model},
		Local:       anvil.LocalOptions{CWD: abs, Store: storeKind},
		MaxSteps:    maxSteps,
		MaxWallTime: maxWall,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func cmdPrompt(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(f.rest) < 2 {
		return fmt.Errorf("usage: anvil prompt [flags] <cwd> <message>")
	}
	cwd := f.rest[0]
	msg := strings.Join(f.rest[1:], " ")
	opts, err := buildOptions(cwd, f)
	if err != nil {
		return err
	}
	if opts.APIKey == "" {
		return fmt.Errorf("ANVIL_API_KEY is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	agent, err := anvil.Create(ctx, opts)
	if err != nil {
		return err
	}
	defer agent.Close()

	fmt.Printf("agent=%s max_steps=%d store=%s\n", agent.AgentID(), opts.MaxSteps, opts.Local.Store)
	run, err := agent.Send(ctx, msg)
	if err != nil {
		return err
	}
	fmt.Println(anvil.FormatRunStart(agent.AgentID(), run.ID()))
	printStream(run.Stream(ctx))
	res, err := run.Wait(ctx)
	if err != nil {
		return err
	}
	printResult(res)
	if res.Status == anvil.StatusError || res.Status == anvil.StatusCancelled {
		if res.Status == anvil.StatusError {
			return &runExitError{msg: res.Err}
		}
	}
	return nil
}

func cmdChat(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(f.rest) < 1 {
		return fmt.Errorf("usage: anvil chat [flags] [--resume id] <cwd>")
	}
	cwd := f.rest[0]
	opts, err := buildOptions(cwd, f)
	if err != nil {
		return err
	}
	if opts.APIKey == "" {
		return fmt.Errorf("ANVIL_API_KEY is required")
	}
	opts.MaxWallTime = 10 * time.Minute

	ctx := context.Background()
	var agent *anvil.Agent
	if f.resume != "" {
		agent, err = anvil.Resume(ctx, f.resume, opts)
	} else {
		agent, err = anvil.Create(ctx, opts)
	}
	if err != nil {
		return err
	}
	defer agent.Close()
	fmt.Println("agent", agent.AgentID())
	fmt.Println("type a message (empty / exit / quit to stop)")

	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" || line == "exit" || line == "quit" {
			break
		}
		runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		run, err := agent.Send(runCtx, line)
		if err != nil {
			stop()
			fmt.Fprintln(os.Stderr, "send:", err)
			continue
		}
		fmt.Println(anvil.FormatRunStart(agent.AgentID(), run.ID()))
		go func() {
			<-runCtx.Done()
			_ = run.Cancel(context.Background())
		}()
		printStream(run.Stream(runCtx))
		res, err := run.Wait(context.Background())
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "wait:", err)
			continue
		}
		fmt.Println(anvil.FormatRunEnd(res))
	}
	fmt.Println("agent id:", agent.AgentID())
	return nil
}

func cmdList(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(f.rest) < 1 {
		return fmt.Errorf("usage: anvil list [flags] <cwd>")
	}
	opts, err := buildOptions(f.rest[0], f)
	if err != nil {
		return err
	}
	infos, err := anvil.List(context.Background(), anvil.ListOptions{Local: opts.Local})
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Println("(no agents)")
		return nil
	}
	for _, info := range infos {
		fmt.Printf("%s  model=%s  status=%s  runs=%d  updated=%s\n",
			info.ID, info.Model, info.LastStatus, info.Runs, info.UpdatedAt.Format(time.RFC3339))
	}
	return nil
}

func cmdGet(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(f.rest) < 2 {
		return fmt.Errorf("usage: anvil get [flags] <cwd> <agent-id>")
	}
	opts, err := buildOptions(f.rest[0], f)
	if err != nil {
		return err
	}
	info, err := anvil.Get(context.Background(), f.rest[1], anvil.ListOptions{Local: opts.Local})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(info)
}

func printStream(ch <-chan anvil.Event) {
	for ev := range ch {
		switch ev.Type {
		case anvil.EventProgress:
			fmt.Printf("\n[progress %d/%d]\n", ev.Step, ev.MaxSteps)
		case anvil.EventAssistant:
			fmt.Print(ev.Text)
		case anvil.EventToolCall:
			fmt.Printf("\n[tool_call %s] %s\n", ev.Name, string(ev.Args))
		case anvil.EventToolResult:
			label := "ok"
			if ev.IsError {
				label = "error"
			}
			fmt.Printf("[tool_result %s %s] %s\n", ev.Name, label, truncate(ev.Content, 2000))
		case anvil.EventError:
			if ev.Code != "" {
				fmt.Printf("\n[error code=%s] %s\n", ev.Code, ev.Text)
			} else {
				fmt.Printf("\n[error] %s\n", ev.Text)
			}
		case anvil.EventDone:
			if ev.Text != "" {
				fmt.Printf("\n")
			}
		}
	}
	fmt.Println()
}

func printResult(res *anvil.RunResult) {
	fmt.Println("---")
	fmt.Println(anvil.FormatRunEnd(res))
	if res.Usage != nil {
		fmt.Printf("usage: in=%d out=%d total=%d\n",
			res.Usage.InputTokens, res.Usage.OutputTokens, res.Usage.TotalTokens)
	}
	if res.Err != "" {
		fmt.Println("err:", res.Err)
	}
}

func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "…"
}

func cmdIndex(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: anvil index build|status|clear [flags] <cwd>")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "build":
		return cmdIndexBuild(rest)
	case "status":
		return cmdIndexStatus(rest)
	case "clear":
		return cmdIndexClear(rest)
	default:
		return fmt.Errorf("unknown index subcommand %q (build|status|clear)", sub)
	}
}

func cmdIndexBuild(args []string) error {
	var embedURL, embedModel, embedKey string
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		a := args[0]
		args = args[1:]
		switch {
		case a == "--embed-url" && len(args) > 0:
			embedURL = args[0]
			args = args[1:]
		case a == "--embed-model" && len(args) > 0:
			embedModel = args[0]
			args = args[1:]
		case a == "--embed-key" && len(args) > 0:
			embedKey = args[0]
			args = args[1:]
		case a == "--base-url" && len(args) > 0:
			embedURL = args[0]
			args = args[1:]
		default:
			return fmt.Errorf("unknown flag %s", a)
		}
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: anvil index build [--embed-url URL] [--embed-model ID] [--embed-key KEY] <cwd>")
	}
	cwd := args[0]
	if embedURL == "" {
		embedURL = firstNonEmpty(os.Getenv("ANVIL_EMBED_BASE_URL"), os.Getenv("ANVIL_BASE_URL"))
	}
	if embedModel == "" {
		embedModel = os.Getenv("ANVIL_EMBED_MODEL")
	}
	if embedKey == "" {
		embedKey = firstNonEmpty(os.Getenv("ANVIL_EMBED_API_KEY"), os.Getenv("ANVIL_API_KEY"))
	}
	// Embed only when both URL and model are set.
	opts := anvil.IndexBuildOptions{}
	if embedURL != "" && embedModel != "" {
		opts.EmbedBaseURL = embedURL
		opts.EmbedModel = embedModel
		opts.EmbedAPIKey = embedKey
	}
	res, err := anvil.BuildIndex(context.Background(), cwd, opts)
	if err != nil {
		return err
	}
	fmt.Printf("index=%s files_seen=%d indexed=%d skipped=%d removed=%d chunks=%d embedded=%d\n",
		res.DBPath, res.FilesSeen, res.FilesIndexed, res.FilesSkipped, res.FilesRemoved, res.Chunks, res.Embedded)
	return nil
}

func cmdIndexStatus(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: anvil index status <cwd>")
	}
	st, err := anvil.IndexStat(args[0])
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(st)
}

func cmdIndexClear(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: anvil index clear <cwd>")
	}
	return anvil.ClearIndex(args[0])
}

func cmdModels(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	baseURL := firstNonEmpty(f.baseURL, os.Getenv("ANVIL_BASE_URL"))
	apiKey := firstNonEmpty(f.apiKey, os.Getenv("ANVIL_API_KEY"))
	if baseURL == "" {
		return fmt.Errorf("ANVIL_BASE_URL or --base-url required (gateway root ending in /v1)")
	}
	list, err := anvil.ListModels(context.Background(), anvil.ListModelsOptions{
		BaseURL: baseURL,
		APIKey:  apiKey,
	})
	if err != nil {
		return err
	}
	for _, m := range list {
		extra := ""
		if m.UpstreamModel != "" {
			extra = " upstream=" + m.UpstreamModel
		}
		if m.Provider != "" {
			extra += " provider=" + m.Provider
		}
		fmt.Printf("%s%s\n", m.ID, extra)
	}
	return nil
}
