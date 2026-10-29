// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import "testing"

func unlessSet(t *testing.T, unless ...string) *Set {
	t.Helper()
	var rules []Rule
	for _, tool := range []string{"write_file", "edit_file", "apply_patch"} {
		rules = append(rules, Rule{Effect: "deny", Tool: tool, Unless: unless, Reason: "outside the brief"})
	}
	set, err := New(rules)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// An unless rule denies a write when any path it touches is outside the globs.
func TestUnlessDeniesOutsidePaths(t *testing.T) {
	set := unlessSet(t, "pkg/x/**", "docs/a.md")
	cases := []struct {
		name  string
		facts CallFacts
		deny  bool
	}{
		{"inside a directory", CallFacts{Tool: "edit_file", Paths: []string{"pkg/x/a.go"}}, false},
		{"inside nested", CallFacts{Tool: "write_file", Paths: []string{"pkg/x/deep/b.go"}}, false},
		{"the exact file", CallFacts{Tool: "edit_file", Paths: []string{"docs/a.md"}}, false},
		{"outside", CallFacts{Tool: "edit_file", Paths: []string{"README.md"}}, true},
		{"sibling of the file", CallFacts{Tool: "edit_file", Paths: []string{"docs/b.md"}}, true},
		{"parent escape", CallFacts{Tool: "edit_file", Paths: []string{"pkg/x/../../README.md"}}, true},
		{"windows separators", CallFacts{Tool: "edit_file", Paths: []string{`pkg\x\a.go`}}, false},
		{"one of two outside", CallFacts{Tool: "apply_patch", Paths: []string{"pkg/x/a.go", "README.md"}}, true},
		{"all inside", CallFacts{Tool: "apply_patch", Paths: []string{"pkg/x/a.go", "pkg/x/b.go"}}, false},
		// A write whose paths cannot be read must not slip past an allowlist.
		{"unknown paths fail closed", CallFacts{Tool: "apply_patch"}, true},
		// The rule is about writes: a read of the same outside path is not its business.
		{"other tools untouched", CallFacts{Tool: "read_file", Paths: []string{"README.md"}}, false},
	}
	for _, tc := range cases {
		d := set.Evaluate(tc.facts, "linux")
		if got := d.Matched && d.Effect == Deny; got != tc.deny {
			t.Errorf("%s: deny=%v, want %v (%+v)", tc.name, got, tc.deny, d)
		}
	}
}

// The refusal names the rule, so the model and a reader see why.
func TestUnlessDecisionNamesTheRule(t *testing.T) {
	d := unlessSet(t, "pkg/**").Evaluate(CallFacts{Tool: "edit_file", Paths: []string{"README.md"}}, "linux")
	if !d.Matched || d.Reason != "outside the brief" {
		t.Fatalf("%+v", d)
	}
	if got := d.Ref(); got == "" {
		t.Fatal("empty reference")
	}
}

// Unless exists for code rules only: a rule file that carries the key is refused, so a
// repository cannot ship a rule the user never meant to be grammar.
func TestUnlessIsRefusedInRuleFiles(t *testing.T) {
	_, err := Parse([]byte(`{"rules":[{"effect":"deny","tool":"edit_file","unless":["pkg/**"]}]}`))
	if err == nil {
		t.Fatal("the workspace file accepted unless")
	}
	if _, _, err := ParseUser([]byte(`{"rules":[{"effect":"deny","tool":"edit_file","unless":["pkg/**"]}]}`)); err == nil {
		t.Fatal("the user file accepted unless")
	}
}

// An allow can never carry unless: it would widen, and the allowlist is only ever a deny.
func TestUnlessOnlyOnDeny(t *testing.T) {
	if _, err := New([]Rule{{Effect: "allow", Tool: "edit_file", Unless: []string{"pkg/**"}}}); err == nil {
		t.Fatal("an allow rule with unless was accepted")
	}
	if _, err := New([]Rule{{Effect: "deny", Tool: "edit_file", Unless: []string{"pkg/**"}, Pattern: "x"}}); err == nil {
		t.Fatal("unless together with a pattern was accepted")
	}
	if _, err := New([]Rule{{Effect: "deny", Tool: "edit_file", Unless: []string{""}}}); err == nil {
		t.Fatal("an empty unless glob was accepted")
	}
}

// Deny beats allow wherever it sits, so a parent's rule order and a generated deny
// compose as the design says: the parent's deny still wins, and a brief cannot allow.
func TestUnlessComposesWithParentRules(t *testing.T) {
	parentDeny := Rule{Effect: "deny", Tool: "edit_file", Pattern: "secrets/**", Reason: "parent"}
	rules := []Rule{parentDeny}
	for _, tool := range []string{"write_file", "edit_file", "apply_patch"} {
		rules = append(rules, Rule{Effect: "deny", Tool: tool, Unless: []string{"secrets/**", "pkg/**"}, Reason: "brief"})
	}
	set, err := New(rules)
	if err != nil {
		t.Fatal(err)
	}
	// The brief lists secrets/, but the parent's own deny is the first deny and decides.
	d := set.Evaluate(CallFacts{Tool: "edit_file", Paths: []string{"secrets/k"}}, "linux")
	if !d.Matched || d.Effect != Deny || d.Reason != "parent" {
		t.Fatalf("the parent's deny did not win: %+v", d)
	}
}
