// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"golang.org/x/crypto/bcrypt"
)

// Store is the SQLite auth database.
type Store struct {
	db  *sql.DB
	ttl time.Duration
}

// Open opens/creates the auth DB and migrates schema.
func Open(path string, sessionTTL time.Duration) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		// ok if Dir is .
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, ttl: sessionTTL}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
`)
	return err
}

// Close closes the DB.
func (s *Store) Close() error { return s.db.Close() }

// UserCount returns the number of users.
func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// NeedsSetup is true when no admin has been created yet.
func (s *Store) NeedsSetup() (bool, error) {
	n, err := s.UserCount()
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// EnsureAdmin creates bootstrap admin if no users exist and password is non-empty.
// Empty password is a no-op (use the install wizard instead).
func (s *Store) EnsureAdmin(username, password string) error {
	n, err := s.UserCount()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if password == "" {
		return nil
	}
	if username == "" {
		username = "admin"
	}
	_, err = s.CreateAdmin(username, password)
	return err
}

// CreateAdmin inserts the first admin. Fails if any user already exists.
func (s *Store) CreateAdmin(username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return User{}, fmt.Errorf("username and password are required")
	}
	if len(password) < 8 {
		return User{}, fmt.Errorf("password must be at least 8 characters")
	}
	n, err := s.UserCount()
	if err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, fmt.Errorf("setup already completed")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	res, err := s.db.Exec(
		`INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`,
		username, string(hash), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username}, nil
}

// User is a logged-in account.
type User struct {
	ID       int64
	Username string
}

// Login verifies password and issues a session token (plaintext once).
func (s *Store) Login(username, password string) (token string, user User, err error) {
	var id int64
	var hash string
	err = s.db.QueryRow(`SELECT id, password_hash FROM users WHERE username=?`, username).Scan(&id, &hash)
	if err != nil {
		return "", User{}, fmt.Errorf("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", User{}, fmt.Errorf("invalid credentials")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", User{}, err
	}
	token = hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	th := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	exp := now.Add(s.ttl)
	_, err = s.db.Exec(
		`INSERT INTO sessions(token_hash, user_id, expires_at, created_at) VALUES(?,?,?,?)`,
		th, id, exp.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return "", User{}, err
	}
	return token, User{ID: id, Username: username}, nil
}

// LookupSession validates a bearer token.
func (s *Store) LookupSession(token string) (User, error) {
	if token == "" {
		return User{}, fmt.Errorf("unauthorized")
	}
	sum := sha256.Sum256([]byte(token))
	th := hex.EncodeToString(sum[:])
	var userID int64
	var expStr string
	err := s.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token_hash=?`, th).Scan(&userID, &expStr)
	if err != nil {
		return User{}, fmt.Errorf("unauthorized")
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || time.Now().UTC().After(exp) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, th)
		return User{}, fmt.Errorf("unauthorized")
	}
	var username string
	if err := s.db.QueryRow(`SELECT username FROM users WHERE id=?`, userID).Scan(&username); err != nil {
		return User{}, fmt.Errorf("unauthorized")
	}
	return User{ID: userID, Username: username}, nil
}

// Logout deletes the session for token.
func (s *Store) Logout(token string) {
	if token == "" {
		return
	}
	sum := sha256.Sum256([]byte(token))
	th := hex.EncodeToString(sum[:])
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, th)
}
