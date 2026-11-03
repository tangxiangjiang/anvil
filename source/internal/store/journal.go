// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// JournalSchemaVersion is the only run-event payload contract this build can
// replay. A future version must be decoded explicitly rather than guessed.
const JournalSchemaVersion uint32 = 1

var (
	ErrJournalVersion  = errors.New("unsupported journal schema version")
	ErrJournalSequence = errors.New("invalid journal sequence")
)

// JournalEvent is one durable, ordered event for a run. Sequence numbers are
// scoped to RunID, start at one, and are never reused.
type JournalEvent struct {
	RunID string
	Seq   uint64
	Time  time.Time
	Event json.RawMessage
}

// JournalLimit bounds the durable operational replay for one run. Zero fields
// are unlimited. The newest event is always retained even when it alone is
// larger than MaxBytes, so sequence allocation can continue monotonically.
type JournalLimit struct {
	MaxEvents int
	MaxBytes  int64
}

// AppendRunEvent durably acknowledges one caller-allocated event sequence.
// Implementations reject duplicates, gaps, and sequences that do not start at
// one. A nil return means the event survived the backend's sync/commit boundary.
func (s *Store) AppendRunEvent(agentID, runID string, seq uint64, event json.RawMessage) error {
	return s.AppendRunEventLimited(agentID, runID, seq, event, JournalLimit{})
}

