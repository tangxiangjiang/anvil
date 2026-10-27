// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import "testing"

// sameDecision compares decisions field by field: Decision carries a []string
// now (the protected paths), so it is no longer comparable as a value.
func sameDecision(a, b Decision) bool {
	if a.Matched != b.Matched || a.Effect != b.Effect || a.Source != b.Source ||
		a.Index != b.Index || a.Tool != b.Tool || a.Pattern != b.Pattern || a.Reason != b.Reason {
		return false
	}
	if len(a.Protected) != len(b.Protected) {
		return false
	}
	for i := range a.Protected {
		if a.Protected[i] != b.Protected[i] {
			return false
		}
	}
	return true
}

func mustParse(t *testing.T, text string) *Set {
	t.Helper()
	set, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("Parse(%s) = %v", text, err)
	}
	return set
}

// A nil set is what a workspace without a rule file has, and it must have no
// opinion rather than a default one.
func TestNilSetHasNoOpinion(t *testing.T) {
	var set *Set
	if set.Len() != 0 {
		t.Errorf("Len() = %d, want 0", set.Len())
	}
	for _, f := range []CallFacts{
		{Tool: "shell", Command: "rm -rf /"},
		{Tool: "write_file", Paths: []string{"a.txt"}},
		{Tool: "mcp_x"},
	} {
		if d := set.Evaluate(f, "linux"); d.Matched {
			t.Errorf("Evaluate(%+v) = %s, want no rule", f, d)
		}
	}
}

func TestEvaluatePrecedence(t *testing.T) {
	set := mustParse(t, `{"version":1,"rules":[
		{"effect":"allow","tool":"shell","pattern":"*"},
		{"effect":"ask","tool":"shell","pattern":"git push:*"},
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*","reason":"destructive"}
	]}`)

	cases := []struct {
		name    string
		command string
		want    Effect
		index   int
	}{
		{"allow", "ls -la", Allow, 0},
		{"ask outranks an earlier allow", "git push origin main", Ask, 1},
		{"deny outranks an earlier allow", "rm -rf /", Deny, 2},
	}
	for _, tc := range cases {
		d := set.Evaluate(CallFacts{Tool: "shell", Command: tc.command}, "linux")
		if !d.Matched || d.Effect != tc.want || d.Index != tc.index {
			t.Errorf("%s: Evaluate(%q) = %s, want %s at rule %d", tc.name, tc.command, d, tc.want, tc.index)
		}
	}
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf /"}, "linux"); d.Reason != "destructive" {
		t.Errorf("Decision.Reason = %q, want the rule's reason", d.Reason)
	}
}

// A deny later in the file still defeats a broader ask above it: effects are
// compared before file order is consulted, so ordering rules within one effect
// never has to be reasoned about across effects.
func TestDenyOutranksAsk(t *testing.T) {
	set := mustParse(t, `{"rules":[
		{"effect":"ask","tool":"shell","pattern":"git push:*"},
		{"effect":"deny","tool":"shell","pattern":"git push --force:*","reason":"force push"}
	]}`)
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "git push --force origin"}, "linux"); d.Effect != Deny || d.Index != 1 {
		t.Errorf("force push = %s, want deny at rule 1", d)
	}
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "git push origin main"}, "linux"); d.Effect != Ask || d.Index != 0 {
		t.Errorf("plain push = %s, want ask at rule 0", d)
	}
}

// Within one effect the first rule in the file wins, so a reader can find the
// deciding rule by reading down from the top.
func TestFirstRuleInEffectWins(t *testing.T) {
	set := mustParse(t, `{"rules":[
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*","reason":"first"},
		{"effect":"deny","tool":"shell","pattern":"rm:*","reason":"second"}
	]}`)
	d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf /"}, "linux")
	if d.Index != 0 || d.Reason != "first" {
		t.Errorf("Evaluate = %+v, want rule 0", d)
	}
}

