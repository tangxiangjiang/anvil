// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- the rule file as a file -------------------------------------------------

func TestPermissionRulesMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	rules, err := LoadPermissionRules(dir)
	if err != nil || rules != nil {
		t.Fatalf("a workspace with no rule file has no rules and no error: %v %v", rules, err)
	}
	if got, want := PermissionRulesPath(dir), filepath.Join(dir, ".anvil", "permissions.json"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// A rule written by the editor's card has to be a rule the SDK accepts, or the
// button that wrote it would be promising something the next session refuses.
func TestAddPermissionRuleWritesARuleTheLoaderAccepts(t *testing.T) {
	dir := t.TempDir()
	added, err := AddPermissionRule(dir, PermissionRule{
		Effect: "allow", Tool: "shell", Pattern: "git status:*",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Pattern != "git status:*" {
		t.Fatalf("added = %+v", added)
	}
	// The file it wrote is a file the SDK loads: same text, same rules.
	data, err := os.ReadFile(PermissionRulesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPermissionRules(dir)
	if err != nil {
		t.Fatalf("the file it just wrote must load: %v", err)
	}
	if len(loaded) != 1 || loaded[0] != (PermissionRule{Effect: "allow", Tool: "shell", Pattern: "git status:*"}) {
		t.Fatalf("loaded = %+v from %s", loaded, data)
	}
	var f struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("the file is not JSON: %v\n%s", err, data)
	}
	if f.Version != 1 {
		t.Errorf("version = %d, want 1", f.Version)
	}
	// And it is a policy a session runs under.
	a, _ := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})
	res := execTool(a, "shell", map[string]any{"command": "git status --short"})
	if res.Rule == nil || res.Rule.Pattern != "git status:*" {
		t.Fatalf("the added rule must decide the call it was written for: %+v", res.Rule)
	}
}

func TestAddPermissionRuleAppendsAfterTheFile(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", `{"version":1,"rules":[
		{"effect":"allow","tool":"shell","pattern":"readonly","reason":"只读"},
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*"}
	]}`)
	added, err := AddPermissionRule(dir, PermissionRule{Effect: "allow", Tool: "shell", Pattern: "git push:*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 3 {
		t.Fatalf("added = %+v", added)
	}
	if added[0].Pattern != "readonly" || added[0].Reason != "只读" {
		t.Errorf("the file's own rules must survive an append, got %+v", added[0])
	}
	if added[2].Pattern != "git push:*" {
		t.Errorf("the new rule goes last, got %+v", added[2])
	}
	// Position is what a rule's identity is: the number the card and the denial
	// quote has to be the number a reader counts in the file.
	if m := EvaluatePermissionRules(added, "shell", shellArgs("git push origin main")); m == nil || m.Index != 2 {
		t.Fatalf("the appended rule is rule 3: %+v", m)
	}
}

// The one thing an append must never do: replace policy it could not read.
func TestAddPermissionRuleNeverClobbersAMalformedFile(t *testing.T) {
	dir := t.TempDir()
	broken := `{"version":1,"rules":[{"effect":"allow","tool":"shell",}]}`
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", broken)
	_, err := AddPermissionRule(dir, PermissionRule{Effect: "allow", Tool: "shell", Pattern: "ls:*"})
	if err == nil {
		t.Fatal("appending to a file that cannot be read must fail")
	}
	var perr *PermissionRuleError
	if !errors.As(err, &perr) {
		t.Fatalf("the error must name the file and the place: %T %v", err, err)
	}
	if perr.Path != PermissionRulesPath(dir) || perr.Line == 0 {
		t.Fatalf("error = %+v", perr)
	}
	onDisk, readErr := os.ReadFile(PermissionRulesPath(dir))
	if readErr != nil || string(onDisk) != broken {
		t.Fatalf("the file must be untouched:\n%s", onDisk)
	}
}

func TestAddPermissionRuleRefusesARuleTheLoaderWould(t *testing.T) {
	dir := t.TempDir()
	before := `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", before)
	_, err := AddPermissionRule(dir, PermissionRule{Effect: "maybe", Tool: "shell", Pattern: "ls:*"})
	if err == nil || !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("an unknown effect must be refused by name: %v", err)
	}
	onDisk, _ := os.ReadFile(PermissionRulesPath(dir))
	if string(onDisk) != before {
		t.Fatalf("a refused rule must not touch the file:\n%s", onDisk)
	}
}

// --- explaining a decision ---------------------------------------------------

