// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package compress

import (
	"context"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

type fixedCompleter struct {
	text  string
	calls int
}

func (f *fixedCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	f.calls++
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: f.text}}, nil
}

func TestEstimateEmptyIsZero(t *testing.T) {
	if got := Estimate(nil); got != 0 {
		t.Fatalf("Estimate(nil) = %d, want 0", got)
	}
	if got := Estimate([]llm.Message{}); got != 0 {
		t.Fatalf("Estimate(empty) = %d, want 0", got)
	}
}

// TestEstimateIsMonotonic pins the shape rather than exact token counts: more
// text, a tool call, or an image must never make the estimate smaller.
func TestEstimateIsMonotonic(t *testing.T) {
	base := Estimate([]llm.Message{{Role: llm.RoleUser, Content: "abcd"}})
	if base != perMessageTokens+1 {
		t.Fatalf("base = %d, want %d", base, perMessageTokens+1)
	}
	longer := Estimate([]llm.Message{{Role: llm.RoleUser, Content: "abcdefgh"}})
	if longer <= base {
		t.Fatalf("longer content did not raise the estimate: %d <= %d", longer, base)
	}

	withCall := Estimate([]llm.Message{{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID: "c1", Name: "read_file", Args: []byte(`{"path":"a.txt"}`),
		}},
	}})
	if withCall <= perMessageTokens {
		t.Fatalf("tool call not counted: %d", withCall)
	}
	biggerArgs := Estimate([]llm.Message{{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID: "c1", Name: "read_file", Args: []byte(`{"path":"a-much-longer-name.txt"}`),
		}},
	}})
	if biggerArgs <= withCall {
		t.Fatalf("tool arguments not counted: %d <= %d", biggerArgs, withCall)
	}

	withImage := Estimate([]llm.Message{{
		Role:  llm.RoleUser,
		Parts: []llm.ContentPart{{Type: "image", MIME: "image/png"}},
	}})
	if withImage != perMessageTokens+EstimateImageTokens {
		t.Fatalf("image estimate = %d, want %d", withImage, perMessageTokens+EstimateImageTokens)
	}
	withText := Estimate([]llm.Message{{
		Role:  llm.RoleUser,
		Parts: []llm.ContentPart{{Type: "text", Text: "abcdefgh"}},
	}})
	if withText != perMessageTokens+2 {
		t.Fatalf("text part estimate = %d, want %d", withText, perMessageTokens+2)
	}
}

func TestEstimateCountsRunesNotBytes(t *testing.T) {
	// Five CJK runes are five bytes short of three times that; counting bytes
	// would report more than twice the tokens.
	cjk := Estimate([]llm.Message{{Role: llm.RoleUser, Content: "上下文管理"}})
	if cjk != perMessageTokens+2 {
		t.Fatalf("cjk estimate = %d, want %d", cjk, perMessageTokens+2)
	}
}

