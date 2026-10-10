// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schemaVersion = "1"

// DB is a project search index at {cwd}/.anvil/index/search.db.
type DB struct {
	cwd string
	db  *sql.DB
}

// Open opens or creates the index database under cwd.
func Open(cwd string) (*DB, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(abs, ".anvil", "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create index dir: %w", err)
	}
	path := filepath.Join(dir, "search.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, _ = db.Exec(`PRAGMA foreign_keys=ON`)
	_, _ = db.Exec(`PRAGMA busy_timeout=5000`)
	idx := &DB{cwd: abs, db: db}
	if err := idx.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

// Close closes the database.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// Path returns the search.db path.
func (d *DB) Path() string {
	return filepath.Join(d.cwd, ".anvil", "index", "search.db")
}

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS files(
			path TEXT PRIMARY KEY,
			mtime INTEGER NOT NULL,
			hash TEXT NOT NULL,
			lang TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS chunks(
			id INTEGER PRIMARY KEY,
			uid TEXT NOT NULL UNIQUE,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			hash TEXT NOT NULL,
			text TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_path ON chunks(path)`,
		`CREATE TABLE IF NOT EXISTS chunk_vec(
			chunk_id INTEGER PRIMARY KEY,
			embedding BLOB NOT NULL,
			FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
		)`,
	}
	for _, s := range stmts {
		if _, err := d.db.Exec(s); err != nil {
			return err
		}
	}
	// FTS5 table (may already exist).
	_, err := d.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
		uid UNINDEXED,
		path UNINDEXED,
		start_line UNINDEXED,
		end_line UNINDEXED,
		text,
		tokenize = 'porter unicode61'
	)`)
	if err != nil {
		return fmt.Errorf("fts5: %w", err)
	}
	var ver string
	_ = d.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&ver)
	if ver == "" {
		_, _ = d.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version', ?)`, schemaVersion)
	}
	return nil
}

func (d *DB) setMeta(key, value string) error {
	_, err := d.db.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES(?,?)`, key, value)
	return err
}

func (d *DB) getMeta(key string) string {
	var v string
	_ = d.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return v
}