// AppendRunEventLimited appends and, when necessary, atomically replaces the
// file with a bounded journal tail plus an explicit retained-prefix marker.
func (s *Store) AppendRunEventLimited(agentID, runID string, seq uint64, event json.RawMessage, limit JournalLimit) error {
	if s == nil {
		return errors.New("nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.loadUnlocked(agentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := validateNextJournalSequence(recs, runID, seq); err != nil {
		return err
	}
	rec, err := journalRecord(runID, seq, event)
	if err != nil {
		return err
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now().UTC()
	}
	next := append(recs, rec)
	compacted, changed, err := compactJournalRecords(next, runID, limit)
	if err != nil {
		return err
	}
	if !changed {
		return s.appendUnlocked(agentID, rec)
	}
	return s.replaceUnlocked(agentID, compacted)
}

// LoadRunEvents validates the complete journal before returning any replay
// state. This prevents an unknown schema or interior sequence fault from
// partially mutating a caller's reconstructed run.
func (s *Store) LoadRunEvents(agentID, runID string) ([]JournalEvent, error) {
	if s == nil {
		return nil, errors.New("nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.loadUnlocked(agentID)
	if err != nil {
		return nil, err
	}
	return journalEvents(recs, runID)
}

func journalRecord(runID string, seq uint64, event json.RawMessage) (Record, error) {
	if runID == "" {
		return Record{}, fmt.Errorf("%w: run id is required", ErrJournalSequence)
	}
	if seq == 0 {
		return Record{}, fmt.Errorf("%w: sequence must start at 1", ErrJournalSequence)
	}
	if len(event) == 0 || !json.Valid(event) {
		return Record{}, errors.New("journal event must be valid JSON")
	}
	return Record{
		Type:           TypeRunEvent,
		RunID:          runID,
		RunSeq:         seq,
		JournalVersion: JournalSchemaVersion,
		Event:          append(json.RawMessage(nil), event...),
	}, nil
}

func validateNextJournalSequence(recs []Record, runID string, seq uint64) error {
	if runID == "" || seq == 0 {
		return fmt.Errorf("%w: run id and positive sequence are required", ErrJournalSequence)
	}
	events, err := journalEvents(recs, runID)
	if err != nil {
		return err
	}
	expected := uint64(1)
	if len(events) > 0 {
		expected = events[len(events)-1].Seq + 1
	}
	if seq != expected {
		return fmt.Errorf("%w: run %s got %d, want %d", ErrJournalSequence, runID, seq, expected)
	}
	return nil
}

func journalEvents(recs []Record, runID string) ([]JournalEvent, error) {
	if runID == "" {
		return nil, fmt.Errorf("%w: run id is required", ErrJournalSequence)
	}
	var out []JournalEvent
	expected := uint64(1)
	sawGap := false
	for i, rec := range recs {
		if rec.Type == TypeRunGap && rec.RunID == runID {
			if sawGap || len(out) > 0 || rec.JournalVersion != JournalSchemaVersion || rec.RunSeq <= 1 {
				return nil, fmt.Errorf("%w at record %d: invalid retained-prefix marker",
					ErrJournalSequence, i+1)
			}
			sawGap = true
			expected = rec.RunSeq
			continue
		}
		if rec.Type != TypeRunEvent || rec.RunID != runID {
			continue
		}
		if rec.JournalVersion != JournalSchemaVersion {
			return nil, fmt.Errorf("%w at record %d: got %d, want %d",
				ErrJournalVersion, i+1, rec.JournalVersion, JournalSchemaVersion)
		}
		if rec.RunSeq != expected {
			return nil, fmt.Errorf("%w at record %d: run %s got %d, want %d",
				ErrJournalSequence, i+1, runID, rec.RunSeq, expected)
		}
		if len(rec.Event) == 0 || !json.Valid(rec.Event) {
			return nil, fmt.Errorf("invalid journal event at record %d", i+1)
		}
		out = append(out, JournalEvent{
			RunID: rec.RunID,
			Seq:   rec.RunSeq,
			Time:  rec.Time,
			Event: append(json.RawMessage(nil), rec.Event...),
		})
		expected++
	}
	return out, nil
}

func compactJournalRecords(recs []Record, runID string, limit JournalLimit) ([]Record, bool, error) {
	events, err := journalEvents(recs, runID)
	if err != nil {
		return nil, false, err
	}
	if len(events) == 0 || (limit.MaxEvents <= 0 && limit.MaxBytes <= 0) {
		return recs, false, nil
	}
	start := len(events) - 1
	bytesKept := int64(len(events[start].Event))
	for start > 0 {
		nextBytes := bytesKept + int64(len(events[start-1].Event))
		nextCount := len(events) - start + 1
		if limit.MaxEvents > 0 && nextCount > limit.MaxEvents {
			break
		}
		if limit.MaxBytes > 0 && nextBytes > limit.MaxBytes {
			break
		}
		start--
		bytesKept = nextBytes
	}
	first := events[start].Seq
	hadGap := events[0].Seq > 1
	if start == 0 && !hadGap {
		return recs, false, nil
	}
	retained := make(map[uint64]struct{}, len(events)-start)
	for _, event := range events[start:] {
		retained[event.Seq] = struct{}{}
	}
	out := make([]Record, 0, len(recs)-start+1)
	markerWritten := false
	for _, rec := range recs {
		if rec.RunID == runID && rec.Type == TypeRunGap {
			continue
		}
		if rec.RunID == runID && rec.Type == TypeRunEvent {
			if _, keep := retained[rec.RunSeq]; !keep {
				continue
			}
			if !markerWritten && first > 1 {
				out = append(out, Record{
					Type:           TypeRunGap,
					Time:           rec.Time,
					RunID:          runID,
					RunSeq:         first,
					JournalVersion: JournalSchemaVersion,
				})
				markerWritten = true
			}
		}
		out = append(out, rec)
	}
	return out, true, nil
}

func (s *Store) replaceUnlocked(agentID string, recs []Record) (err error) {
	if err := safeID(agentID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	var data bytes.Buffer
	for _, rec := range recs {
		line, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode compacted record: %w", err)
		}
		data.Write(line)
		data.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(s.dir, ".journal-*.tmp")
	if err != nil {
		return fmt.Errorf("create compacted journal: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0o644); err != nil {
		return err
	}
	if _, err = tmp.Write(data.Bytes()); err != nil {
		return fmt.Errorf("write compacted journal: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync compacted journal: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close compacted journal: %w", err)
	}
	if err = os.Rename(tmpName, s.Path(agentID)); err != nil {
		return fmt.Errorf("replace compacted journal: %w", err)
	}
	return nil
}

// JournalHighWater validates every run-event journal before returning its
// per-run last sequence. Callers can then build replay projections without
// exposing partially reconstructed state when one record is incompatible.
func JournalHighWater(recs []Record) (map[string]uint64, error) {
	runIDs := make(map[string]struct{})
	for _, rec := range recs {
		if rec.Type == TypeRunEvent {
			if rec.RunID == "" {
				return nil, fmt.Errorf("%w: run event has no run id", ErrJournalSequence)
			}
			runIDs[rec.RunID] = struct{}{}
		}
	}
	highWater := make(map[string]uint64, len(runIDs))
	for runID := range runIDs {
		events, err := journalEvents(recs, runID)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			highWater[runID] = events[len(events)-1].Seq
		}
	}
	return highWater, nil
}
