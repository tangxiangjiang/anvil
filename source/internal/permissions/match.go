// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"path"
	"strings"

	"github.com/anvil-dev/anvil/internal/shellcmd"
)

// readonlyPattern is the one reserved pattern. It asks the analyzer instead of
// comparing text, and matches only when the whole command line is proved
// non-mutating — never on a guess. A call to the shell builtin named `readonly`
// cannot be matched by name for this reason; write the command out instead.
const readonlyPattern = "readonly"

// ReadonlyPattern is readonlyPattern, exported for the SDK's public helpers,
// which build rule text a client can write into the file. One spelling, in one
// place: a client that guessed at it would write a rule that never applies.
const ReadonlyPattern = readonlyPattern

// covers reports whether one rule matches one call.
//
// Which fact the pattern is compared against is decided by the facts themselves
// rather than by a table of tool names: a call that carries a command is a shell
// call, a call that carries paths touches files, and a call that carries neither
// can only be matched by name. That keeps the package from having to learn every
// tool's name, and a rule on a tool whose facts do not exist simply does not
// match — which asks, and asking is the direction this design can afford.
func (r rule) covers(f CallFacts, goos string) bool {
	if !matchGlob(r.tool, f.Tool) {
		return false
	}
	if r.unless != nil {
		return outsideAll(r.unless, f.Paths)
	}
	if r.pattern == "" {
		// An omission matches by name alone. It is not a wildcard in the
		// dangerous direction: it is the broad rule the user wrote down.
		return true
	}
	switch {
	case f.Command != "":
		return matchCommand(r.pattern, f.Command, goos)
	case len(f.Paths) > 0:
		return matchPaths(r.pattern, f.Paths)
	}
	return matchGlob(r.pattern, f.Tool)
}

// matchCommand compares a shell rule's pattern against a command line. Three
// forms, and no substring matching in any of them:
//
//	"readonly"       the analyzer proves the whole line non-mutating
//	"git status:*"   a token prefix of a single simple command
//	"git status -s"  the argv of a single simple command, exactly
//
// There is deliberately no HasPrefix here. A prefix rule refuses a line made of
// several commands, so `git status:*` does not match `git status && rm -rf /`;
// and because the comparison is between tokens, it does not match `git statusx`
// either. A string prefix would pass both of those.
func matchCommand(pattern, command, goos string) bool {
	if pattern == readonlyPattern {
		return shellcmd.Analyze(command, goos) == shellcmd.ReadOnly
	}
	if strings.HasSuffix(pattern, "*") {
		base := strings.TrimSuffix(pattern, "*")
		base = strings.TrimSuffix(strings.TrimSpace(base), ":")
		base = strings.TrimSpace(base)
		if base == "" {
			// "*" is every shell call — the same thing an omitted pattern says,
			// written out.
			return true
		}
		want, ok := shellcmd.Lex(base, goos)
		if !ok {
			return false
		}
		have, ok := singleCommand(command, goos)
		if !ok {
			return false
		}
		return hasTokenPrefix(have, want)
	}
	want, ok := shellcmd.Lex(pattern, goos)
	if !ok {
		return false
	}
	have, ok := singleCommand(command, goos)
	if !ok {
		return false
	}
	return equalTokens(have, want)
}

// singleCommand lexes a command line that has to be exactly one simple command.
func singleCommand(command, goos string) ([]string, bool) {
	segments, ok := shellcmd.Split(command)
	if !ok || len(segments) != 1 {
		return nil, false
	}
	return shellcmd.Lex(segments[0], goos)
}

func hasTokenPrefix(have, want []string) bool {
	if len(have) < len(want) {
		return false
	}
	for i := range want {
		if have[i] != want[i] {
			return false
		}
	}
	return true
}

func equalTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// matchPaths reports whether one glob covers every path a call touches.
//
// A path rule is a claim about the whole call, so a single path outside the glob
// means the rule does not match and the call is asked about instead: allowing a
// patch that also edits a file the rule never mentioned is exactly the mistake
// this shape prevents. An empty path list matches nothing.
//
// Paths are compared as written, with `\` read as a separator. On a
// case-insensitive filesystem that means a rule spelled in the other case does
// not match — deliberately, because the alternative is matching a spelling the
// user did not write, and a rule that matches too little asks.
func matchPaths(pattern string, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	want := cleanPath(pattern)
	for _, p := range paths {
		if !matchGlob(want, cleanPath(p)) {
			return false
		}
	}
	return true
}

// outsideAll reports whether a call touches a path that none of the globs covers. A
// call with no readable paths counts as outside: an allowlist that a malformed patch
// could slip past would not confine anything.
func outsideAll(globs, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		clean := cleanPath(p)
		covered := false
		for _, g := range globs {
			if matchGlob(g, clean) {
				covered = true
				break
			}
		}
		if !covered {
			return true
		}
	}
	return false
}

// cleanPath puts a path into the form globs are written in: `/` separators, no
// leading `./`, no doubled or trailing separator, and any `.` or `..` resolved.
func cleanPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" {
		return ""
	}
	return path.Clean(p)
}

// matchGlob reports whether a pattern covers a name, with `/` as the separator.
// `*` and `?` stay inside one segment; `**` as a whole segment covers any number
// of segments including none, so `**/*.go` covers `a.go` and `src/a/b.go` while
// `*.go` covers only `a.go`. A whole name with no separators — a tool name — is
// therefore matched by one segment pattern.
//
// There is deliberately no basename fallback. A pattern that matches a file
// anywhere in the tree must say `**/`, because a glob that quietly reached
// deeper than it reads would be an allow the user did not write.
func matchGlob(pattern, name string) bool {
	if pattern == "" {
		return name == ""
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 || !matchSegment(pat[0], name[0]) {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// matchSegment matches one path segment, or a whole tool name, against a pattern
// with no `/` left in it.
func matchSegment(pat, s string) bool {
	p, t := []rune(pat), []rune(s)
	pi, ti := 0, 0
	star, mark := -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			// Remember where to resume if this star has to cover more.
			star, mark = pi, ti
			pi++
		case star >= 0:
			mark++
			pi, ti = star+1, mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
