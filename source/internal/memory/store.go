// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package memory implements the cwd-local development memory store (0.21 plugin).
package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/redact"
)

const (
	DirName          = "memory"
	FileName         = "entries.jsonl"
	DefaultMaxText   = 8 << 10  // 8 KiB
	DefaultMaxEntries = 2000
	DefaultMaxBytes  = 8 << 20 // 8 MiB file
)

// Entry is one durable memory record.
type Entry struct {
	ID        string    `json:"id"`
	Key       string    `json:"key,omitempty"`
	Text      string    `json:"text"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Source    string    `json:"source,omitempty"` // agent|user|import
	Deleted   bool      `json:"deleted,omitempty"`
	Vector    []float32 `json:"vector,omitempty"`
}

// Caps limits store growth.
type Caps struct {
	MaxText    int
	MaxEntries int
	MaxBytes   int
}

func (c Caps) normalized() Caps {
	if c.MaxText <= 0 {
		c.MaxText = DefaultMaxText
	}
	if c.MaxEntries <= 0 {
		c.MaxEntries = DefaultMaxEntries
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	return c
}

// Store is a JSONL-backed memory database under {cwd}/.anvil/memory/.
type Store struct {
	cwd  string
	dir  string
	path string
	caps Caps
	mu   sync.Mutex
}

// Dir returns the absolute memory directory for cwd.
func Dir(cwd string) string {
	return filepath.Join(cwd, ".anvil", DirName)
}

// Open ensures the memory directory and returns a store bound to cwd.
func Open(cwd string, caps Caps) (*Store, error) {
	cwd = filepath.Clean(cwd)
	dir := Dir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{
		cwd:  cwd,
		dir:  dir,
		path: filepath.Join(dir, FileName),
		caps: caps.normalized(),
	}, nil
}

// Path returns the entries.jsonl path.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// WriteInput is memory_write args.
type WriteInput struct {
	Key    string
	Text   string
	Tags   []string
	Source string
	Vector []float32 // optional precomputed embedding
}

// Write upserts by key (if set) or appends a new id.
func (s *Store) Write(in WriteInput) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, err := sanitizeText(in.Text, s.caps.MaxText)
	if err != nil {
		return Entry{}, err
	}
	entries, err := s.loadAll()
	if err != nil {
		return Entry{}, err
	}
	now := time.Now().UTC()
	src := strings.TrimSpace(in.Source)
	if src == "" {
		src = "agent"
	}
	tags := normalizeTags(in.Tags)
	key := strings.TrimSpace(in.Key)

	var out Entry
	if key != "" {
		for i := range entries {
			if !entries[i].Deleted && entries[i].Key == key {
				entries[i].Text = text
				entries[i].Tags = tags
				entries[i].UpdatedAt = now
				entries[i].Source = src
				if len(in.Vector) > 0 {
					entries[i].Vector = in.Vector
				}
				out = entries[i]
				return out, s.rewrite(entries)
			}
		}
	}
	live := countLive(entries)
	if live >= s.caps.MaxEntries {
		return Entry{}, fmt.Errorf("memory store at max entries (%d)", s.caps.MaxEntries)
	}
	out = Entry{
		ID:        "mem_" + id.New(),
		Key:       key,
		Text:      text,
		Tags:      tags,
		CreatedAt: now,
		UpdatedAt: now,
		Source:    src,
		Vector:    in.Vector,
	}
	entries = append(entries, out)
	if err := s.appendLine(out); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// Read returns an entry by id or key.
func (s *Store) Read(idOrKey string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idOrKey = strings.TrimSpace(idOrKey)
	if idOrKey == "" {
		return Entry{}, fmt.Errorf("id or key is required")
	}
	entries, err := s.loadAll()
	if err != nil {
		return Entry{}, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Deleted {
			continue
		}
		if e.ID == idOrKey || (e.Key != "" && e.Key == idOrKey) {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("memory entry not found: %s", idOrKey)
}

// ListOpts filters memory_list.
type ListOpts struct {
	Limit int
	Tag   string
}

// List returns recent live entries (newest first).
func (s *Store) List(opts ListOpts) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}
	entries, err := s.loadAll()
	if err != nil {
		return nil, err
	}
	tag := strings.ToLower(strings.TrimSpace(opts.Tag))
	var out []Entry
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Deleted {
			continue
		}
		if tag != "" && !hasTag(e.Tags, tag) {
			continue
		}
		out = append(out, e)
		if len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

// Delete soft-deletes by id.
func (s *Store) Delete(entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return fmt.Errorf("id is required")
	}
	entries, err := s.loadAll()
	if err != nil {
		return err
	}
	found := false
	now := time.Now().UTC()
	for i := range entries {
		if entries[i].ID == entryID && !entries[i].Deleted {
			entries[i].Deleted = true
			entries[i].UpdatedAt = now
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("memory entry not found: %s", entryID)
	}
	return s.rewrite(entries)
}

func (s *Store) loadAll() ([]Entry, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), s.caps.MaxText*4+4096)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

func (s *Store) appendLine(e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	fi, err := os.Stat(s.path)
	size := int64(0)
	if err == nil {
		size = fi.Size()
	} else if !os.IsNotExist(err) {
		return err
	}
	if size+int64(len(raw)) > int64(s.caps.MaxBytes) {
		return fmt.Errorf("memory store exceeds max size (%d bytes)", s.caps.MaxBytes)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(raw)
	return err
}

func (s *Store) rewrite(entries []Entry) error {
	var b strings.Builder
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if b.Len() > s.caps.MaxBytes {
		return fmt.Errorf("memory store exceeds max size (%d bytes)", s.caps.MaxBytes)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func sanitizeText(text string, max int) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("text must be valid UTF-8")
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "begin private key") || strings.Contains(lower, "begin rsa private key") {
		return "", fmt.Errorf("refusing to store private key material")
	}
	out := redact.String(text)
	if utf8.RuneCountInString(out) > max {
		// truncate by runes
		r := []rune(out)
		out = string(r[:max]) + "…"
	}
	return out, nil
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func countLive(entries []Entry) int {
	n := 0
	for _, e := range entries {
		if !e.Deleted {
			n++
		}
	}
	return n
}

// AllLive returns non-deleted entries (caller must hold lock or use for tests via Search/List).
func (s *Store) allLiveUnlocked() ([]Entry, error) {
	entries, err := s.loadAll()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		if !e.Deleted {
			out = append(out, e)
		}
	}
	return out, nil
}
