// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemPromptIncludesRulesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("ALWAYS USE SNAKE_CASE"), 0o644)

	off, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	if strings.Contains(off.systemPrompt, "ALWAYS USE SNAKE_CASE") {
		t.Fatal("default should not load rules")
	}

	on, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir, LoadRules: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer on.Close()
	if !strings.Contains(on.systemPrompt, "ALWAYS USE SNAKE_CASE") {
		t.Fatalf("prompt=%q", on.systemPrompt)
	}
}