func TestEvaluatePermissionRulesNamesTheDecidingRule(t *testing.T) {
	rules := []PermissionRule{
		{Effect: "allow", Tool: "shell", Pattern: "readonly", Reason: "只读命令"},
		{Effect: "deny", Tool: "shell", Pattern: "rm -rf:*", Reason: "危险删除"},
	}
	m := EvaluatePermissionRules(rules, "shell", shellArgs("rm -rf build"))
	if m == nil || m.Effect != "deny" || m.Index != 1 || m.Reason != "危险删除" {
		t.Fatalf("m = %+v", m)
	}
	// The wording is the one a denial uses, so the card and the model's error
	// agree on how to find the rule.
	for _, want := range []string{"permissions.json rule 2", `deny shell "rm -rf:*"`, "危险删除"} {
		if !strings.Contains(m.Describe(), want) {
			t.Errorf("Describe() = %q, missing %q", m.Describe(), want)
		}
	}
	if m := EvaluatePermissionRules(rules, "shell", shellArgs("git fetch")); m != nil {
		t.Errorf("no rule covers git fetch: %+v", m)
	}
	// A rule set that will not compile decides nothing, rather than pretending.
	if m := EvaluatePermissionRules([]PermissionRule{{Effect: "nope", Tool: "shell"}}, "shell", shellArgs("ls")); m != nil {
		t.Errorf("an invalid set must not claim a match: %+v", m)
	}
}

// --- what the card may offer -------------------------------------------------

// shellArgs is a shell call the way the registry receives it, so the tests ask
// the same question the card will: given this tool and these arguments, what may
// be offered.
func shellArgs(command string) json.RawMessage {
	data, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		panic(err)
	}
	return data
}

// twoFilesPatch is a patch that parses and touches two files, which is the shape
// a path rule cannot honestly cover.
const twoFilesPatch = "*** Begin Patch\n*** Add File: a.txt\n+x\n*** Add File: b.txt\n+y\n*** End Patch\n"

func patchArgs(t *testing.T, patch string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(map[string]string{"patch": patch})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPermissionRuleOptionsAreProvenAgainstTheCall(t *testing.T) {
	pats := func(opts []PermissionRuleOption) map[string]string {
		out := map[string]string{}
		for _, o := range opts {
			out[o.Kind] = o.Rule.Pattern
		}
		return out
	}
	cases := []struct {
		name string
		tool string
		args json.RawMessage
		want map[string]string
	}{
		{
			// The shape the milestone is about: exact, a prefix that names the
			// command and its subcommand, and the analyzer's read-only promise.
			name: "read-only command", tool: "shell", args: shellArgs("git status -s"),
			want: map[string]string{"exact": "git status -s", "prefix": "git status:*", "readonly": "readonly"},
		},
		{
			// A mutating command gets no read-only option, because the analyzer
			// refuses to prove it — which is the whole point of offering it.
			name: "destructive command", tool: "shell", args: shellArgs("rm -rf build"),
			want: map[string]string{"exact": "rm -rf build", "prefix": "rm:*"},
		},
		{
			// Two commands: no single-command rule covers the line, and a prefix
			// rule must never be allowed to mean "starts with git status".
			name: "compound command", tool: "shell", args: shellArgs("git status && rm -rf build"),
			want: map[string]string{},
		},
		{
			// Expansion is not lexable, so nothing exact or prefix-shaped is
			// offered; the analyzer will not prove it either.
			name: "expansion", tool: "shell", args: shellArgs("echo $(date)"),
			want: map[string]string{},
		},
		{
			// A redirection is a different command, and the matcher compares
			// literal tokens: nothing exact or prefix-shaped describes a line the
			// lexer refuses, and the analyzer will not call it read-only. Such a
			// call is simply asked about every time.
			name: "redirection", tool: "shell", args: shellArgs("ls > out.txt"),
			want: map[string]string{},
		},
		{
			name: "one file", tool: "write_file", args: json.RawMessage(`{"path":"a.txt"}`),
			want: map[string]string{"path": "a.txt"},
		},
		{
			// A patch touching several files is offered nothing: one glob covering
			// all of them would be broader than the files the user was shown.
			name: "several files", tool: "apply_patch", args: patchArgs(t, twoFilesPatch),
			want: map[string]string{},
		},
		{
			// An external tool with no facts can only be allowed as a whole.
			name: "factless tool", tool: "mcp__notes__search",
			want: map[string]string{"tool": ""},
		},
		{
			// …but not a mutating built-in whose facts were unavailable: "allow
			// every shell call" is what this table replaces.
			name: "shell with no preview", tool: "shell",
			want: map[string]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pats(PermissionRuleOptions(c.tool, c.args))
			if len(got) != len(c.want) {
				t.Fatalf("options = %+v, want %+v", got, c.want)
			}
			for kind, pattern := range c.want {
				if got[kind] != pattern {
					t.Errorf("kind %q = %q, want %q (all: %+v)", kind, got[kind], pattern, got)
				}
			}
		})
	}
}

// Every option is a rule that decides the call it was offered for. If one were
// not, the button would write a rule that does nothing.
func TestEveryOfferedOptionDecidesTheCall(t *testing.T) {
	for _, c := range []struct {
		tool string
		args json.RawMessage
	}{
		{"shell", shellArgs("git status -s")},
		{"shell", shellArgs("rm -rf build")},
		{"write_file", json.RawMessage(`{"path":"a.txt"}`)},
		{"mcp__notes__search", nil},
	} {
		for _, o := range PermissionRuleOptions(c.tool, c.args) {
			if m := EvaluatePermissionRules([]PermissionRule{o.Rule}, c.tool, c.args); m == nil {
				t.Errorf("option %+v does not decide %s %v", o, c.tool, c.args)
			}
		}
	}
}

