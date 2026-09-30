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
)

// Record kinds.
const (
	TypeMeta     = "meta"
	TypeMessage  = "message"
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
