package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

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
	agentID := ""
	args := os.Args[1:]
	for len(args) > 0 {
		switch {
		case args[0] == "--resume" && len(args) >= 2:
			agentID = args[1]
			args = args[2:]
		case strings.HasPrefix(args[0], "-"):
			fmt.Fprintf(os.Stderr, "unknown flag %s\n", args[0])
			os.Exit(2)
		default:
			cwd = args[0]
			args = args[1:]
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	opts := anvil.Options{
		Provider:    provider,
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       anvil.Model{ID: model},
		Local:       anvil.LocalOptions{CWD: abs},
		MaxWallTime: 10 * time.Minute,
	}

	ctx := context.Background()
	var agent *anvil.Agent
	if agentID != "" {
		agent, err = anvil.Resume(ctx, agentID, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "resume:", err)
			os.Exit(1)
		}
		fmt.Println("resumed", agent.AgentID())
	} else {
		agent, err = anvil.Create(ctx, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create:", err)
			os.Exit(1)
		}
		fmt.Println("created", agent.AgentID())
		fmt.Println("resume later with: go run ./examples/interactive --resume", agent.AgentID(), abs)
	}
	defer agent.Close()

	in := bufio.NewScanner(os.Stdin)
	fmt.Println("type a message (empty / exit / quit to stop)")
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
		go func() {
			<-runCtx.Done()
			_ = run.Cancel(context.Background())
		}()
		for ev := range run.Stream(runCtx) {
			switch ev.Type {
			case anvil.EventAssistant:
				fmt.Print(ev.Text)
			case anvil.EventToolCall:
				fmt.Printf("\n[tool_call %s]\n", ev.Name)
			case anvil.EventToolResult:
				fmt.Printf("[tool_result %s error=%v]\n", ev.Name, ev.IsError)
			case anvil.EventError:
				fmt.Printf("\n[error] %s\n", ev.Text)
			}
		}
		res, err := run.Wait(context.Background())
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "wait:", err)
			continue
		}
		if res.Status == anvil.StatusFinished && res.Result != "" {
			// already streamed; ensure newline
			fmt.Println()
		}
		fmt.Printf("status=%s steps=%d agent=%s\n", res.Status, res.Steps, agent.AgentID())
	}
	fmt.Println("agent id:", agent.AgentID())
}
