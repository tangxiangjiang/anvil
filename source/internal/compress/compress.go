package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
)

// Options controls history compression.
type Options struct {
	MaxChars   int
	KeepRecent int
}

// Result of a compression attempt.
type Result struct {
	Messages []llm.Message
	DidCompress bool
	Summary     string
}

// Apply compresses older messages when over budget. System messages are kept.
// If completer is nil or summarization fails, falls back to a deterministic trim summary.
func Apply(ctx context.Context, completer llm.Completer, model string, messages []llm.Message, opts Options) (Result, error) {
	if opts.MaxChars <= 0 || chars(messages) <= opts.MaxChars {
		return Result{Messages: messages}, nil
	}
	keep := opts.KeepRecent
	if keep <= 0 {
		keep = 6
	}

	var system []llm.Message
	var rest []llm.Message
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			system = append(system, m)
		} else {
			rest = append(rest, m)
		}
	}
	if len(rest) <= keep {
		return Result{Messages: messages}, nil
	}
	old := rest[:len(rest)-keep]
	recent := rest[len(rest)-keep:]

	summary, err := summarize(ctx, completer, model, old)
	if err != nil || summary == "" {
		summary = deterministicSummary(old)
	}
	summaryMsg := llm.Message{
		Role:    llm.RoleUser,
		Content: "[conversation summary]\n" + summary,
	}
	out := append([]llm.Message{}, system...)
	out = append(out, summaryMsg)
	out = append(out, recent...)
	return Result{Messages: out, DidCompress: true, Summary: summaryMsg.Content}, nil
}

func summarize(ctx context.Context, completer llm.Completer, model string, old []llm.Message) (string, error) {
	if completer == nil {
		return "", fmt.Errorf("no completer")
	}
	var b strings.Builder
	b.WriteString("Summarize the following agent conversation for continuity. Keep paths, decisions, and unresolved tasks. Be concise.\n\n")
	for _, m := range old {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		if len(m.ToolCalls) > 0 {
			b.WriteString(" [tool_calls]")
		}
		b.WriteString("\n")
	}
	resp, err := completer.Complete(ctx, llm.Request{
		Model: model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: b.String()},
		},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Message.Content), nil
}

func deterministicSummary(old []llm.Message) string {
	var parts []string
	for _, m := range old {
		line := string(m.Role) + ": " + trim(m.Content, 200)
		if len(m.ToolCalls) > 0 {
			line += " (tools)"
		}
		parts = append(parts, line)
		if len(parts) >= 20 {
			parts = append(parts, "…")
			break
		}
	}
	return strings.Join(parts, "\n")
}

func chars(messages []llm.Message) int {
	n := 0
	for _, m := range messages {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Name) + len(tc.Args)
		}
	}
	return n
}

func trim(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
