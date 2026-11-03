// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite is a SQLite-backed session store at {cwd}/.anvil/agents.db.
type SQLite struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) the agents database under cwd.
func OpenSQLite(cwd string) (*SQLite, error) {
	dir := filepath.Join(cwd, ".anvil")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create .anvil: %w", err)
	}
	path := filepath.Join(dir, "agents.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	_, err := s.db.Exec(`
PRAGMA synchronous = FULL;
CREATE TABLE IF NOT EXISTS records (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id TEXT NOT NULL,
  payload TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_records_agent ON records(agent_id);
`)
	return err
}

// Close closes the database.
func (s *SQLite) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// TryLock claims exclusive write ownership of one agent id.
//
// SQLite serialises writers inside one process already, but a second process
// (or a second Anvil host on the same workspace) is exactly the case that
// interleaves transcripts, so the same OS claim is taken here. The lock file
// goes beside the database.
func (s *SQLite) TryLock(agentID string) (*SessionLock, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("nil store")
	}
	var path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(new(int), new(string), &path); err != nil {
		return nil, fmt.Errorf("resolve agents.db path: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	return AcquireSessionLock(dir, agentID)
}

// Exists reports whether any records exist for agentID.
func (s *SQLite) Exists(agentID string) bool {
	if s == nil {
		return false
	}
	if err := safeID(agentID); err != nil {
		return false
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM records WHERE agent_id = ?`, agentID).Scan(&n)
	return err == nil && n > 0
}

// Append inserts one record.
func (s *SQLite) Append(agentID string, rec Record) error {
	if s == nil {
		return errors.New("nil store")
	}
	if err := safeID(agentID); err != nil {
		return err
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now().UTC()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO records(agent_id, payload) VALUES(?, ?)`, agentID, string(line))
	return err
}

// UpdateRecords performs the same append-only compare-and-swap transaction as
// the JSONL backend. All audit rows commit together or none become visible.
func (s *SQLite) UpdateRecords(agentID string, update func([]Record) ([]Record, error)) error {
	if s == nil || s.db == nil {
		return errors.New("nil store")
	}
	if update == nil {
		return errors.New("record update is required")
	}
	if err := safeID(agentID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin record update: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT payload FROM records WHERE agent_id = ? ORDER BY id ASC`, agentID)
	if err != nil {
		return fmt.Errorf("load record update: %w", err)
	}
	var recs []Record
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return err
		}
		var rec Record
		if err := json.Unmarshal([]byte(payload), &rec); err != nil {
			rows.Close()
			return fmt.Errorf("decode stored record: %w", err)
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	next, err := update(append([]Record(nil), recs...))
	if err != nil {
		return err
	}
	if len(next) < len(recs) || !reflect.DeepEqual(next[:len(recs)], recs) {
		return errors.New("record update must preserve the existing append-only prefix")
	}
	now := time.Now().UTC()
	for i := len(recs); i < len(next); i++ {
		if next[i].Time.IsZero() {
			next[i].Time = now
		}
		line, err := json.Marshal(next[i])
		if err != nil {
			return fmt.Errorf("encode updated record: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO records(agent_id, payload) VALUES(?, ?)`, agentID, string(line)); err != nil {
			return fmt.Errorf("insert updated record: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record update: %w", err)
	}
	return nil
}

// AppendRunEvent atomically validates and commits one caller-allocated event
// sequence. Transaction commit is the durable acknowledgement boundary.
func (s *SQLite) AppendRunEvent(agentID, runID string, seq uint64, event json.RawMessage) error {
	return s.AppendRunEventLimited(agentID, runID, seq, event, JournalLimit{})
}

// AppendRunEventLimited commits the new event and any required journal-tail
// compaction in one transaction.
func (s *SQLite) AppendRunEventLimited(agentID, runID string, seq uint64, event json.RawMessage, limit JournalLimit) error {
	if s == nil || s.db == nil {
		return errors.New("nil store")
	}
	if err := safeID(agentID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin journal append: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT payload FROM records WHERE agent_id = ? ORDER BY id ASC`, agentID)
	if err != nil {
		return fmt.Errorf("load journal: %w", err)
	}
	var recs []Record
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return err
		}
		var rec Record
		if err := json.Unmarshal([]byte(payload), &rec); err != nil {
			rows.Close()
			return fmt.Errorf("decode stored record: %w", err)
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
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
	compacted, changed, err := compactJournalRecords(append(recs, rec), runID, limit)
	if err != nil {
		return err
	}
	if changed {
		if _, err := tx.Exec(`DELETE FROM records WHERE agent_id = ?`, agentID); err != nil {
			return fmt.Errorf("replace compacted journal: %w", err)
		}
		for _, item := range compacted {
			line, err := json.Marshal(item)
			if err != nil {
				return fmt.Errorf("encode compacted record: %w", err)
			}
			if _, err := tx.Exec(`INSERT INTO records(agent_id, payload) VALUES(?, ?)`, agentID, string(line)); err != nil {
				return fmt.Errorf("insert compacted record: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit compacted journal: %w", err)
		}
		return nil
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO records(agent_id, payload) VALUES(?, ?)`, agentID, string(line)); err != nil {
		return fmt.Errorf("insert journal record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit journal record: %w", err)
	}
	return nil
}

// LoadRunEvents validates the complete journal before exposing replay state.
func (s *SQLite) LoadRunEvents(agentID, runID string) ([]JournalEvent, error) {
	recs, err := s.Load(agentID)
	if err != nil {
		return nil, err
	}
	return journalEvents(recs, runID)
}

// Delete removes every record for agentID.
func (s *SQLite) Delete(agentID string) error {
	if s == nil {
		return errors.New("nil store")
	}
	if err := safeID(agentID); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM records WHERE agent_id = ?`, agentID)
	if err != nil {
		return fmt.Errorf("delete records: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete records: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, agentID)
	}
	return nil
}

// Load returns records for agentID in insertion order.
func (s *SQLite) Load(agentID string) ([]Record, error) {
	if s == nil {
		return nil, errors.New("nil store")
	}
	if err := safeID(agentID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT payload FROM records WHERE agent_id = ? ORDER BY id ASC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var rec Record
		if err := json.Unmarshal([]byte(payload), &rec); err != nil {
			return nil, fmt.Errorf("decode stored record: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, agentID)
	}
	return out, nil
}

// ListIDs returns distinct agent ids.
func (s *SQLite) ListIDs() ([]string, error) {
	if s == nil {
		return nil, errors.New("nil store")
	}
	rows, err := s.db.Query(`SELECT DISTINCT agent_id FROM records ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
