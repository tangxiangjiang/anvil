package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
			continue
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
