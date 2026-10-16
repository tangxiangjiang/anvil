// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"os"
	"path/filepath"
	"testing"
)

// The code rules come after the file's, exactly as at build time: within one
// effect the first rule decides, so the file is the part a reader sees on top.
func TestSourceReadsTheFileAndAppendsCodeRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"git status:*"}]}`)
	initial, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := NewSource(dir, []Rule{{Effect: "deny", Tool: "shell", Pattern: "rm -rf:*"}}, initial)

	set, err := src.Read()
	if err != nil {
		t.Fatal(err)
	}
	if set.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", set.Len())
	}
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "git status"}, "linux"); d.Index != 0 || d.Effect != Allow {
		t.Errorf("the file's rule is rule 1: %+v", d)
	}
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf /"}, "linux"); d.Index != 1 || d.Effect != Deny {
		t.Errorf("the code rule is rule 2: %+v", d)
	}
}

// The file is the interface: an edit while a session is open is in force at the
// next call, not at the next session.
func TestSourceSeesAnEditWithoutRebuilding(t *testing.T) {
	dir := t.TempDir()
	src := NewSource(dir, nil, nil)
	if set, err := src.Read(); err != nil || set.Len() != 0 {
		t.Fatalf("Read = %v, %v, want no rules", set, err)
	}
	write(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`)
	set, err := src.Read()
	if err != nil {
		t.Fatal(err)
	}
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "ls"}, "linux"); d.Effect != Allow {
		t.Errorf("Evaluate = %+v, want the rule just written", d)
	}
}

// A file somebody is halfway through editing is reported and does not withdraw
// the policy already in force — refusing every call because of a typo punishes
// the wrong person.
func TestSourceFallsBackToTheLastGoodRead(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`)
	initial, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := NewSource(dir, nil, initial)

	write(t, dir, `{"version":1,"rules":[{`)
	set, err := src.Read()
	if err == nil {
		t.Fatal("Read = nil error, want the parse failure reported")
	}
	if set == nil || set.Len() != 1 {
		t.Fatalf("Read = %v, want the last set that read cleanly", set)
	}
	// Deleting the file is not a typo: the policy is gone, and so are the rules.
	if err := os.Remove(filepath.Join(dir, ".anvil", FileName)); err != nil {
		t.Fatal(err)
	}
	set, err = src.Read()
	if err != nil {
		t.Fatal(err)
	}
	if set.Len() != 0 {
		t.Fatalf("Len() = %d, want the file's removal to withdraw its rules", set.Len())
	}
}

// A nil source is what a caller has when it never asked for the file, and it
// must behave like no rules rather than like a panic.
func TestNilSourceReadsNothing(t *testing.T) {
	var src *Source
	set, err := src.Read()
	if err != nil || set != nil {
		t.Fatalf("Read = %v, %v, want nothing", set, err)
	}
}
