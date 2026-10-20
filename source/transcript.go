// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anvil-dev/anvil/internal/store"
)

// Transcript entry kinds.
const (
	// TranscriptMessage is one stored model message: a user turn, an assistant
	// turn (which may carry the tool calls it made), or a tool's result.
	TranscriptMessage = "message"
	// TranscriptRunEnd is where a run ended, with its status and usage.
	TranscriptRunEnd = "run_end"
	// TranscriptSummary is a compaction boundary (0.43+): the summary the model
	// continued from, at the point in the conversation where it replaced the
	// turns before it. The turns themselves are still listed — compaction is a
	// marker in an append-only store, not a deletion.
	TranscriptSummary = "summary"
)

// DefaultTranscriptLimit caps LoadTranscript when the caller asks for no
// particular limit. It is a bound on what one render may cost, not a page size:
// a client shows the tail and says how much it left out.
const DefaultTranscriptLimit = 200

// TranscriptToolCall is one tool invocation as the store recorded it. The
// arguments are carried verbatim: without them a replayed tool call is a name
// and a state, and a client cannot show what the call was for.
type TranscriptToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// TranscriptEntry is one unit of a stored conversation.
//
// It is a projection for a reader, not for the model: nothing here is folded,
// summarized or compressed, because a person reading a conversation wants the
// turns that happened rather than the context the model was given.
type TranscriptEntry struct {
	// Kind is TranscriptMessage or TranscriptRunEnd.
	Kind string
	// RunID is the run this entry belonged to.
	RunID string

	// Message fields. Role is the store's own word: user, assistant or tool.
	Role       string
	Content    string
	ToolCallID string
	Name       string
	ToolCalls  []TranscriptToolCall

	// Run-end fields.
	Status string
	Steps  int
	Usage  *Usage
}

// TranscriptOptions configures LoadTranscript. Only Local.CWD is required.
type TranscriptOptions struct {
	Local LocalOptions
	// Limit caps how many entries are returned, keeping the newest. Zero means
	// DefaultTranscriptLimit.
	Limit int
}

// TranscriptResult is a stored conversation, oldest first.
type TranscriptResult struct {
	Entries []TranscriptEntry
	// Dropped is how many entries before Entries[0] were left out — by the cap,
	// and by the run boundary it was moved back to. A client that shows the tail
	// can say so instead of implying the conversation started there.
	Dropped int
}

// LoadTranscript reads the stored conversation for agentID under
// opts.Local.CWD, oldest first. It is the same load Resume performs, read for a
// different purpose — a client showing a session it did not watch — so it can
// read an agent that has no live session, and it writes nothing.
//
// What it returns is what the store holds: the messages that were streamed to a
// client and persisted, with tool output already redacted on its way in. What
// it cannot return it never had — reasoning was streamed and dropped rather
// than stored, and the API key is nowhere in the file.
func LoadTranscript(ctx context.Context, agentID string, opts TranscriptOptions) (TranscriptResult, error) {
	_ = ctx
	if agentID == "" {
		return TranscriptResult{}, newError("agent id is required", false, nil)
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return TranscriptResult{}, err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return TranscriptResult{}, newError("open store", false, err)
	}
	defer st.Close()
	recs, err := st.Load(agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return TranscriptResult{}, newError("agent not found: "+agentID, false, nil)
		}
		return TranscriptResult{}, newError("load agent store", false, err)
	}
	return transcriptFromRecords(recs, opts.Limit), nil
}

// transcriptFromRecords projects stored records into a renderable conversation.
//
// It is deliberately not historyFromRecords. That function folds records into
// the model's messages: it seeds the summary marker, applies the compression
// rule and returns []llm.Message. Every one of those is wrong for a reader —
// the compression one silently, since it would drop turns the user watched
// scroll past. Two projections over one load, because the two have different
// jobs.
func transcriptFromRecords(recs []store.Record, limit int) TranscriptResult {
	if limit <= 0 {
		limit = DefaultTranscriptLimit
	}
	entries := make([]TranscriptEntry, 0, len(recs))
	for _, rec := range recs {
		switch rec.Type {
		case store.TypeMessage:
			entry := TranscriptEntry{
				Kind:       TranscriptMessage,
				RunID:      rec.RunID,
				Role:       rec.Role,
				Content:    rec.Content,
				ToolCallID: rec.ToolCallID,
				Name:       rec.Name,
			}
			for _, tc := range rec.ToolCalls {
				entry.ToolCalls = append(entry.ToolCalls, TranscriptToolCall{
					ID:   tc.ID,
					Name: tc.Name,
					Args: tc.Args,
				})
			}
			entries = append(entries, entry)
		case store.TypeSummary:
			entries = append(entries, TranscriptEntry{
				Kind:    TranscriptSummary,
				RunID:   rec.RunID,
				Role:    rec.Role,
				Content: rec.Content,
				Name:    rec.Name,
			})
		case store.TypeRunEnd:
			entry := TranscriptEntry{
				Kind:   TranscriptRunEnd,
				RunID:  rec.RunID,
				Status: rec.Status,
				Steps:  rec.Steps,
			}
			if rec.Usage != nil {
				entry.Usage = &Usage{
					InputTokens:      rec.Usage.InputTokens,
					OutputTokens:     rec.Usage.OutputTokens,
					TotalTokens:      rec.Usage.TotalTokens,
					CachedTokens:     rec.Usage.CachedTokens,
					CacheWriteTokens: rec.Usage.CacheWriteTokens,
				}
			}
			entries = append(entries, entry)
		}
	}

	dropped := 0
	if len(entries) > limit {
		dropped = len(entries) - limit
		entries = entries[dropped:]
	}
	// Start where a run starts. A cap lands wherever the count falls, and a
	// transcript that opened with a tool's result would show an OUT whose card
	// was never drawn — or the tail of a reply attributed to a question that
	// scrolled off. A run begins with the user turn that started it, so the
	// window is moved forward to the first one it contains.
	if first := firstUserTurn(entries); first > 0 {
		dropped += first
		entries = entries[first:]
	}
	return TranscriptResult{Entries: entries, Dropped: dropped}
}

// firstUserTurn is the index of the first entry that begins a run, or 0 when
// there is none — a store with no user message has no boundary to align to, and
// returning what it has beats returning nothing.
func firstUserTurn(entries []TranscriptEntry) int {
	for i, entry := range entries {
		if entry.Kind == TranscriptMessage && entry.Role == "user" {
			return i
		}
	}
	return 0
}
