// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import "testing"

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		// Tool names are single segments, so a star cannot wander.
		{"mcp_*", "mcp_x", true},
		{"mcp_*", "mcpish", false},
		{"mcp_*", "mcp", false},
		{"mcp_*", "mcpx", false},
		{"*", "shell", true},
		{"shell", "shell", true},
		{"shell", "shells", false},

		// A star stays inside one path segment: the file must be named.
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/a/b.go", false},
		{"*.go", "a.go", true},
		{"*.go", "src/a.go", false}, // no basename fallback
		{"a?.go", "ab.go", true},
		{"a?.go", "a.go", false},

		// `**` covers any number of segments, including none.
		{"src/**/*.go", "src/a.go", true},
		{"src/**/*.go", "src/a/b.go", true},
		{"src/**/*.go", "src/a.md", false},
		{"src/**/*.go", "other/a.go", false},
		{"**/*.go", "a.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"**", "a/b", true},
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "docs", true}, // `**` covers zero segments
	}
	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestMatchCommand(t *testing.T) {
	cases := []struct {
		pattern string
		command string
		want    bool
	}{
		// The reserved pattern asks the analyzer; it never compares text.
		{readonlyPattern, "ls -la", true},
		{readonlyPattern, "cd sub && git status", true},
		{readonlyPattern, "rm -rf x", false},
		{readonlyPattern, "ls > out", false},
		{readonlyPattern, "ls *.go", false},

		// Token prefix, not string prefix.
		{"git status:*", "git status", true},
		{"git status:*", "git status -s", true},
		{"git status:*", "git status --short", true},
		{"git status:*", "git statusx", false},
		{"git status:*", "git stash", false},
		{"git status:*", "", false},
		{"rm -rf:*", "rm -rf /", true},
		{"rm -rf:*", "rm -r /", false},

		// Exact argv.
		{"go version", "go version", true},
		{"go version", "go version -m", false},
		{"go version", "go versio", false},

		// A whole-line rule refuses a compound line.
		{"git status", "git status && rm -rf /", false},
		{"git status:*", "git status && rm -rf /", false},
		{"rm -rf:*", "echo hi && rm -rf /", false},

		// `*` is every shell call, which is what omitting the pattern says too.
		{"*", "rm -rf /", true},
		{"git *", "git push --force", true},
		{"git *", "git status", true},
		{"git *", "gitupload", false},

		// The pattern is lexed the same way the command is, so a pattern that
		// cannot be read as literal tokens — or that quotes a space into one
		// token while the command splits it — matches nothing.
		{"git $(date):*", "git status", false},
		{"git \"a b\":*", "git a b", false},
	}
	for _, tc := range cases {
		if got := matchCommand(tc.pattern, tc.command, "linux"); got != tc.want {
			t.Errorf("matchCommand(%q, %q) = %v, want %v", tc.pattern, tc.command, got, tc.want)
		}
	}
}

// A path rule is a claim about the whole call. Allowing a patch that also edits
// a file the rule never mentioned is the mistake this shape exists to prevent.
func TestPathRuleNeedsEveryPath(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		paths   []string
		want    bool
	}{
		{"one path inside", "src/**/*.go", []string{"src/a.go"}, true},
		{"both inside", "src/**/*.go", []string{"src/a.go", "src/b/c.go"}, true},
		{"second outside", "src/**/*.go", []string{"src/a.go", "README.md"}, false},
		{"none", "src/**/*.go", nil, false},
		{"separators normalised", "src/**/*.go", []string{`src\a.go`}, true},
		{"dot slash normalised", "src/**/*.go", []string{"./src/a.go"}, true},
		{"case is not folded", "SRC/**", []string{"src/a.go"}, false},
	}
	for _, tc := range cases {
		if got := matchPaths(tc.pattern, tc.paths); got != tc.want {
			t.Errorf("%s: matchPaths(%q, %q) = %v, want %v", tc.name, tc.pattern, tc.paths, got, tc.want)
		}
	}
}
