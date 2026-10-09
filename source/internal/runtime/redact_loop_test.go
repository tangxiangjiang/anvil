package runtime_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

type secretTool struct{}

func (secretTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "leak_secrets",
		Description: "test tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (secretTool) Execute(ctx context.Context, args json.RawMessage) tools.Result {
	_ = ctx
	_ = args
	return tools.Result{
		Content: "key=sk-abcdefghijklmnopqrstuv Bearer tokensecret99",
	}
}

type callSecretCompleter struct{ n int }

func (c *callSecretCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	if c.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID: "1", Name: "leak_secrets", Args: json.RawMessage(`{}`),
			}},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
}

func TestToolResultRedactedOnStream(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), false)
	if err := reg.Register(secretTool{}); err != nil {
		t.Fatal(err)
	}
	var toolContents []string
	res := runtime.Loop(context.Background(), &callSecretCompleter{}, reg, runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "x", MaxSteps: 4,
	}, func(ev runtime.Event) {
		if ev.Type == "tool_result" {
			toolContents = append(toolContents, ev.Content)
		}
	})
	if res.Status != "finished" {
		t.Fatalf("%+v", res)
	}
	if len(toolContents) != 1 {
		t.Fatalf("events=%v", toolContents)
	}
	got := toolContents[0]
	if strings.Contains(got, "sk-abcdefghijklmnopqrstuv") || strings.Contains(got, "tokensecret99") {
		t.Fatalf("secrets not redacted: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %q", got)
	}
	// Model history should also see redacted content.
	for _, m := range res.Messages {
		if m.Role == llm.RoleTool && (strings.Contains(m.Content, "sk-abcdefghijklmnopqrstuv") || strings.Contains(m.Content, "tokensecret99")) {
			t.Fatalf("history leaked secrets: %q", m.Content)
		}
	}
}
