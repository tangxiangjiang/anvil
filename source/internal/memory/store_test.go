// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/memory"
)

func TestWriteReadListDelete(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir, memory.Caps{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := st.Write(memory.WriteInput{
		Key: "testing", Text: "Prefer table-driven tests in package foo", Tags: []string{"testing", "foo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.ID, "mem_") {
		t.Fatalf("id %q", e.ID)
	}
	got, err := st.Read(e.ID)
	if err != nil || got.Text != e.Text {
		t.Fatalf("%v %+v", err, got)
	}
	byKey, err := st.Read("testing")
	if err != nil || byKey.ID != e.ID {
		t.Fatalf("%v %+v", err, byKey)
	}
	list, err := st.List(memory.ListOpts{Limit: 10, Tag: "foo"})
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %+v", err, list)
	}
	if err := st.Delete(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read(e.ID); err == nil {
		t.Fatal("expected missing after delete")
	}
	rel, err := filepath.Rel(dir, st.Path())
	if err != nil || !strings.HasPrefix(filepath.ToSlash(rel), ".anvil/memory/") {
		t.Fatalf("path outside memory dir: %s (%v)", st.Path(), err)
	}
}

func TestRedactOnWriteAndRejectPrivateKey(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir, memory.Caps{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := st.Write(memory.WriteInput{Text: "token sk-abcdefghijklmnopqrstuvwxyz ok"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Text, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret not redacted: %q", e.Text)
	}
	if !strings.Contains(e.Text, "[REDACTED]") {
		t.Fatalf("expected redact placeholder: %q", e.Text)
	}
	_, err = st.Write(memory.WriteInput{Text: "-----BEGIN PRIVATE KEY-----\nMIIKsecret\n-----END PRIVATE KEY-----"})
	if err == nil {
		t.Fatal("expected reject private key")
	}
}

func TestSearchFindsSeededEntry(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir, memory.Caps{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Write(memory.WriteInput{
		Text: "Always run go test ./... before merge", Tags: []string{"ci"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.Write(memory.WriteInput{Text: "unrelated note about UI colors"})
	hits, err := st.Search(context.Background(), memory.SearchOpts{Query: "go test before merge", TopK: 5})
	if err != nil || len(hits) == 0 {
		t.Fatalf("%v %+v", err, hits)
	}
	if !strings.Contains(hits[0].Entry.Text, "go test") {
		t.Fatalf("unexpected top hit %+v", hits[0])
	}
}

func TestStoreOnlyUnderAnvilMemory(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir, memory.Caps{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.Write(memory.WriteInput{Text: "x"})
	entries := filepath.Join(dir, ".anvil", "memory", "entries.jsonl")
	if _, err := os.Stat(entries); err != nil {
		t.Fatal(err)
	}
}
