// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestMemoryPluginAbsentByDefault(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range []string{"memory_write", "memory_read", "memory_search", "memory_list", "memory_delete"} {
		if toolPresent(a, name) {
			t.Fatalf("%s must be absent without Plugins", name)
		}
	}
}

func TestMemoryCrossAgentPersistence(t *testing.T) {
	dir := t.TempDir()
	a1, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:   LocalOptions{CWD: dir},
		Plugins: []string{"memory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a1.Close()
	if !toolPresent(a1, "memory_write") || !toolPresent(a1, "memory_search") {
		t.Fatal("memory tools missing")
	}
	res := a1.registry.Execute(context.Background(), "memory_write", mustJSON(map[string]any{
		"text": "Use table-driven tests in package bar",
		"key":  "bar-testing",
		"tags": []string{"testing"},
	}))
	if res.IsError {
		t.Fatalf("%+v", res)
	}
	id1 := a1.AgentID()
	_ = a1.Close()

	a2, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:   LocalOptions{CWD: dir},
		Plugins: []string{"memory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	if a2.AgentID() == id1 {
		t.Fatal("expected new agent id")
	}
	search := a2.registry.Execute(context.Background(), "memory_search", mustJSON(map[string]any{
		"query": "table-driven tests bar",
		"top_k": 5,
	}))
	if search.IsError || !strings.Contains(search.Content, "table-driven") {
		t.Fatalf("%+v", search)
	}
	read := a2.registry.Execute(context.Background(), "memory_read", mustJSON(map[string]any{
		"key": "bar-testing",
	}))
	if read.IsError || !strings.Contains(read.Content, "package bar") {
		t.Fatalf("%+v", read)
	}
}

func TestMemorySessionHintAndResumeUnaffected(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:   LocalOptions{CWD: dir},
		Plugins: []string{"memory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !strings.Contains(a.systemPrompt, "Memory tools are available") {
		t.Fatalf("missing session hint: %q", a.systemPrompt)
	}
	a.complete = scriptOnce{msg: llm.Message{Role: llm.RoleAssistant, Content: "hi"}}
	run, err := a.Send(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil || res.Status != StatusFinished {
		t.Fatalf("%v %+v", err, res)
	}
	id := a.AgentID()
	_ = a.Close()

	// Resume without memory plugin — core session still works; memory tools absent.
	a2, err := Resume(context.Background(), id, Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	if toolPresent(a2, "memory_write") {
		t.Fatal("memory tools should be absent without Plugins on Resume")
	}
	if len(a2.history) == 0 {
		t.Fatal("resume should restore history")
	}
}

func TestMemoryWriteRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir}, Plugins: []string{"memory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res := a.registry.Execute(context.Background(), "memory_write", mustJSON(map[string]any{
		"text": "key sk-abcdefghijklmnopqrstuvwxyz",
		"key":  "secret-note",
	}))
	if res.IsError {
		t.Fatalf("%+v", res)
	}
	read := a.registry.Execute(context.Background(), "memory_read", mustJSON(map[string]any{
		"key": "secret-note",
	}))
	if read.IsError || !strings.Contains(read.Content, "[REDACTED]") {
		t.Fatalf("expected redacted read %+v", read)
	}
}