func TestToolNameGlob(t *testing.T) {
	set := mustParse(t, `{"rules":[{"effect":"allow","tool":"mcp_*"}]}`)
	for _, tc := range []struct {
		tool string
		want bool
	}{
		{"mcp_fetch", true},
		{"mcp_", true},
		{"mcp", false},
		{"mcpish", false},
		{"shell", false},
	} {
		if got := set.Evaluate(CallFacts{Tool: tc.tool}, "linux").Matched; got != tc.want {
			t.Errorf("mcp_* against %q = %v, want %v", tc.tool, got, tc.want)
		}
	}
}

// An omitted pattern is the broad rule written down, not a wildcard that
// surprises: it matches the tool by name and nothing subtler.
func TestOmittedPatternMatchesByToolName(t *testing.T) {
	set := mustParse(t, `{"rules":[{"effect":"deny","tool":"shell"}]}`)
	if !set.Evaluate(CallFacts{Tool: "shell", Command: "ls"}, "linux").Matched {
		t.Error("a rule without a pattern did not match its tool")
	}
	if set.Evaluate(CallFacts{Tool: "read_file", Paths: []string{"a"}}, "linux").Matched {
		t.Error("a rule without a pattern matched another tool")
	}
}

// A call whose paths could not be determined must never be covered by a path
// rule: the rule would be claiming to have checked something nobody checked.
func TestUnknownPathsNeverMatchAPathRule(t *testing.T) {
	set := mustParse(t, `{"rules":[{"effect":"allow","tool":"write_file","pattern":"src/**"}]}`)
	if d := set.Evaluate(CallFacts{Tool: "write_file"}, "linux"); d.Matched {
		t.Errorf("Evaluate with no paths = %s, want no rule", d)
	}
}

func TestEvaluatePatchCoveringEveryPath(t *testing.T) {
	set := mustParse(t, `{"rules":[{"effect":"ask","tool":"apply_patch","pattern":"**/*.md"}]}`)
	inside := set.Evaluate(CallFacts{Tool: "apply_patch", Paths: []string{"a.md", "docs/b.md"}}, "linux")
	if inside.Effect != Ask {
		t.Errorf("patch of two markdown files = %s, want ask", inside)
	}
	outside := set.Evaluate(CallFacts{Tool: "apply_patch", Paths: []string{"a.md", "a.go"}}, "linux")
	if outside.Matched {
		t.Errorf("patch touching a .go file = %s, want no rule", outside)
	}
}

