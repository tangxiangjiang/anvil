// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package shellcmd

import (
	"reflect"
	"testing"
)

func TestSplitSeparatesOperators(t *testing.T) {
	cases := []struct {
		command  string
		segments []string
	}{
		{"ls", []string{"ls"}},
		{"ls -la", []string{"ls -la"}},
		{"cd x && ls", []string{"cd x", "ls"}},
		{"a || b", []string{"a", "b"}},
		{"a ; b", []string{"a", "b"}},
		{"a | b", []string{"a", "b"}},
		{"a & b", []string{"a", "b"}},
		{"a\nb", []string{"a", "b"}},
		{"a && b || c ; d | e", []string{"a", "b", "c", "d", "e"}},
	}
	for _, tc := range cases {
		got, ok := Split(tc.command)
		if !ok {
			t.Errorf("Split(%q) not ok", tc.command)
			continue
		}
		if !reflect.DeepEqual(got, tc.segments) {
			t.Errorf("Split(%q) = %q, want %q", tc.command, got, tc.segments)
		}
	}
}

// A separator inside quotes belongs to the argument, not to the command line.
// Without this, `echo "a && b"` would be read as two commands.
func TestSplitQuotesProtectOperators(t *testing.T) {
	cases := []struct {
		command  string
		segments []string
	}{
		{`echo "a && b"`, []string{`echo "a && b"`}},
		{`echo 'a; b'`, []string{`echo 'a; b'`}},
		{`echo "a | b" && ls`, []string{`echo "a | b"`, "ls"}},
	}
	for _, tc := range cases {
		got, ok := Split(tc.command)
		if !ok {
			t.Errorf("Split(%q) not ok", tc.command)
			continue
		}
		if !reflect.DeepEqual(got, tc.segments) {
			t.Errorf("Split(%q) = %q, want %q", tc.command, got, tc.segments)
		}
	}
}

func TestSplitRefusesUnterminatedQuote(t *testing.T) {
	for _, command := range []string{`echo "unclosed`, `echo 'unclosed`, `ls "a`} {
		if _, ok := Split(command); ok {
			t.Errorf("Split(%q) returned ok for an unterminated quote", command)
		}
	}
}

func TestLex(t *testing.T) {
	cases := []struct {
		name    string
		segment string
		goos    string
		argv    []string
		ok      bool
	}{
		{name: "plain", segment: "git status", argv: []string{"git", "status"}, ok: true},
		{name: "single quoted script", segment: "sed -n '1p' f", argv: []string{"sed", "-n", "1p", "f"}, ok: true},
		{name: "double quoted literal", segment: `grep -n "name" f`, argv: []string{"grep", "-n", "name", "f"}, ok: true},
		{name: "quotes group whitespace", segment: `echo 'a b'`, argv: []string{"echo", "a b"}, ok: true},
		{name: "quoted glob is literal", segment: `find . -name '*.go'`, argv: []string{"find", ".", "-name", "*.go"}, ok: true},
		{name: "empty quoted argument counts", segment: `echo ""`, argv: []string{"echo", ""}, ok: true},

		{name: "variable", segment: "echo $HOME", ok: false},
		{name: "variable in quotes", segment: `echo "$HOME"`, ok: false},
		{name: "substitution", segment: "echo $(date)", ok: false},
		{name: "backtick", segment: "echo `date`", ok: false},
		{name: "escape in double quotes", segment: `echo "a\"b"`, ok: false},
		{name: "escape posix", segment: `ls \;`, ok: false},
		{name: "unquoted glob", segment: "ls *.go", ok: false},
		{name: "char class glob", segment: "ls [ab].go", ok: false},
		{name: "redirection out", segment: "ls > out", ok: false},
		{name: "redirection in", segment: "cat < in", ok: false},
		{name: "fd redirection", segment: "ls 2>&1", ok: false},
		{name: "heredoc", segment: "cat <<EOF", ok: false},
		{name: "subshell", segment: "(ls)", ok: false},
		{name: "brace expansion", segment: "ls {a,b}", ok: false},
		{name: "brace expansion rewriting a later arg", segment: "find . {-delete,}", ok: false},
		{name: "brace placeholder", segment: "find . -exec rm {} +", argv: []string{"find", ".", "-exec", "rm", "{}", "+"}, ok: true},
		{name: "brace placeholder must stand alone", segment: "find . -name {}x", ok: false},
		{name: "brace placeholder not doubled", segment: "find . -name {}}", ok: false},
		{name: "history", segment: "ls !$", ok: false},
		{name: "empty", segment: "   ", ok: false},

		{name: "windows path", segment: `dir C:\temp`, goos: "windows", argv: []string{"dir", `C:\temp`}, ok: true},
		{name: "windows escape refused", segment: `dir C:\temp`, goos: "linux", ok: false},
		{name: "windows percent", segment: "echo %PATH%", goos: "windows", ok: false},
		{name: "windows caret", segment: "dir ^& ls", goos: "windows", ok: false},
	}
	for _, tc := range cases {
		goos := tc.goos
		if goos == "" {
			goos = "linux"
		}
		argv, ok := Lex(tc.segment, goos)
		if ok != tc.ok {
			t.Errorf("%s: Lex(%q, %s) ok = %v, want %v (argv %q)", tc.name, tc.segment, goos, ok, tc.ok, argv)
			continue
		}
		if ok && !reflect.DeepEqual(argv, tc.argv) {
			t.Errorf("%s: Lex(%q) = %q, want %q", tc.name, tc.segment, argv, tc.argv)
		}
	}
}
