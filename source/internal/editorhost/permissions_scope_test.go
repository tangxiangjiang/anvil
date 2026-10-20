// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil"
)

// --- the user's own file (0.47) ----------------------------------------------

// writeUserRules plants the test's user rule file, creating its directory.
func writeUserRules(t *testing.T, body string) string {
	t.Helper()
	path, err := anvil.UserPermissionRulesPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// permissions.get reports the person's file beside the workspace's: one rules
// array in merge order, each rule carrying the file it came from and the place a
// reader counts in that file.
func TestPermissionsGetReportsBothScopes(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`)
	userPath := writeUserRules(t, `{"version":1,"protected":["secrets/**"],"rules":[
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*","reason":"危险删除"}
	]}`)

	res := resultOf(t, h.callOK(MethodPermissionsGet, nil))
	if got := res["user_path"]; got != userPath {
		t.Errorf("user_path = %v, want %v", got, userPath)
	}
	if exists, _ := res["user_exists"].(bool); !exists {
		t.Error("user_exists = false for a file that is there")
	}
	if protected, _ := res["protected"].([]any); len(protected) != 1 || protected[0] != "secrets/**" {
		t.Errorf("protected = %v, want the user file's glob", res["protected"])
	}
	if problem, ok := res["user_problem"]; ok && problem != nil {
		t.Errorf("a readable user file is not a problem: %v", problem)
	}

	rules, _ := res["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("rules = %v, want the user's and the workspace's", rules)
	}
	first, _ := rules[0].(map[string]any)
	if first["source"] != RuleSourceUser {
		t.Errorf("the user's rule must come first: %v", first)
	}
	if index, _ := first["index"].(float64); int(index) != 1 {
		t.Errorf("user rule index = %v, want 1", first["index"])
	}
	// The citation names the file a reader opens, not a position in the merged
	// list — where both rules would have competed for the same number.
	if d, _ := first["describe"].(string); !strings.Contains(d, "user permissions.json rule 1") {
		t.Errorf("describe = %q", d)
	}
	second, _ := rules[1].(map[string]any)
	if second["source"] != RuleSourceWorkspace {
		t.Errorf("the workspace's rule must come second: %v", second)
	}
	if index, _ := second["index"].(float64); int(index) != 1 {
		t.Errorf("workspace rule index = %v, want 1 in its own file", second["index"])
	}
	if d, _ := second["describe"].(string); !strings.Contains(d, ".anvil/permissions.json rule 1") {
		t.Errorf("describe = %q", d)
	}
}

// permissions.add names its target file, and the rule it writes is cited inside
// that file.
func TestPermissionsAddWritesTheUserFile(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())
	userPath, err := anvil.UserPermissionRulesPath()
	if err != nil {
		t.Fatal(err)
	}

	res := resultOf(t, h.callOK(MethodPermissionsAdd, PermissionsAddParams{
		Source: RuleSourceUser,
		Rule:   RuleSpec{Tool: "shell", Pattern: "git status:*"},
	}))
	if got := res["path"]; got != userPath {
		t.Errorf("path = %v, want the user file %s", got, userPath)
	}
	if got := res["source"]; got != RuleSourceUser {
		t.Errorf("source = %v, want user", got)
	}
	if index, _ := res["index"].(float64); int(index) != 1 {
		t.Errorf("index = %v, want 1", res["index"])
	}
	// The workspace file is not what was written.
	if _, err := os.Stat(anvil.PermissionRulesPath(dir)); !os.IsNotExist(err) {
		t.Errorf("an add to the user scope must not create the workspace file: %v", err)
	}
	data, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "git status:*") {
		t.Errorf("the user file does not hold the rule: %s", data)
	}
}

// permissions.set owns one layer per call. The user layer carries protected
// globs and may create its file when asked; the workspace layer keeps its own
// shape, and each refuses the other's field.
func TestPermissionsSetTargetsOneLayer(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	res := resultOf(t, h.callOK(MethodPermissionsSet, PermissionsSetParams{
		Source:    RuleSourceUser,
		Rules:     []RuleSpec{{Effect: "deny", Tool: "shell", Pattern: "rm -rf:*"}},
		Protected: []string{"secrets/**"},
		Create:    true,
	}))
	if exists, _ := res["user_exists"].(bool); !exists {
		t.Errorf("create should have written the user file: %v", res)
	}
	if protected, _ := res["protected"].([]any); len(protected) != 1 || protected[0] != "secrets/**" {
		t.Errorf("protected = %v", res["protected"])
	}

	// The user file has no starting mode: a save that sends one is refused
	// rather than silently dropping it.
	m := h.call(MethodPermissionsSet, PermissionsSetParams{
		Source: RuleSourceUser, DefaultMode: ModeAcceptEdits, Create: true,
	})
	if _, ok := rpcErrorOf(m); !ok {
		t.Fatalf("a user-layer save with default_mode must be refused: %v", m)
	}

	// A workspace file may not declare protected paths.
	m = h.call(MethodPermissionsSet, PermissionsSetParams{
		Rules: []RuleSpec{{Tool: "shell"}}, Protected: []string{"secrets/**"},
	})
	if _, ok := rpcErrorOf(m); !ok {
		t.Fatalf("a workspace-layer save with protected must be refused: %v", m)
	}

	// Unknown layers are refused, not guessed.
	m = h.call(MethodPermissionsSet, PermissionsSetParams{Source: "global"})
	if _, ok := rpcErrorOf(m); !ok {
		t.Fatalf("an unknown source must be refused: %v", m)
	}
}

// A malformed user file is a state, not a crash: get reports its place, the
// workspace rules still list, and neither an add nor a save replaces it.
func TestPermissionsGetReportsAMalformedUserFile(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`)
	broken := "{\n  \"rules\": [\n    {\"effect\": }\n  ]\n}\n"
	userPath := writeUserRules(t, broken)

	res := resultOf(t, h.callOK(MethodPermissionsGet, nil))
	if exists, _ := res["user_exists"].(bool); !exists {
		t.Error("user_exists = false for a file that is there")
	}
	problem, ok := res["user_problem"].(map[string]any)
	if !ok {
		t.Fatalf("a user file that will not compile is reported: %v", res)
	}
	if line, _ := problem["line"].(float64); int(line) == 0 {
		t.Errorf("user_problem carries no place: %v", problem)
	}
	// The readable layer is still shown; the broken one contributes no rules.
	if rules, _ := res["rules"].([]any); len(rules) != 1 {
		t.Errorf("rules = %v, want the workspace's one", rules)
	}

	m := h.call(MethodPermissionsAdd, PermissionsAddParams{
		Source: RuleSourceUser, Rule: RuleSpec{Tool: "shell"},
	})
	if _, ok := rpcErrorOf(m); !ok {
		t.Fatalf("an append to a malformed user file must be refused: %v", m)
	}
	m = h.call(MethodPermissionsSet, PermissionsSetParams{Source: RuleSourceUser, Create: true})
	if _, ok := rpcErrorOf(m); !ok {
		t.Fatalf("a save over a malformed user file must be refused: %v", m)
	}
	if got, _ := os.ReadFile(userPath); string(got) != broken {
		t.Fatalf("a refused write must leave the file as it was:\n%s", got)
	}
}

