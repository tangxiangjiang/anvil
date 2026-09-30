package main

import (
	"context"
	"fmt"
	"os"
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
	if len(os.Args) > 1 {
		cwd = os.Args[1]
	}
	msg := "List Go files with glob and summarize the module purpose in one sentence."
	if len(os.Args) > 2 {
		msg = os.Args[2]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := anvil.Prompt(ctx, msg, anvil.Options{
		Provider: provider,
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Model:    anvil.Model{ID: model},
		Local:    anvil.LocalOptions{CWD: cwd},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup/run error:", err)
		os.Exit(1)
	}
	fmt.Println("status:", result.Status)
	fmt.Println("steps:", result.Steps)
	fmt.Println(result.Result)
	if result.Status == anvil.StatusError {
		os.Exit(2)
	}
}
