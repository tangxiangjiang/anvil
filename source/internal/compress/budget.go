// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package compress

import (
	"context"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/llm"
)

// EstimateImageTokens is the declared cost of one image part. A provider's
// real cost depends on its own tiling and resolution rules, and no provider
// publishes a formula worth reimplementing here; one fixed number is honest
// about being an estimate and is large enough that an image is never free.
const EstimateImageTokens = 1600

// perMessageTokens is the fixed overhead a chat template adds around one
// message — the role tag, delimiters, and separator. It is deliberately the
// same for every role: a role-specific number would look measured.
const perMessageTokens = 4

// toolCallOverheadTokens approximates the framing of one tool call on top of
// the name and arguments the estimate already counts.
const toolCallOverheadTokens = 85

// Estimate returns an approximate token count for messages.
//
// It exists so a budget can be enforced without a tokenizer: no tokenizer is
// shipped, providers disagree about how they count, and a per-model table
// would be wrong the moment a model is added. Four characters per token is the
// usual rough ratio for English text and tool JSON. The result is used to
// decide whether to compact and to draw a meter — never reported as the
// provider's token count.
func Estimate(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += perMessageTokens + runeTokens(m.Content)
		for _, p := range m.Parts {
			switch p.Type {
			case "image":
				total += EstimateImageTokens
			default:
				total += runeTokens(p.Text)
			}
		}
		for _, tc := range m.ToolCalls {
			total += toolCallOverheadTokens + runeTokens(tc.Name) + runeTokens(string(tc.Args))
		}
	}
	return total
}

// runeTokens estimates the tokens in s. It counts runes rather than bytes so
// that a CJK conversation is not under-counted: one rune of CJK is usually one
// token, while its three UTF-8 bytes would suggest three quarters of one.
func runeTokens(s string) int {
	return (utf8.RuneCountInString(s) + 3) / 4
}

// BudgetOptions controls budget-based compaction.
type BudgetOptions struct {
	// MaxTokens is the estimate at which compaction triggers. Zero disables the
	// threshold; Force still compacts.
	MaxTokens int
	// KeepRecent is how many trailing messages to keep verbatim before the
	// boundary search moves the cut to a user turn. Zero means 8.
	KeepRecent int
	// Force compacts regardless of MaxTokens. A person asking for a compaction
	// should get one even when the estimate says there is room.
	Force bool
}

// Compact summarizes older turns and keeps a safe tail.
//
// It is Apply's budget-based sibling and differs in one rule that matters:
// the tail begins at a user message, so a tool result is never separated from
// the assistant call that produced it. It also reports the estimates on both
// sides, which is what a client shows after a compaction.
func Compact(ctx context.Context, completer llm.Completer, model string, messages []llm.Message, opts BudgetOptions) (Result, error) {
	if !opts.Force {
		if opts.MaxTokens <= 0 || Estimate(messages) <= opts.MaxTokens {
			return Result{Messages: messages}, nil
		}
	}

	keep := opts.KeepRecent
	if keep <= 0 {
		keep = 8
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
	cut := tailStart(rest, keep)
	if cut <= 0 {
		return Result{Messages: messages}, nil
	}
	old := rest[:cut]
	recent := rest[cut:]

	summary, err := summarize(ctx, completer, model, old)
	if err != nil || summary == "" {
		summary = deterministicSummary(old)
	}
	summaryMsg := llm.Message{
		Role:    llm.RoleUser,
		Name:    SummaryMarker,
		Content: "[conversation summary]\n" + summary,
	}
	out := append([]llm.Message{}, system...)
	out = append(out, summaryMsg)
	out = append(out, recent...)
	return Result{
		Messages:    out,
		DidCompress: true,
		Summary:     summaryMsg.Content,
		Recent:      append([]llm.Message(nil), recent...),
		Before:      Estimate(messages),
		After:       Estimate(out),
		Summarized:  len(old),
	}, nil
}

// tailStart picks the index where the kept tail begins.
//
// The preferred cut is `len(rest)-keep`, moved forward to the first user
// message at or after it so the tail opens a turn. Moving forward is safe: it
// keeps fewer messages, never more. When there is no user message after that
// point — a tool result trailing a very long turn — the cut falls back to the
// last user message, which is the most recent boundary that exists. A history
// with no user message at all keeps its last `keep` messages, which is the
// old rule and the only one available.
func tailStart(rest []llm.Message, keep int) int {
	start := len(rest) - keep
	if start < 0 {
		start = 0
	}
	for i := start; i < len(rest); i++ {
		if rest[i].Role == llm.RoleUser {
			return i
		}
	}
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i].Role == llm.RoleUser {
			return i
		}
	}
	return start
}
