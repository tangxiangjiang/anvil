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

// writeWorkspaceFile writes a file under dir, creating any directory it needs.
func writeWorkspaceFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rulesAgent is gatedAgent's sibling: the same recorder, with rules.
func rulesAgent(t *testing.T, dir string, rules []PermissionRule, local LocalOptions) (*Agent, *permRecorder) {
	t.Helper()
	local.CWD = dir
	return gatedAgent(t, dir, Options{
		Local:           local,
		PermissionRules: rules,
	})
}

// --- deny holds without a callback -------------------------------------------

// The whole reason rules are decided in the registry: with no PermissionFunc
// installed there is nobody to ask, and a deny written down must hold anyway.
func TestRuleDenyHoldsWithNoCallback(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		PermissionRules: []PermissionRule{
			{Effect: "deny", Tool: "write_file", Pattern: "**/*.txt", Reason: "文本文件不许写"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	res := execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"})
	if !res.IsError {
		t.Fatalf("a deny must hold with no callback installed, got %+v", res)
	}
	// The model has to be able to read why, and to find the rule.
	for _, want := range []string{"write_file denied", "session rules rule 1", "文本文件不许写"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("deny message %q does not mention %q", res.Content, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Fatal("a denied write must not reach the disk")
	}
	// A path outside the rule's glob is untouched by it.
	if res := execTool(a, "write_file", map[string]any{"path": "a.md", "content": "hi"}); res.IsError {
		t.Fatalf("a path outside the glob must not be denied: %+v", res)
	}
}

// --- allow skips the prompt --------------------------------------------------

func TestRuleAllowRunsWithoutAsking(t *testing.T) {
	dir := t.TempDir()
	a, rec := rulesAgent(t, dir, []PermissionRule{
		{Effect: "allow", Tool: "shell", Pattern: "readonly"},
		{Effect: "allow", Tool: "write_file", Pattern: "src/**"},
	}, LocalOptions{})

	if res := execTool(a, "shell", map[string]any{"command": "echo ready"}); res.IsError {
		t.Fatalf("an allowed readonly command must run: %+v", res)
	}
	// `git status` fails outside a repository, which is not what is under test
	// here: the readonly rule covers the line, not the command's exit status.
	execTool(a, "shell", map[string]any{"command": "git status"})
	if got := rec.names(); len(got) != 0 {
		t.Fatalf("an allowed call must not reach the callback, got %v", got)
	}
	execTool(a, "write_file", map[string]any{"path": "src/a.go", "content": "package a"})
	if rec.count() != 0 {
		t.Fatalf("an allowed write must not reach the callback, got %v", rec.names())
	}
	// Anything the rules do not cover still goes to the callback.
	execTool(a, "shell", map[string]any{"command": "rm -rf build"})
	if got := rec.names(); len(got) != 1 || got[0] != "shell" {
		t.Fatalf("an uncovered call must still be asked about, got %v", got)
	}
}

// A prefix rule compares tokens, so it does not leak onto a compound command.
func TestRulePrefixDoesNotCoverACompoundCommand(t *testing.T) {
	dir := t.TempDir()
	a, rec := rulesAgent(t, dir, []PermissionRule{
		{Effect: "allow", Tool: "shell", Pattern: "git status:*"},
	}, LocalOptions{})

	execTool(a, "shell", map[string]any{"command": "git status --short"})
	if rec.count() != 0 {
		t.Fatalf("the prefix should cover `git status --short`, got %v", rec.names())
	}
	for _, cmd := range []string{"git status && echo hi", "git statusx"} {
		before := rec.count()
		execTool(a, "shell", map[string]any{"command": cmd})
		if rec.count() != before+1 {
			t.Errorf("%q must not be covered by `git status:*`", cmd)
		}
	}
}

// --- ask ---------------------------------------------------------------------

// An ask rule reaches tools the group gate would never have covered: read_file
// is ungated, and the rule makes it prompt anyway.
func TestRuleAskForcesAPromptForAnUngatedTool(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "a.md", "hello")
	a, rec := rulesAgent(t, dir, []PermissionRule{
		{Effect: "ask", Tool: "read_file"},
	}, LocalOptions{})

	if res := execTool(a, "read_file", map[string]any{"path": "a.md"}); res.IsError {
		t.Fatalf("%+v", res)
	}
	if got := rec.names(); len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("an ask rule must prompt for read_file, got %v", got)
	}
	execTool(a, "grep", map[string]any{"pattern": "hello"})
	if rec.count() != 1 {
		t.Fatalf("an ask rule must only cover the tool it names, got %v", rec.names())
	}
}

// A read carries no path fact, so a path rule never matches one — it is asked
// about by the tool-name rule or not at all. This is the "a rule matches only
// facts the card shows" boundary: the read's card names no target path, so there
// is no path for a rule to be written against. Widening reads to path rules
// means widening the card first.
func TestPathRuleDoesNotMatchARead(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "a.md", "hello")
	a, rec := rulesAgent(t, dir, []PermissionRule{
		{Effect: "ask", Tool: "read_file", Pattern: "**/*.md"},
	}, LocalOptions{})

	if res := execTool(a, "read_file", map[string]any{"path": "a.md"}); res.IsError {
		t.Fatalf("%+v", res)
	}
	if got := rec.names(); len(got) != 0 {
		t.Fatalf("a path rule must not decide a call whose paths are unknown, got %v", got)
	}
}