func TestCompactUnderThresholdIsUntouched(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}
	f := &fixedCompleter{text: "summary"}
	res, err := Compact(context.Background(), f, "m", msgs, BudgetOptions{
		MaxTokens: Estimate(msgs) + 1, KeepRecent: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DidCompress {
		t.Fatal("compacted below the threshold")
	}
	if len(res.Messages) != len(msgs) || f.calls != 0 {
		t.Fatalf("messages changed or summarizer called: %+v calls=%d", res.Messages, f.calls)
	}
}

func TestCompactOverThresholdStartsTailAtUser(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleUser, Content: "u3"},
		{Role: llm.RoleAssistant, Content: "a3"},
		{Role: llm.RoleUser, Content: "u4"},
	}
	f := &fixedCompleter{text: "S"}
	res, err := Compact(context.Background(), f, "m", msgs, BudgetOptions{
		MaxTokens: 1, KeepRecent: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DidCompress {
		t.Fatal("did not compact above the threshold")
	}
	if res.Summarized != 4 {
		t.Fatalf("Summarized = %d, want 4", res.Summarized)
	}
	if res.Before <= res.After {
		t.Fatalf("before/after not reported: %d -> %d", res.Before, res.After)
	}
	if len(res.Messages) != 5 {
		t.Fatalf("want system + summary + 3 tail, got %+v", res.Messages)
	}
	if res.Messages[0].Role != llm.RoleSystem {
		t.Fatalf("system message not kept: %+v", res.Messages)
	}
	sum := res.Messages[1]
	if sum.Role != llm.RoleUser || sum.Name != SummaryMarker {
		t.Fatalf("summary message shape changed: %+v", sum)
	}
	if !strings.HasPrefix(sum.Content, "[conversation summary]\nS") {
		t.Fatalf("summary content = %q", sum.Content)
	}
	if res.Messages[2].Role != llm.RoleUser || res.Messages[2].Content != "u3" {
		t.Fatalf("tail does not start at a user turn: %+v", res.Messages[2:])
	}
}

func TestTailStartMovesForwardOffAToolResult(t *testing.T) {
	rest := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "x"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "r1"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c2", Name: "x"}}},
		{Role: llm.RoleTool, ToolCallID: "c2", Content: "r2"},
		{Role: llm.RoleUser, Content: "u2"},
	}
	if got := tailStart(rest, 2); got != 5 {
		t.Fatalf("tailStart = %d, want 5 (the user message after the tool exchange)", got)
	}
	for _, i := range []int{tailStart(rest, 2), tailStart(rest, 4)} {
		if rest[i].Role == llm.RoleTool {
			t.Fatalf("tail starts at a tool result: %+v", rest[i])
		}
	}
}

func TestTailStartFallsBackToTheLastUserTurn(t *testing.T) {
	rest := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "x"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "r1"},
		{Role: llm.RoleAssistant, Content: "a"},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "r2"},
	}
	if got := tailStart(rest, 2); got != 0 {
		t.Fatalf("tailStart = %d, want the last user turn at 0", got)
	}
	// With the fallback at 0 there is nothing older to fold, and Compact must
	// leave the conversation alone rather than summarize nothing.
	res, err := Compact(context.Background(), &fixedCompleter{text: "S"}, "m", rest, BudgetOptions{
		MaxTokens: 1, KeepRecent: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DidCompress {
		t.Fatalf("compacted with no boundary to keep: %+v", res.Messages)
	}
}

func TestTailStartWithoutAUserMessageKeepsTheLastMessages(t *testing.T) {
	rest := []llm.Message{
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleTool, Content: "r1"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleTool, Content: "r2"},
		{Role: llm.RoleAssistant, Content: "a3"},
	}
	if got := tailStart(rest, 2); got != 3 {
		t.Fatalf("tailStart = %d, want 3", got)
	}
}

func TestCompactForceWithNothingToFold(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
	}
	f := &fixedCompleter{text: "S"}
	res, err := Compact(context.Background(), f, "m", msgs, BudgetOptions{Force: true, KeepRecent: 8})
	if err != nil {
		t.Fatal(err)
	}
	if res.DidCompress || f.calls != 0 {
		t.Fatalf("forced compaction folded a single turn: %+v calls=%d", res, f.calls)
	}
}

// TestCompactDoesNotRefoldALoneSummary covers the second /compact: the only
// thing before the tail is the previous summary, so folding it would only spend
// a model call to produce the same marker again.
func TestCompactDoesNotRefoldALoneSummary(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Name: SummaryMarker, Content: "[conversation summary]\nS"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "a2"},
	}
	f := &fixedCompleter{text: "S2"}
	res, err := Compact(context.Background(), f, "m", msgs, BudgetOptions{Force: true, KeepRecent: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.DidCompress || f.calls != 0 {
		t.Fatalf("a lone summary was folded again: %+v calls=%d", res, f.calls)
	}
	if len(res.Messages) != len(msgs) {
		t.Fatalf("messages changed: %d vs %d", len(res.Messages), len(msgs))
	}
}
