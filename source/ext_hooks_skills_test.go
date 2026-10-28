// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func scriptExt() string {
	if runtime.GOOS == "windows" {
		return ".bat"
	}
	return ".sh"
}

func denyScriptBody() string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\nexit /b 1\r\n"
	}
	return "#!/bin/sh\nexit 1\n"
}

func TestBeforeMCPDeny(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755)
	hooksRaw := `{"version":1,"beforeMCP":[{"denyContains":"echo","message":"mcp blocked by fixture"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "hooks.json"), []byte(hooksRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildFakeMCP(t)
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local: LocalOptions{
			CWD:            dir,
			SettingSources: []SettingSource{SettingSourceProject},
		},
		MCPServers: map[string]MCPServerConfig{
			"fake": {Command: bin},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	var mcpName string
	for _, name := range a.registry.Names() {
		if strings.HasPrefix(name, "mcp_fake_") {
			mcpName = name
			break
		}
	}
	if mcpName == "" {
		t.Fatal("expected mcp tool")
	}
	res := a.registry.Execute(context.Background(), mcpName, mustJSON(map[string]any{}))
	if !res.IsError || !strings.Contains(res.Content, "mcp blocked by fixture") {
		t.Fatalf("%+v", res)
	}
}

func TestSessionStartInPrompt(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755)
	hooksRaw := `{"version":1,"sessionStart":[{"message":"SESSION_NOTE_UNIQUE_42"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "hooks.json"), []byte(hooksRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local: LocalOptions{
			CWD:            dir,
			SettingSources: []SettingSource{SettingSourceProject},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !strings.Contains(a.systemPrompt, "SESSION_NOTE_UNIQUE_42") {
		t.Fatalf("missing session note in %q", a.systemPrompt)
	}
}

func TestAlwaysSkillInPrompt(t *testing.T) {
	dir := t.TempDir()
	skDir := filepath.Join(dir, ".anvil", "skills", "commit-style")
	_ = os.MkdirAll(skDir, 0o755)
	body := "---\nname: commit-style\ndescription: commits\n---\nALWAYS_SKILL_BODY_99\n"
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
		Skills: []string{"commit-style"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !strings.Contains(a.systemPrompt, "ALWAYS_SKILL_BODY_99") {
		t.Fatalf("missing always skill: %q", a.systemPrompt)
	}
}

func TestListAndReadSkillTools(t *testing.T) {
	dir := t.TempDir()
	skDir := filepath.Join(dir, ".anvil", "skills", "demo")
	_ = os.MkdirAll(filepath.Join(skDir, "references"), 0o755)
	body := "---\nname: demo\ndescription: demo skill\n---\nSkill body here.\n"
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skDir, "references", "extra.md"), []byte("ref-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey:    "k",
		Model:     Model{ID: "m"},
		Local:     LocalOptions{CWD: dir, SettingSources: []SettingSource{SettingSourceProject}},
		SkillDirs: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	list := a.registry.Execute(context.Background(), "list_skills", mustJSON(map[string]any{}))
	if list.IsError || !strings.Contains(list.Content, "demo") {
		t.Fatalf("%+v", list)
	}
	read := a.registry.Execute(context.Background(), "read_skill", mustJSON(map[string]any{"id": "demo"}))
	if read.IsError || !strings.Contains(read.Content, "Skill body here") {
		t.Fatalf("%+v", read)
	}
	ref := a.registry.Execute(context.Background(), "read_skill", mustJSON(map[string]any{
		"id": "demo", "path": "references/extra.md",
	}))
	if ref.IsError || ref.Content != "ref-ok" {
		t.Fatalf("%+v", ref)
	}
	esc := a.registry.Execute(context.Background(), "read_skill", mustJSON(map[string]any{
		"id": "demo", "path": "../SKILL.md",
	}))
	// ../SKILL.md might resolve inside skill dir depending on path — use outside
	_ = os.WriteFile(filepath.Join(dir, "outside.txt"), []byte("nope"), 0o644)
	esc = a.registry.Execute(context.Background(), "read_skill", mustJSON(map[string]any{
		"id": "demo", "path": "../../outside.txt",
	}))
	if !esc.IsError {
		t.Fatalf("expected escape deny: %+v", esc)
	}
}

func TestCommandHookOptIn(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755)
	script := filepath.Join(dir, ".anvil", "deny_hook"+scriptExt())
	if err := os.WriteFile(script, []byte(denyScriptBody()), 0o755); err != nil {
		t.Fatal(err)
	}
	cmdRel := ".anvil/" + filepath.Base(script)
	hooksRaw := `{"version":1,"beforeShell":[{"command":"` + cmdRel + `","message":"cmd hook deny"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "hooks.json"), []byte(hooksRaw), 0o644); err != nil {
		t.Fatal(err)
	}

	aOff, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir, SettingSources: []SettingSource{SettingSourceProject}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer aOff.Close()
	res := aOff.registry.Execute(context.Background(), "shell", mustJSON(map[string]any{"command": "echo hi"}))
	if res.IsError && strings.Contains(res.Content, "cmd hook deny") {
		t.Fatalf("command hook should be off by default: %+v", res)
	}

	aOn, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:             LocalOptions{CWD: dir, SettingSources: []SettingSource{SettingSourceProject}},
		AllowHookCommands: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer aOn.Close()
	res = aOn.registry.Execute(context.Background(), "shell", mustJSON(map[string]any{"command": "echo hi"}))
	if !res.IsError || !strings.Contains(res.Content, "cmd hook deny") {
		t.Fatalf("expected deny when enabled: %+v", res)
	}
}