// A rule that asks with nobody to ask fails closed: it must never quietly
// become "run it".
func TestRuleAskWithoutACallbackDenies(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		PermissionRules: []PermissionRule{
			{Effect: "ask", Tool: "shell", Pattern: "*"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res := execTool(a, "shell", map[string]any{"command": "echo hi"})
	if !res.IsError || !strings.Contains(res.Content, "no approval channel") {
		t.Fatalf("an ask rule with no callback must deny, got %+v", res)
	}
}

// --- precedence --------------------------------------------------------------

// The file hooks are checked in preExec, above the rules, so a rule that allows
// a call cannot undo a deny written in .anvil/hooks.json.
func TestRuleAllowCannotUndoAHookDeny(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, ".anvil/hooks.json",
		`{"version":1,"beforeShell":[{"denyContains":"rm -rf","message":"hook denies rm -rf"}]}`)
	a, rec := rulesAgent(t, dir, []PermissionRule{
		{Effect: "allow", Tool: "shell", Pattern: "*"},
	}, LocalOptions{LoadHooks: true})

	res := execTool(a, "shell", map[string]any{"command": "rm -rf build"})
	if !res.IsError || !strings.Contains(res.Content, "hook denies rm -rf") {
		t.Fatalf("a hook deny must outrank a rule allow, got %+v", res)
	}
	if rec.count() != 0 {
		t.Fatalf("a hook deny decides before the callback, got %v", rec.names())
	}
	if res := execTool(a, "shell", map[string]any{"command": "echo hi"}); res.IsError {
		t.Fatalf("the hook must only deny what it names: %+v", res)
	}
}

// LoadHooks is a switch of its own, so a workspace file applies without asking
// for the project setting source — which neither client ever set.
func TestLoadHooksIsItsOwnSwitch(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, ".anvil/hooks.json",
		`{"version":1,"beforeShell":[{"denyContains":"rm -rf","message":"hook denies rm -rf"}]}`)

	for _, tc := range []struct {
		name string
		on   bool
		want bool
	}{
		{"off by default", false, false},
		{"on when asked", true, true},
	} {
		a, err := Create(context.Background(), Options{
			APIKey: "k", Model: Model{ID: "m"},
			Local: LocalOptions{CWD: dir, LoadHooks: tc.on},
		})
		if err != nil {
			t.Fatal(err)
		}
		res := execTool(a, "shell", map[string]any{"command": "rm -rf build"})
		if denied := res.IsError && strings.Contains(res.Content, "hook denies rm -rf"); denied != tc.want {
			t.Errorf("%s: denied = %v, want %v (%+v)", tc.name, denied, tc.want, res)
		}
		_ = a.Close()
	}
}

// The file's rules come first, so the order a reader sees is the order that
// decides.
func TestFileRulesPrecedeProgrammaticRules(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", `{"version":1,"rules":[
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*","reason":"from the file"}
	]}`)
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir, LoadPermissions: true},
		PermissionRules: []PermissionRule{
			{Effect: "deny", Tool: "shell", Pattern: "rm:*", Reason: "from the code"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res := execTool(a, "shell", map[string]any{"command": "rm -rf build"})
	if !strings.Contains(res.Content, "rule 1") || !strings.Contains(res.Content, "from the file") {
		t.Fatalf("the file's rule must be the one that decides, got %q", res.Content)
	}
}

// --- a path rule covers the whole call ---------------------------------------

func TestRulePathGlobMustCoverEveryPath(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "a.txt", "one\n")
	a, rec := rulesAgent(t, dir, []PermissionRule{
		{Effect: "allow", Tool: "apply_patch", Pattern: "**/*.txt"},
	}, LocalOptions{})

	inside := "*** Begin Patch\n*** Update File: a.txt\n@@\n-one\n+two\n*** End Patch\n"
	if res := execTool(a, "apply_patch", map[string]any{"patch": inside}); res.IsError {
		t.Fatalf("%+v", res)
	}
	if rec.count() != 0 {
		t.Fatalf("a patch the rule covers must not be asked about, got %v", rec.names())
	}

	// The same patch plus one file outside the glob: the rule no longer covers
	// the call, so it is asked about rather than allowed.
	both := "*** Begin Patch\n*** Update File: a.txt\n@@\n-one\n+two\n" +
		"*** Add File: b.go\n+package b\n*** End Patch\n"
	execTool(a, "apply_patch", map[string]any{"patch": both})
	if got := rec.names(); len(got) != 1 || got[0] != "apply_patch" {
		t.Fatalf("a patch touching a .go file must be asked about, got %v", got)
	}
}

// --- loading -----------------------------------------------------------------

// A malformed policy file fails session creation rather than being ignored.
func TestMalformedRuleFileFailsCreate(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", `{"version":1,"rules":[{"effect":"alow","tool":"shell"}]}`)
	_, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir, LoadPermissions: true},
	})
	if err == nil {
		t.Fatal("Create = nil, want an error for a malformed rule file")
	}
	for _, want := range []string{"permissions.json", "effect"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Create error %q does not mention %q", err, want)
		}
	}
}

// A file that exists but is not asked for is not read at all.
func TestRuleFileIgnoredUnlessAskedFor(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", `not json at all`)
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatalf("an unread policy file must not fail Create: %v", err)
	}
	defer a.Close()
	if res := execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"}); res.IsError {
		t.Fatalf("%+v", res)
	}
}

// With the switch on and no file present, nothing changes: this is the default
// path for every workspace that has never heard of rules.
func TestNoRuleFileChangesNothing(t *testing.T) {
	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{Local: LocalOptions{CWD: dir, LoadPermissions: true}})
	if res := execTool(a, "shell", map[string]any{"command": "echo hi"}); res.IsError {
		t.Fatalf("%+v", res)
	}
	if got := rec.names(); len(got) != 1 || got[0] != "shell" {
		t.Fatalf("with no rules the gate must decide as before, got %v", got)
	}
}