func TestDecisionStringNamesTheRule(t *testing.T) {
	set := mustParse(t, `{"rules":[{"effect":"deny","tool":"shell","pattern":"rm -rf:*"}]}`)
	d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf /"}, "linux")
	if got, want := d.String(), `deny shell "rm -rf:*"`; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := (Decision{}).String(), "no rule"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// The rule file in the design chapter is the one users will copy. It has to
// compile, and it has to mean what the chapter says it means.
func TestChapterExampleCompiles(t *testing.T) {
	set := mustParse(t, `{
		"version": 1,
		"rules": [
			{ "effect": "allow", "tool": "shell", "pattern": "readonly", "reason": "read-only commands" },
			{ "effect": "allow", "tool": "shell", "pattern": "git status:*" },
			{ "effect": "deny",  "tool": "shell", "pattern": "rm -rf:*", "reason": "destructive" },
			{ "effect": "allow", "tool": "write_file", "pattern": "src/**/*.go" },
			{ "effect": "ask",   "tool": "apply_patch", "pattern": "**/*.md" },
			{ "effect": "deny",  "tool": "mcp_*" }
		]
	}`)
	if set.Len() != 6 {
		t.Fatalf("Len() = %d, want 6", set.Len())
	}
	for _, tc := range []struct {
		name string
		f    CallFacts
		want Effect
	}{
		{"read-only command", CallFacts{Tool: "shell", Command: "git log --oneline -1"}, Allow},
		{"git status", CallFacts{Tool: "shell", Command: "git status --short"}, Allow},
		{"destructive", CallFacts{Tool: "shell", Command: "rm -rf build"}, Deny},
		{"unproven command", CallFacts{Tool: "shell", Command: "curl https://example.com"}, Ask},
		{"source file", CallFacts{Tool: "write_file", Paths: []string{"src/a/b.go"}}, Allow},
		{"markdown patch", CallFacts{Tool: "apply_patch", Paths: []string{"README.md"}}, Ask},
		{"mcp call", CallFacts{Tool: "mcp_fetch"}, Deny},
	} {
		d := set.Evaluate(tc.f, "linux")
		switch {
		case tc.want == Ask:
			// A command no rule matched is asked about because nothing matched,
			// which is the same outcome by a different route.
			if d.Matched && d.Effect != Ask {
				t.Errorf("%s: Evaluate = %s, want ask or no rule", tc.name, d)
			}
		case !d.Matched || d.Effect != tc.want:
			t.Errorf("%s: Evaluate = %s, want %s", tc.name, d, tc.want)
		}
	}
}

// Rules() hands the rules back as written, in the order they decide. The agent
// builds a session's policy by compiling the workspace file's rules, appending
// the caller's, and compiling the two together, so the round trip has to be
// exact: a dropped reason or a reordered pair would change which rule decides
// without changing a line of the file the user reads.
func TestRulesRoundTripIsExact(t *testing.T) {
	// Rules() now also hands back where each rule came from: these were compiled
	// by New, so they are the caller's own list, and their positions are counted
	// in it — zero-based here, reported one-based to a reader.
	want := []Rule{
		{Effect: "allow", Tool: "shell", Pattern: "readonly", Reason: "read-only commands", Source: SourceCode, Index: 1},
		{Effect: "allow", Tool: "shell", Pattern: "git status:*", Source: SourceCode, Index: 2},
		{Effect: "deny", Tool: "shell", Pattern: "rm -rf:*", Reason: "destructive", Source: SourceCode, Index: 3},
		{Effect: "ask", Tool: "apply_patch", Pattern: "**/*.md", Source: SourceCode, Index: 4},
		{Effect: "deny", Tool: "mcp_*", Source: SourceCode, Index: 5},
	}
	set, err := New(want)
	if err != nil {
		t.Fatalf("New = %v", err)
	}

	got := set.Rules()
	if len(got) != len(want) {
		t.Fatalf("Rules() has %d rules, want %d", len(got), len(want))
	}
	for i := range want {
		if !sameRule(got[i], want[i]) {
			t.Errorf("Rules()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	again, err := New(got)
	if err != nil {
		t.Fatalf("New(Rules()) = %v", err)
	}
	for _, f := range []CallFacts{
		{Tool: "shell", Command: "git log --oneline -1"},
		{Tool: "shell", Command: "git status --short"},
		{Tool: "shell", Command: "rm -rf build"},
		{Tool: "shell", Command: "curl https://example.com"},
		{Tool: "apply_patch", Paths: []string{"README.md"}},
		{Tool: "mcp_fetch"},
		{Tool: "write_file", Paths: []string{"a.go"}},
	} {
		if a, b := set.Evaluate(f, "linux"), again.Evaluate(f, "linux"); !sameDecision(a, b) {
			t.Errorf("Evaluate(%+v) = %+v, but %+v after a Rules() round trip", f, a, b)
		}
	}

	// A nil set is a workspace with no rules, and it hands back nothing to
	// concatenate rather than a panic or an empty rule.
	if got := (*Set)(nil).Rules(); got != nil {
		t.Errorf("nil set Rules() = %v, want nil", got)
	}
}

// sameRule compares rules field by field: Rule carries a slice (Unless), so it is not
// comparable as a value.
func sameRule(a, b Rule) bool {
	if a.Effect != b.Effect || a.Tool != b.Tool || a.Pattern != b.Pattern || a.Reason != b.Reason ||
		a.Source != b.Source || a.Index != b.Index || len(a.Unless) != len(b.Unless) {
		return false
	}
	for i := range a.Unless {
		if a.Unless[i] != b.Unless[i] {
			return false
		}
	}
	return true
}
