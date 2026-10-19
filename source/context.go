// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

// ContextOptions is the declared token budget (0.43+).
//
// The estimate it is compared against is not the provider's token count; see
// CompactResult and the design chapter. The window is what the caller knows
// about its model, which is the only place that knowledge exists.
type ContextOptions struct {
	// WindowTokens is the model's context window. 0 means unknown, and unknown
	// turns automatic compaction off.
	WindowTokens int
	// AutoCompactAt is the fraction of the window at which compaction triggers.
	// 0 or a value outside (0, 1] means 0.8.
	AutoCompactAt float64
	// KeepRecent is how many trailing messages compaction keeps verbatim before
	// moving the cut to a user message. 0 means 8; a value below 2 means 8.
	KeepRecent int
}

// ContextUsage describes where an agent's conversation stands against its
// budget. EstimatedTokens is an estimate, never the provider's count.
type ContextUsage struct {
	EstimatedTokens int
	WindowTokens    int
	CompactAtTokens int
	Messages        int
}

// CompactResult is what Agent.Compact did.
type CompactResult struct {
	// Compacted is false when there was nothing old enough to summarize; the
	// call is then a no-op rather than an error.
	Compacted  bool
	Before     int
	After      int
	Summarized int
	// Summary is the text that was kept, prefixed as it is in the store.
	Summary string
}

// contextThreshold is the token estimate at which automatic compaction fires.
// Zero means no window, and therefore no automatic compaction.
func contextThreshold(window int, at float64) int {
	if window <= 0 {
		return 0
	}
	if at <= 0 || at > 1 {
		at = 0.8
	}
	n := int(float64(window) * at)
	if n < 1 {
		n = 1
	}
	return n
}

// Compact summarizes an idle agent's older turns and keeps a safe tail.
//
// It is the manual counterpart to automatic compaction: it runs regardless of
// the estimate, because a person who asked meant it. It refuses while a run is
// in progress rather than racing the runtime's message slice, and it persists
// through the same sink the runtime uses — the summary marker, then the kept
// tail. The tail is written again rather than left where it already is because
// Resume reads from the last marker forward: a marker with no records after it
// would resume into a summary alone.
func (a *Agent) Compact(ctx context.Context) (CompactResult, error) {
	if a == nil {
		return CompactResult{}, newError("nil agent", false, nil)
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return CompactResult{}, newError("agent is closed", false, nil)
	}
	if a.running {
		a.mu.Unlock()
		return CompactResult{}, newError("a run is in progress", false, nil)
	}
	if a.compacting {
		a.mu.Unlock()
		return CompactResult{}, newError("a compaction is already in progress", false, nil)
	}
	// Compaction holds the flag, not the lock: the summarizer is a model call,
	// and holding a.mu across it would block every reader for its duration.
	a.compacting = true
	history := append([]llm.Message(nil), a.history...)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.compacting = false
		a.mu.Unlock()
	}()

	cr, err := compress.Compact(ctx, a.complete, a.opts.Model.ID, history, compress.BudgetOptions{
		KeepRecent: a.opts.Context.KeepRecent,
		Force:      true,
	})
	if err != nil {
		return CompactResult{}, newError("compact history", false, err)
	}
	if !cr.DidCompress {
		return CompactResult{}, nil
	}
	a.persist(store.Record{
		Type:    store.TypeSummary,
		Role:    string(llm.RoleUser),
		Content: cr.Summary,
		Name:    compress.SummaryMarker,
	})
	for _, m := range cr.Recent {
		a.persistMessage("", m)
	}
	a.mu.Lock()
	a.history = nonSystemMessages(cr.Messages)
	a.mu.Unlock()
	return CompactResult{
		Compacted:  true,
		Before:     cr.Before,
		After:      cr.After,
		Summarized: cr.Summarized,
		Summary:    cr.Summary,
	}, nil
}

// ContextUsage reports the conversation's estimate against the declared window.
// It is safe to call while a run is in progress: the runtime replaces the
// history only when the run ends, so the figure is the last completed state.
func (a *Agent) ContextUsage() ContextUsage {
	if a == nil {
		return ContextUsage{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	msgs := make([]llm.Message, 0, len(a.history)+1)
	if a.systemPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt})
	}
	msgs = append(msgs, a.history...)
	return ContextUsage{
		EstimatedTokens: compress.Estimate(msgs),
		WindowTokens:    a.opts.Context.WindowTokens,
		CompactAtTokens: contextThreshold(a.opts.Context.WindowTokens, a.opts.Context.AutoCompactAt),
		Messages:        len(a.history),
	}
}