// --- the file is the interface, while the session is open --------------------

// The card writes a rule mid-run, and the promise it makes is that the choice is
// in force now — not at the next restart. A file read once at session.new would
// make "always allow" mean "always allow, after you reload".
func TestAWrittenRuleDecidesTheNextCallInTheSameSession(t *testing.T) {
	dir := t.TempDir()
	a, rec := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})

	execTool(a, "shell", map[string]any{"command": "git status"})
	if rec.count() != 1 {
		t.Fatalf("with no rule file the gate asks: %v", rec.names())
	}
	if _, err := AddPermissionRule(dir, PermissionRule{
		Effect: "allow", Tool: "shell", Pattern: "git status:*",
	}); err != nil {
		t.Fatal(err)
	}
	res := execTool(a, "shell", map[string]any{"command": "git status --short"})
	if rec.count() != 1 {
		t.Fatalf("the rule just written must decide the next call: %v", rec.names())
	}
	if res.Rule == nil || res.Rule.Pattern != "git status:*" {
		t.Fatalf("rule = %+v", res.Rule)
	}
	// A command the new rule does not cover is still asked about.
	execTool(a, "shell", map[string]any{"command": "rm -rf build"})
	if rec.count() != 2 {
		t.Fatalf("a rule covers what it says and nothing else: %v", rec.names())
	}
}

// A policy file somebody is halfway through editing must not take the session
// down: the last policy that read cleanly stays in force until it reads again.
func TestAMalformedEditKeepsTheLastGoodPolicy(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, ".anvil/permissions.json",
		`{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"git status:*"}]}`)
	a, rec := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})

	if res := execTool(a, "shell", map[string]any{"command": "git status"}); res.Rule == nil {
		t.Fatal("the file's rule must be in force")
	}
	writeWorkspaceFile(t, dir, ".anvil/permissions.json", `{"version":1,"rules":[{`)
	if res := execTool(a, "shell", map[string]any{"command": "git status"}); res.Rule == nil {
		t.Fatalf("a half-saved file must not withdraw the policy in force: %+v", res)
	}
	if rec.count() != 0 {
		t.Fatalf("nobody was asked: %v", rec.names())
	}
	// Deleting the file is not a typo: the policy is gone, and the gate is back.
	if err := os.Remove(PermissionRulesPath(dir)); err != nil {
		t.Fatal(err)
	}
	execTool(a, "shell", map[string]any{"command": "git status"})
	if rec.count() != 1 {
		t.Fatalf("with the file gone the gate asks again: %v", rec.names())
	}
}


// A call that ran without asking has to be able to say why, or "it just ran" is
// indistinguishable from "nobody was watching".
func TestToolResultCarriesTheRuleThatAllowedIt(t *testing.T) {
	dir := t.TempDir()
	a, rec := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})
	writeWorkspaceFile(t, dir, ".anvil/permissions.json",
		`{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly","reason":"只读命令"}]}`)

	res := execTool(a, "shell", map[string]any{"command": "git status"})
	// The exit status is the command's business — a temp directory is not a
	// repository — and says nothing about whether the rule allowed it.
	if rec.count() != 0 {
		t.Errorf("an allowed call must not be asked about: %v", rec.names())
	}
	if res.Rule == nil || res.Rule.Effect != "allow" || res.Rule.Index != 0 || res.Rule.Pattern != "readonly" {
		t.Fatalf("rule = %+v, want the readonly rule", res.Rule)
	}
	// A call no rule decided carries none: the client must not invent one.
	if other := execTool(a, "shell", map[string]any{"command": "rm -rf build"}); other.Rule != nil {
		t.Fatalf("no rule decided rm -rf build, got %+v", other.Rule)
	}
}

// The same fact, from the other side: a prompt forced by a rule says which rule,
// so a card can explain why it appeared.
func TestPermissionRequestCarriesTheRuleThatAsked(t *testing.T) {
	dir := t.TempDir()
	a, rec := rulesAgent(t, dir, nil, LocalOptions{LoadPermissions: true})
	writeWorkspaceFile(t, dir, ".anvil/permissions.json",
		`{"version":1,"rules":[{"effect":"ask","tool":"read_file","reason":"问一下"}]}`)
	writeWorkspaceFile(t, dir, "a.txt", "hi")

	execTool(a, "read_file", map[string]any{"path": "a.txt"})
	got, ok := rec.last()
	if !ok {
		t.Fatal("the ask rule must have prompted")
	}
	if got.Rule == nil || got.Rule.Effect != "ask" || got.Rule.Reason != "问一下" {
		t.Fatalf("request rule = %+v", got.Rule)
	}
	// A prompt the mode gate raises on its own names no rule.
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()
	if res := execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "x"}); res.IsError {
		t.Fatalf("gated write with an allowing callback: %+v", res)
	}
	got, _ = rec.last()
	if got.Rule != nil {
		t.Fatalf("the gate asked, not a rule: %+v", got.Rule)
	}
}
