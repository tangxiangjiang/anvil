// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"testing"
)

// The user file is read where a person's preferences live, which is also where
// a test keeps its own: $ANVIL_HOME/permissions.json.
func writeUserRules(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANVIL_HOME", home)
	writeWorkspaceFile(t, home, "permissions.json", body)
}

// A workspace rule that says allow cannot weaken a deny the person wrote for
// themselves: severity decides across the two files, not the order they load.
func TestUserDenyBeatsWorkspaceAllow(t *testing.T) {
	dir := t.TempDir()
	writeUserRules(t, `{"version":1,"rules":[
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*","reason":"no destructive commands"}
	]}`)
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", `{"version":1,"rules":[
		{"effect":"allow","tool":"shell","pattern":"rm -rf:*"}
	]}`)

	a, rec := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})

	res := execTool(a, "shell", map[string]any{"command": "rm -rf build"})
	if !res.IsError {
		t.Fatalf("the user's deny must hold over the workspace's allow: %+v", res)
	}
	// The citation names the file the person has to open, and the rule's own
	// place in it — not a position in the merged list.
	for _, want := range []string{"user permissions.json rule 1", "no destructive commands"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("deny message %q does not mention %q", res.Content, want)
		}
	}
	if rec.count() != 0 {
		t.Errorf("a deny decides without asking, got %d callbacks", rec.count())
	}
}

// A user allow is a rule like any other: it runs the call without a prompt, and
// the record says which file the allowance came from.
func TestUserAllowRunsWithoutAsking(t *testing.T) {
	dir := t.TempDir()
	writeUserRules(t, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`)

	a, rec := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})

	res := execTool(a, "shell", map[string]any{"command": "echo hi"})
	if res.IsError {
		t.Fatalf("the user's allow must run the call: %+v", res)
	}
	if rec.count() != 0 {
		t.Fatalf("an allow decides without asking, got %d callbacks", rec.count())
	}
	// Index is counted from zero in this type (the file's own naming is what
	// Describe quotes), so the first rule of the user file is index 0.
	if res.Rule == nil || res.Rule.Source != PermissionScopeUser || res.Rule.Index != 0 {
		t.Fatalf("rule = %+v, want the user file's first rule", res.Rule)
	}
}

// A protected path is one no allow rule may cover, so the call falls through to
// the callback even though a workspace rule says "always allow"; the result
// carries the path, which is how the record says why no rule decided it.
func TestAnAllowCannotCoverAProtectedPath(t *testing.T) {
	dir := t.TempDir()
	writeUserRules(t, `{"version":1,"rules":[]}`)
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", `{"version":1,"rules":[
		{"effect":"allow","tool":"write_file","pattern":"**"}
	]}`)

	a, rec := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})

	res := execTool(a, "write_file", map[string]any{"path": ".git/config", "content": "x"})
	if rec.count() != 1 {
		t.Fatalf("the mode must decide a protected path, got %d callbacks", rec.count())
	}
	last, _ := rec.last()
	if len(last.Protected) != 1 || last.Protected[0] != ".git/config" {
		t.Fatalf("the card's protected = %v", last.Protected)
	}
	if len(res.Protected) != 1 || res.Protected[0] != ".git/config" {
		t.Fatalf("the result's protected = %v", res.Protected)
	}
	if res.Rule != nil {
		t.Fatalf("an allow that was skipped must not be reported as deciding: %+v", res.Rule)
	}
}

// A user file that will not compile is as fatal as the workspace's at session
// start: a policy that cannot be read is not a policy of nothing, and a session
// that quietly ignored half of what the person wrote down would be worse than
// one that refuses to open.
func TestAMalformedUserFileFailsSessionCreation(t *testing.T) {
	dir := t.TempDir()
	writeUserRules(t, "{\n  \"rules\": [\n    {\"effect\": }\n  ]\n}\n")

	_, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir, LoadPermissions: true},
	})
	if err == nil {
		t.Fatal("a malformed user file must fail Create")
	}
	for _, want := range []string{"load user permissions", "permissions.json", "line 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}
