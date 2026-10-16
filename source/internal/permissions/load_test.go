// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A workspace need not have a rule file, and its absence must not look like an
// empty policy: the caller carries nil and behaves exactly as it did before
// rules existed.
func TestLoadMissingFileIsNoRules(t *testing.T) {
	set, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load = %v, want no error for a missing file", err)
	}
	if set != nil {
		t.Fatalf("Load = %v, want nil for a missing file", set)
	}
}

func TestLoadReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"version":1,"rules":[{"effect":"deny","tool":"shell","pattern":"rm -rf:*"}]}`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if set.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", set.Len())
	}
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf /"}, "linux"); d.Effect != Deny {
		t.Errorf("Evaluate = %s, want deny", d)
	}
}

// --- the declared starting mode ----------------------------------------------
//
// A file may say which mode its workspace's sessions start in. This package
// checks the shape only: which words are modes is the host's vocabulary, and a
// second copy of that list living down here is how the two start disagreeing.

func TestParseReadsTheDeclaredMode(t *testing.T) {
	set, err := Parse([]byte(`{"version":1,"defaultMode":"accept_edits","rules":[]}`))
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if got := set.DefaultMode(); got != "accept_edits" {
		t.Fatalf("DefaultMode() = %q, want accept_edits", got)
	}
}

// A file that declares nothing, and rules that a program supplied, both have no
// declaration — and neither is an error.
func TestNoDeclarationReadsAsEmpty(t *testing.T) {
	set, err := Parse([]byte(`{"version":1,"rules":[]}`))
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if got := set.DefaultMode(); got != "" {
		t.Fatalf("DefaultMode() = %q, want empty", got)
	}
	programmatic, err := New([]Rule{{Effect: "allow", Tool: "shell"}})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	if got := programmatic.DefaultMode(); got != "" {
		t.Fatalf("DefaultMode() = %q for rules a program supplied, want empty", got)
	}
	var nilSet *Set
	if got := nilSet.DefaultMode(); got != "" {
		t.Fatalf("DefaultMode() = %q on a nil set, want empty", got)
	}
}

// A word this package cannot read as a mode name is a problem on the file, with
// the line to fix — not a policy the host has to refuse later. An empty value is
// in this group on purpose: a mode name is a word, so `""` is a typo rather than
// a declaration that says nothing.
func TestMalformedDefaultModePointsAtTheLine(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"capitalised", "Bypass"},
		{"a phrase", "accept edits"},
		{"empty", ""},
		{"leading digit", "3rd_mode"},
		{"punctuation", "accept-edits"},
		{"padded", " accept_edits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := "{\n  \"version\": 1,\n  \"defaultMode\": " + strconv.Quote(tc.value) + ",\n  \"rules\": []\n}\n"
			_, err := Parse([]byte(text))
			if err == nil {
				t.Fatalf("Parse(%s) = nil, want an error", text)
			}
			if !strings.Contains(err.Error(), "defaultMode") {
				t.Errorf("error %q does not name the key", err)
			}
			line, col := Position(err)
			// The key's own position, so the squiggle lands on the declaration
			// rather than on the file: line 3, which begins with two spaces.
			if line != 3 || col != 3 {
				t.Errorf("Position = (%d, %d), want (3, 3)", line, col)
			}
		})
	}
}

// A word that is shaped like a mode but is not one passes here and is refused by
// the host: the two checks are deliberately in different packages, and this is
// what keeps the boundary where the design put it.
func TestUnknownWordIsStillAWellFormedDeclaration(t *testing.T) {
	set, err := Parse([]byte(`{"version":1,"defaultMode":"yolo","rules":[]}`))
	if err != nil {
		t.Fatalf("Parse = %v, want a bare lowercase word to be read", err)
	}
	if got := set.DefaultMode(); got != "yolo" {
		t.Fatalf("DefaultMode() = %q, want yolo", got)
	}
}

// A malformed file is fatal, and the error has to say where: the whole reason to
// refuse is that the user can then fix it.
func TestLoadMalformedFileNamesThePlace(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "{\n  \"version\": 1,\n  \"rules\": [\n    {\"effect\": \"allow\", \"tool\": \"shell\",}\n  ]\n}\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load = nil, want an error for a malformed file")
	}
	if !strings.Contains(err.Error(), "permissions.json") {
		t.Errorf("error %q does not name the file", err)
	}
	if !strings.Contains(err.Error(), "line ") {
		t.Errorf("error %q does not name a line", err)
	}
}

// A misspelled key is a rule the user believes is in force and is not, so it is
// refused rather than ignored.
func TestParseRefusesUnknownFields(t *testing.T) {
	_, err := Parse([]byte(`{"rules":[{"effect":"allow","tool":"shell","patern":"ls"}]}`))
	if err == nil {
		t.Fatal("Parse = nil, want an error for an unknown field")
	}
	if !strings.Contains(err.Error(), "patern") {
		t.Errorf("error %q does not name the unknown field", err)
	}
}

// The position is what a client turns into a squiggle, so it has to survive the
// wrapping Load does and be absent — not zero-valued-looking — for a problem
// that belongs to no place in the text.
func TestPositionOfAParseError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "{\n  \"rules\": [\n    {\"effect\": \"allow\", \"tool\": \"shell\",}\n  ]\n}\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("want an error")
	}
	line, col := Position(err)
	if line != 3 || col <= 0 {
		t.Fatalf("Position = %d,%d, want line 3", line, col)
	}

	// An unknown field carries no offset — encoding/json does not report one —
	// and the honest position for it is none.
	_, err = Parse([]byte(`{"rules":[{"effect":"allow","tool":"shell","patern":"ls"}]}`))
	if err == nil {
		t.Fatal("want an error")
	}
	if line, col := Position(err); line != 0 || col != 0 {
		t.Fatalf("Position = %d,%d, want no place", line, col)
	}
	if line, col := Position(nil); line != 0 || col != 0 {
		t.Fatalf("Position(nil) = %d,%d", line, col)
	}
}

func TestParseRefusesBadRules(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"unknown effect", `{"rules":[{"effect":"alow","tool":"shell"}]}`, "effect"},
		{"missing effect", `{"rules":[{"tool":"shell"}]}`, "effect"},
		{"missing tool", `{"rules":[{"effect":"allow"}]}`, "tool"},
		{"unsupported version", `{"version":2,"rules":[]}`, "version 2"},
		{"not an object", `[]`, "cannot unmarshal"},
		{"trailing data", `{"rules":[]} {"rules":[]}`, "trailing data"},
	}
	for _, tc := range cases {
		_, err := Parse([]byte(tc.text))
		if err == nil {
			t.Errorf("%s: Parse(%s) = nil, want an error", tc.name, tc.text)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err, tc.want)
		}
	}
}

// An empty policy is not a malformed one: a file that lists no rules is a
// workspace that has decided nothing, which is the same as no file.
func TestParseAcceptsAnEmptyPolicy(t *testing.T) {
	for _, text := range []string{`{}`, `{"version":1}`, `{"version":1,"rules":[]}`} {
		set, err := Parse([]byte(text))
		if err != nil {
			t.Errorf("Parse(%s) = %v", text, err)
			continue
		}
		if set.Len() != 0 {
			t.Errorf("Parse(%s).Len() = %d, want 0", text, set.Len())
		}
	}
}

// Programmatic rules go through the same validation as a file's, so a caller
// cannot install a rule a file would have been refused for.
func TestNewValidatesLikeParse(t *testing.T) {
	if set, err := New(nil); err != nil || set != nil {
		t.Errorf("New(nil) = (%v, %v), want (nil, nil)", set, err)
	}
	if _, err := New([]Rule{{Effect: "allow"}}); err == nil {
		t.Error("New with no tool = nil, want an error")
	}
	set, err := New([]Rule{{Effect: "deny", Tool: "mcp_*"}})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	if !set.Evaluate(CallFacts{Tool: "mcp_x"}, "linux").Matched {
		t.Error("a programmatic rule did not match")
	}
}

func write(t *testing.T, dir, text string) {
	t.Helper()
	path := filepath.Join(dir, ".anvil")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, FileName), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
