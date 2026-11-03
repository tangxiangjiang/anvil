// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anvil-dev/anvil/internal/store"
)

const maxHistoryContextSide = 20
const defaultHistoryContextSide = 10

// SessionHistoryContextOptions selects a bounded window around a search hit.
// RecordPos is the one-based logical record position returned by SearchSessions.
type SessionHistoryContextOptions struct {
	Local     LocalOptions
	AgentID   string
	RecordPos uint64
	Before    int
	After     int
}

// SessionHistoryContextResult is a read-only window, not transcript pagination.
type SessionHistoryContextResult struct {
	Entries       []TranscriptEntry
	TargetIndex   int
	ClippedBefore bool
	ClippedAfter  bool
}

// SessionHistoryAnchorError means that a search result no longer names a
// record in the selected store. Callers must refresh rather than guess.
type SessionHistoryAnchorError struct {
	AgentID   string
	RecordPos uint64
}

func (e *SessionHistoryAnchorError) Error() string {
	return fmt.Sprintf("history anchor expired: %s record %d", e.AgentID, e.RecordPos)
}

// SessionHistoryContext loads at most 41 renderable entries around one search
// hit. It never changes the normal transcript tail or resumes the agent.
func SessionHistoryContext(
	ctx context.Context,
	opts SessionHistoryContextOptions,
) (SessionHistoryContextResult, error) {
	if opts.AgentID == "" || opts.RecordPos == 0 {
		return SessionHistoryContextResult{}, &SessionHistoryAnchorError{
			AgentID: opts.AgentID, RecordPos: opts.RecordPos,
		}
	}
	if err := ctx.Err(); err != nil {
		return SessionHistoryContextResult{}, err
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return SessionHistoryContextResult{}, err
	}
	backend, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return SessionHistoryContextResult{}, newError("open store", false, err)
	}
	defer backend.Close()
	records, err := backend.Load(opts.AgentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SessionHistoryContextResult{}, &SessionHistoryAnchorError{
				AgentID: opts.AgentID, RecordPos: opts.RecordPos,
			}
		}
		return SessionHistoryContextResult{}, newError("load agent store", false, err)
	}
	if opts.RecordPos > uint64(len(records)) {
		return SessionHistoryContextResult{}, &SessionHistoryAnchorError{
			AgentID: opts.AgentID, RecordPos: opts.RecordPos,
		}
	}

	entries := transcriptFromRecords(records, len(records)+1).Entries
	positions := transcriptEntryRecordPositions(records)
	if len(entries) == 0 || len(entries) != len(positions) {
		return SessionHistoryContextResult{}, &SessionHistoryAnchorError{
			AgentID: opts.AgentID, RecordPos: opts.RecordPos,
		}
	}
	target := nearestTranscriptEntry(positions, opts.RecordPos)
	before := boundedHistorySide(opts.Before)
	after := boundedHistorySide(opts.After)
	start := max(0, target-before)
	end := min(len(entries), target+after+1)
	return SessionHistoryContextResult{
		Entries:       entries[start:end],
		TargetIndex:   target - start,
		ClippedBefore: start > 0,
		ClippedAfter:  end < len(entries),
	}, nil
}

func boundedHistorySide(value int) int {
	if value <= 0 {
		return defaultHistoryContextSide
	}
	return min(value, maxHistoryContextSide)
}

func nearestTranscriptEntry(positions []uint64, target uint64) int {
	best := 0
	bestDistance := absoluteRecordDistance(positions[0], target)
	for i := 1; i < len(positions); i++ {
		distance := absoluteRecordDistance(positions[i], target)
		if distance < bestDistance {
			best, bestDistance = i, distance
		}
	}
	return best
}

func absoluteRecordDistance(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}

// transcriptEntryRecordPositions mirrors transcriptFromRecords' additive
// projection. Run-start and durable state records affect replay but do not draw
// an entry; plan hits therefore anchor to the nearest visible conversation row.
func transcriptEntryRecordPositions(records []store.Record) []uint64 {
	positions := make([]uint64, 0, len(records))
	for index, record := range records {
		switch record.Type {
		case store.TypeMessage, store.TypeSummary, store.TypeRunEnd,
			store.TypeRetry, store.TypeTruncated:
			positions = append(positions, uint64(index+1))
		case store.TypeStage:
			var stage StageEvent
			if len(record.Stage) > 0 && json.Unmarshal(record.Stage, &stage) == nil {
				positions = append(positions, uint64(index+1))
			}
		}
	}
	return positions
}
