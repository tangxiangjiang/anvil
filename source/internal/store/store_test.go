// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestSummarizeTitlesASessionFromItsFirstUserMessage(t *testing.T) {
	recs := []store.Record{
		{Type: store.TypeMeta, AgentID: "agent-a"},
		{Type: store.TypeMessage, Role: "user", Content: "Fix the flaky test"},
		{Type: store.TypeMessage, Role: "assistant", Content: "Looking at it."},
		{Type: store.TypeMessage, Role: "user", Content: "and check the timeout too"},
	}
	// The first one is what the session is about; the second is a follow-up.
	if got := store.Summarize(recs).Title; got != "Fix the flaky test" {
		t.Fatalf("title=%q", got)
	}
}

func TestSummarizeMakesATitleOneLine(t *testing.T) {
	recs := []store.Record{
		{Type: store.TypeMessage, Role: "user", Content: "Fix the\n\tflaky  test \x07now\n"},
	}
	if got := store.Summarize(recs).Title; got != "Fix the flaky test now" {
		t.Fatalf("title=%q", got)
	}
}

func TestSummarizeTruncatesATitleAtEightyRunes(t *testing.T) {
	// Runes, not bytes. A byte cut splits a three-byte character, so the title
	// ends in mojibake — for exactly the people who write the least Latin text.
	recs := []store.Record{
		{Type: store.TypeMessage, Role: "user", Content: strings.Repeat("测", 100)},
	}
	title := store.Summarize(recs).Title
	if title != strings.Repeat("测", 80)+"…" {
		t.Fatalf("title=%q (%d runes)", title, len([]rune(title)))
	}
	if !utf8.ValidString(title) {
		t.Fatal("title is not valid UTF-8")
	}
}

func TestSummarizeDoesNotMarkATitleThatFits(t *testing.T) {
	// An ellipsis on a title that lost nothing claims something was dropped.
	exactly := strings.Repeat("测", 80)
	recs := []store.Record{{Type: store.TypeMessage, Role: "user", Content: exactly}}
	if got := store.Summarize(recs).Title; got != exactly {
		t.Fatalf("title=%q", got)
	}
}

func TestSummarizeHasNoTitleWithoutAUserMessage(t *testing.T) {
	recs := []store.Record{
		{Type: store.TypeMeta, AgentID: "agent-a"},
		{Type: store.TypeRunEnd, Status: "finished"},
	}
	if got := store.Summarize(recs).Title; got != "" {
		t.Fatalf("title=%q", got)
	}
}

func TestSummarizeHasNoTitleForAWhitespaceOnlyPrompt(t *testing.T) {
	recs := []store.Record{{Type: store.TypeMessage, Role: "user", Content: "  \n\t "}}
	if got := store.Summarize(recs).Title; got != "" {
		t.Fatalf("title=%q", got)
	}
}

func TestSummarizeDoesNotTitleASessionFromToolOutput(t *testing.T) {
	// A tool result carries file contents; a session named after one would name
	// the session after whatever the agent happened to read first.
	recs := []store.Record{
		{Type: store.TypeMessage, Role: "tool", Name: "read_file", Content: "package main"},
		{Type: store.TypeMessage, Role: "assistant", Content: "I looked."},
	}
	if got := store.Summarize(recs).Title; got != "" {
		t.Fatalf("title=%q", got)
	}
}
