// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package store implements the local append-only JSONL session store described
// in doc/design/06-session-store.md.
//
// Layout:
//
//	{cwd}/.anvil/agents/agent-<id>.jsonl
//
// Each line is one JSON record. Writes are append-only. Reads are crash-safe:
// a trailing partially written line (no terminating newline / invalid JSON) is
// ignored rather than failing the whole load.
//
// Secrets: the API key is never persisted. The meta record stores only the
// non-secret options snapshot (provider, model, cwd, limits).
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Record kinds.
const (
	TypeMeta     = "meta"
	TypeMessage  = "message"
	TypeSummary  = "summary" // compression marker (0.9+); prior messages are ignored on Resume
	TypeRunStart = "run_start"
	TypeRunEnd   = "run_end"
)

// ErrNotFound is returned by Load when the agent has no store file.
var ErrNotFound = errors.New("agent store not found")

// ToolCall mirrors a persisted model-requested tool invocation.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Usage is the persisted token accounting for a run.
type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
	// Cache counts are additive fields (0.43+): older files simply lack them,
	// and zero keeps its honest meaning — the provider reported nothing.
	CachedTokens     int `json:"cached_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Record is one JSONL line. Only the fields relevant to Type are populated.
type Record struct {
	Type string    `json:"type"`
	Time time.Time `json:"ts"`

	// meta
	AgentID    string `json:"agent_id,omitempty"`
	CWD        string `json:"cwd,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	MaxSteps   int    `json:"max_steps,omitempty"`
	AllowShell *bool  `json:"allow_shell,omitempty"`
	// Title is a name the user gave the session. It is written only by a rename,
	// as a new meta record, so the store stays append-only and the records that
	// came before it are untouched.
	Title string `json:"title,omitempty"`
	// Subagent identity (0.42+). A child agent records where it came from, so a
	// listing can nest it under its parent and a delete can take it away with the
	// conversation it belongs to. All empty on a top-level session.
	ParentAgentID    string `json:"parent_agent_id,omitempty"`
	ParentRunID      string `json:"parent_run_id,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
	Depth            int    `json:"depth,omitempty"`
	Kind             string `json:"kind,omitempty"`       // "subagent" for a child
	TaskTitle        string `json:"task_title,omitempty"` // the brief's description
	// Brief is the validated delegate brief a worker was given (0.50+), as JSON.
	Brief json.RawMessage `json:"brief,omitempty"`

	// message
	Role       string     `json:"role,omitempty"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`

	// run_start / run_end
	RunID  string `json:"run_id,omitempty"`
	Status string `json:"status,omitempty"`
	Steps  int    `json:"steps,omitempty"`
	Usage  *Usage `json:"usage,omitempty"`
	Err    string `json:"err,omitempty"`
	Code   string `json:"code,omitempty"`
}

// Store is a directory-backed JSONL session store.
type Store struct {
	dir string
}

// New returns a store rooted at {cwd}/.anvil/agents.
func New(cwd string) *Store {
	return &Store{dir: filepath.Join(cwd, ".anvil", "agents")}
}

// Dir returns the directory that holds the JSONL files.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Path returns the on-disk path for an agent id.
func (s *Store) Path(agentID string) string {
	return filepath.Join(s.dir, safeName(agentID)+".jsonl")
}

// Exists reports whether a store file exists for agentID.
func (s *Store) Exists(agentID string) bool {
	if s == nil {
		return false
	}
	_, err := os.Stat(s.Path(agentID))
	return err == nil
}

// Append writes one record as a single line. The directory is created lazily.
func (s *Store) Append(agentID string, rec Record) error {
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
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	f, err := os.OpenFile(s.Path(agentID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open store file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync record: %w", err)
	}
	return nil
}

// Delete removes the file that holds agentID's records.
//
// It is the one non-append operation the store offers, and it exists because a
// person asking to stop keeping a conversation is a request about the file, not
// about its contents: rewriting the records to hide them would keep the bytes
// the user asked to be rid of.
func (s *Store) Delete(agentID string) error {
	if s == nil {
		return errors.New("nil store")
	}
	if err := safeID(agentID); err != nil {
		return err
	}
	if err := os.Remove(s.Path(agentID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, agentID)
		}
		return fmt.Errorf("delete store file: %w", err)
	}
	return nil
}

// Load reads all valid records for agentID in order. A trailing partial or
// corrupt line is ignored (crash-safe tail). Missing file → ErrNotFound.
func (s *Store) Load(agentID string) ([]Record, error) {
	if s == nil {
		return nil, errors.New("nil store")
	}
	if err := safeID(agentID); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.Path(agentID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, agentID)
		}
		return nil, fmt.Errorf("read store file: %w", err)
	}
	var out []Record
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// Treat anything from here on as an unterminated / corrupt tail.
			_ = i
			break
		}
		out = append(out, rec)
	}
	return out, nil
}

// Summary is a rollup of one agent JSONL file for operators.
type Summary struct {
	ID         string
	CWD        string
	Provider   string
	Model      string
	Title      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastStatus string
	LastRunID  string
	Runs       int
	Usage      *Usage
	// Subagent identity, carried from the meta record so a listing can nest a
	// child under its parent without reading every transcript.
	ParentAgentID    string
	ParentRunID      string
	ParentToolCallID string
	Depth            int
	Kind             string
	TaskTitle        string
}

// ListIDs returns agent ids that have store files (filename without .jsonl).
func (s *Store) ListIDs() ([]string, error) {
	if s == nil {
		return nil, errors.New("nil store")
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		if id == "" || safeName(id) != id {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Summarize derives operator metadata from loaded records.
func Summarize(records []Record) Summary {
	var sum Summary
	var usage Usage
	hasUsage := false
	for _, rec := range records {
		if !rec.Time.IsZero() {
			if sum.CreatedAt.IsZero() || rec.Time.Before(sum.CreatedAt) {
				sum.CreatedAt = rec.Time
			}
			if sum.UpdatedAt.IsZero() || rec.Time.After(sum.UpdatedAt) {
				sum.UpdatedAt = rec.Time
			}
		}
		switch rec.Type {
		case TypeMeta:
			if sum.ID == "" {
				sum.ID = rec.AgentID
			}
			if sum.CWD == "" {
				sum.CWD = rec.CWD
			}
			if sum.Provider == "" {
				sum.Provider = rec.Provider
			}
			if sum.Model == "" {
				sum.Model = rec.Model
			}
			// A rename replaces whatever the session was called: the derived
			// title from the first message, or an earlier rename. Records are
			// chronological, so the last one written wins — which is what a
			// person expects from naming something twice.
			if rec.Title != "" {
				sum.Title = titleOf(rec.Title)
			}
			// Identity is written once, by Create. A later meta record (a rename)
			// carries none of it, so an empty field never erases what the first
			// record said.
			if rec.ParentAgentID != "" {
				sum.ParentAgentID = rec.ParentAgentID
			}
			if rec.ParentRunID != "" {
				sum.ParentRunID = rec.ParentRunID
			}
			if rec.ParentToolCallID != "" {
				sum.ParentToolCallID = rec.ParentToolCallID
			}
			if rec.Depth > 0 {
				sum.Depth = rec.Depth
			}
			if rec.Kind != "" {
				sum.Kind = rec.Kind
			}
			if rec.TaskTitle != "" {
				sum.TaskTitle = titleOf(rec.TaskTitle)
			}
		case TypeMessage:
			// The first thing a person typed is what the session is about. A
			// later message is a follow-up, not a better name, and a session
			// nobody prompted simply has no title.
			if sum.Title == "" && rec.Role == "user" {
				sum.Title = titleOf(rec.Content)
			}
		case TypeRunEnd:
			sum.Runs++
			sum.LastStatus = rec.Status
			sum.LastRunID = rec.RunID
			if rec.Usage != nil {
				hasUsage = true
				usage.InputTokens += rec.Usage.InputTokens
				usage.OutputTokens += rec.Usage.OutputTokens
				usage.TotalTokens += rec.Usage.TotalTokens
				usage.CachedTokens += rec.Usage.CachedTokens
				usage.CacheWriteTokens += rec.Usage.CacheWriteTokens
			}
		}
	}
	if hasUsage {
		u := usage
		sum.Usage = &u
	}
	return sum
}

// titleLimit is how much of a first message becomes a session's title. It is
// applied here rather than in a client because this is the one place every
// consumer reads a title from, and a prompt can be hundreds of kilobytes while
// a listing carries every session in the workspace.
const titleLimit = 80

// titleOf turns a prompt into a title: one line, no control characters, cut at
// titleLimit runes. Runes and not bytes, because a byte cut splits a multi-byte
// character — and it does so first for the people who write the fewest Latin
// characters, which is the worst possible way to get it wrong.
func titleOf(content string) string {
	out := make([]rune, 0, titleLimit)
	pendingSpace := false
	for _, r := range content {
		switch {
		case unicode.IsSpace(r):
			// Held rather than written, so a run of spaces and newlines — and a
			// trailing one — collapse to nothing instead of to a gap.
			pendingSpace = len(out) > 0
		case unicode.IsControl(r):
			// Dropped, not folded: a control character is not a break in the
			// text, it is a byte no sidebar can render.
		default:
			if pendingSpace {
				out = append(out, ' ')
				pendingSpace = false
			}
			if len(out) == titleLimit {
				return string(out) + "…"
			}
			out = append(out, r)
		}
	}
	return string(out)
}

// TitleWithSuffix appends suffix to a title and keeps the result within
// titleLimit runes.
//
// The room is made in the title, not in the listing: appending past the limit
// and letting the next Summarize cut the string would remove the suffix first,
// and a suffixed title exists precisely to stay distinguishable from the one it
// was made from.
func TitleWithSuffix(title, suffix string) string {
	if title == "" || suffix == "" {
		return title
	}
	base := []rune(title)
	room := titleLimit - len([]rune(suffix))
	if room < 0 {
		room = 0
	}
	if len(base) <= room {
		return title + suffix
	}
	return string(base[:room]) + suffix
}

func safeID(agentID string) error {
	if agentID == "" {
		return errors.New("agent id is required")
	}
	if safeName(agentID) != agentID {
		return fmt.Errorf("invalid agent id: %q", agentID)
	}
	return nil
}

func safeName(agentID string) string {
	return filepath.Base(filepath.Clean(filepath.ToSlash(agentID)))
}