// A protected path is never covered by an allow rule: the card appears even
// though the file says "always allow", it says why, offers no "remember" that
// could never work, and the tool result carries the protected path.
func TestProtectedPathSkipsTheAllowRuleAndSaysSo(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":".git/config","content":"x"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, `{"version":1,"rules":[
		{"effect":"allow","tool":"write_file","pattern":"**","reason":"anything in the workspace"}
	]}`)
	sessionID := startSession(t, h, SessionParams{})

	var cards []map[string]any
	out := promptRun(t, h, sessionID, "write it", func(req map[string]any) map[string]any {
		cards = append(cards, req)
		return map[string]any{"decision": DecisionAllow}
	})
	if len(cards) != 1 {
		t.Fatalf("an allow rule must not cover .git: %d cards", len(cards))
	}
	if protected, _ := cards[0]["protected"].([]any); len(protected) != 1 || protected[0] != ".git/config" {
		t.Errorf("the card's protected = %v", cards[0]["protected"])
	}
	if remember := rememberKinds(t, cards[0]); len(remember) != 0 {
		t.Errorf("the card offers %v for a protected path, and none of them could decide it", remember)
	}

	results := toolResultsOf(t, out.events)
	if len(results) != 1 {
		t.Fatalf("want one tool result, got %v", results)
	}
	if protected, _ := results[0]["protected"].([]any); len(protected) != 1 || protected[0] != ".git/config" {
		t.Errorf("the tool result's protected = %v", results[0]["protected"])
	}
	// The rule did not decide the call, so the record must not claim it did.
	if _, ok := results[0]["rule"]; ok {
		t.Errorf("an allow that was skipped must not be reported as deciding: %v", results[0]["rule"])
	}
}
