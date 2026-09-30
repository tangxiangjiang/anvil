package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestAppendLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	id := "agent-testhistory01"

	if err := st.Append(id, store.Record{
		Type:    store.TypeMeta,
		AgentID: id,
		CWD:     dir,
		Model:   "m",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(id, store.Record{
		Type:    store.TypeMessage,
		Role:    "user",
		Content: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(id, store.Record{
		Type: store.TypeMessage,
		Role: "assistant",
		ToolCalls: []store.ToolCall{{
			ID:   "c1",
			Name: "read_file",
			Args: json.RawMessage(`{"path":"a.txt"}`),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(id, store.Record{
		Type:       store.TypeMessage,
		Role:       "tool",
		ToolCallID: "c1",
		Name:       "read_file",
		Content:    "file body",
	}); err != nil {
		t.Fatal(err)
	}

	recs, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 4 {
		t.Fatalf("got %d records", len(recs))
	}
	if recs[1].Content != "hello" || recs[3].Content != "file body" {
		t.Fatalf("%+v", recs)
	}

	// API key must never appear in the file.
	raw, err := os.ReadFile(st.Path(id))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-") || strings.Contains(strings.ToLower(string(raw)), "api_key") {
		t.Fatalf("store file looks like it contains secrets: %s", raw)
	}
}

func TestLoadMissing(t *testing.T) {
	st := store.New(t.TempDir())
	_, err := st.Load("agent-missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestLoadIgnoresCorruptTail(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	id := "agent-tail"
	if err := st.Append(id, store.Record{Type: store.TypeMeta, AgentID: id}); err != nil {
		t.Fatal(err)
	}
	path := st.Path(id)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"message","role":"user","content":"partial`)
	_ = f.Close()

	recs, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected only meta, got %d", len(recs))
	}
}

func TestRejectPathTraversalID(t *testing.T) {
	st := store.New(t.TempDir())
	err := st.Append("../evil", store.Record{Type: store.TypeMeta})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFileLivesUnderDotAnvil(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	id := "agent-abc"
	_ = st.Append(id, store.Record{Type: store.TypeMeta, AgentID: id})
	wantDir := filepath.Join(dir, ".anvil", "agents")
	if st.Dir() != wantDir {
		t.Fatalf("dir=%s want=%s", st.Dir(), wantDir)
	}
	if !st.Exists(id) {
		t.Fatal("expected exists")
	}
}
