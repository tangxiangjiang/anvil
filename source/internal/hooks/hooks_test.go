// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package hooks_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/hooks"
)

func TestLoadOldSchema(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version":1,"beforeShell":[{"denyContains":"rm -rf","message":"no"}]}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	// Load looks under .anvil/
	_ = os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "hooks.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	f, path, err := hooks.Load(dir)
	if err != nil || f == nil {
		t.Fatalf("%v %s", err, path)
	}
	deny, msg := f.CheckShell("rm -rf /", hooks.CheckOpts{})
	if !deny || msg != "no" {
		t.Fatalf("%v %q", deny, msg)
	}
}

func TestBeforeMCPDeny(t *testing.T) {
	f := &hooks.File{
		BeforeMCP: []hooks.Rule{{DenyContains: "secret_tool", Message: "mcp blocked"}},
	}
	deny, msg := f.CheckMCP("mcp_demo_secret_tool", "secret_tool", `{}`, hooks.CheckOpts{})
	if !deny || msg != "mcp blocked" {
		t.Fatalf("%v %q", deny, msg)
	}
	deny, _ = f.CheckMCP("mcp_demo_ok", "ok", `{}`, hooks.CheckOpts{})
	if deny {
		t.Fatal("expected allow")
	}
}

func TestSessionStartCap(t *testing.T) {
	big := strings.Repeat("x", 5000)
	f := &hooks.File{SessionStart: []hooks.SessionRule{{Message: big}}}
	notes := f.SessionStartNotes()
	if len([]rune(notes)) > 4001 { // cap + ellipsis
		t.Fatalf("len=%d", len([]rune(notes)))
	}
	if !strings.HasSuffix(notes, "…") {
		t.Fatalf("%q", notes[len(notes)-5:])
	}
}

func TestCommandHookDisabledIgnored(t *testing.T) {
	dir := t.TempDir()
	script := writeDenyScript(t, dir)
	f := &hooks.File{
		BeforeShell: []hooks.Rule{{Command: script, Message: "cmd deny"}},
	}
	deny, _ := f.CheckShell("echo hi", hooks.CheckOpts{CWD: dir, AllowCommands: false})
	if deny {
		t.Fatal("command hook must be ignored when AllowCommands=false")
	}
}

func TestCommandHookDenyWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	script := writeDenyScript(t, dir)
	f := &hooks.File{
		BeforeShell: []hooks.Rule{{Command: filepath.Base(script), Message: "cmd deny", TimeoutMs: 2000}},
	}
	deny, msg := f.CheckShell("echo hi", hooks.CheckOpts{CWD: dir, AllowCommands: true})
	if !deny || !strings.Contains(msg, "cmd deny") {
		t.Fatalf("%v %q", deny, msg)
	}
}

func writeDenyScript(t *testing.T, dir string) string {
	t.Helper()
	var path string
	var body string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "deny.bat")
		body = "@echo off\r\nexit /b 1\r\n"
	} else {
		path = filepath.Join(dir, "deny.sh")
		body = "#!/bin/sh\nexit 1\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
