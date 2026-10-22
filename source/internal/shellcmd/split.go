// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package shellcmd answers one question about a shell command line: can it be
// *proved* not to change anything? It never guesses, and it is not a shell —
// nothing here evaluates, expands, or executes. What it cannot take apart
// literally it reports as unknown, and an unknown command is one the user is
// asked about. See doc/design/v0.x/36-tool-permissions.md.
package shellcmd

import "strings"

// Split separates a command line into its simple commands at the operators that
// join them, outside quotes and backslash escapes.
//
// ok is false when the line cannot be taken apart without evaluating it — an
// unterminated quote, for instance. A caller that gets ok=false must treat the
// whole line as unanalyzable rather than as one segment.
func Split(command string) (segments []string, ok bool) {
	var (
		cur   strings.Builder
		quote byte // 0, '\'' or '"' while inside quotes
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segments = append(segments, s)
		}
		cur.Reset()
	}

	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			cur.WriteByte(c)
			switch {
			case c == quote:
				quote = 0
			case quote == '"' && c == '\\' && i+1 < len(command):
				// Keep the escaped pair together; Lex refuses it later, which
				// is the one place that decision is made.
				i++
				cur.WriteByte(command[i])
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '\\':
			// An escaped operator is not an operator. Keep the pair; whether
			// the escape is acceptable is Lex's call, not this function's.
			cur.WriteByte(c)
			if i+1 < len(command) {
				i++
				cur.WriteByte(command[i])
			}
		case '\n', ';':
			flush()
		case '&', '|':
			// && and || are one operator; & and | are their own.
			if i+1 < len(command) && command[i+1] == c {
				i++
			}
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	if len(segments) == 0 {
		return nil, false
	}
	return segments, true
}

// Lex turns one simple command into literal argv tokens.
//
// It fails whenever a token's value would depend on the shell: `$` or backtick
// expansion, a backslash escape (on POSIX), a redirection, an unquoted glob
// metacharacter, or a subshell/brace/history character. Quotes are allowed only
// when what they enclose is itself literal — which is what lets
// `sed -n '1p' file` and `grep -n "name" file` be proved read-only, while
// `grep -n "$PATTERN" file` cannot be.
//
// The invariant callers rely on: every token this returns is exactly the string
// the shell would pass as that argument, with no rewriting.
func Lex(segment, goos string) (argv []string, ok bool) {
	var (
		out     []string
		cur     strings.Builder
		quote   byte
		started bool
	)
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}

	for i := 0; i < len(segment); i++ {
		c := segment[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
				continue
			}
			cur.WriteByte(c)
		case quote == '"':
			switch c {
			case '"':
				quote = 0
			case '$', '`', '\\':
				return nil, false
			default:
				cur.WriteByte(c)
			}
		default:
			switch c {
			case '\'', '"':
				quote = c
			case ' ', '\t':
				flush()
				continue
			case '$', '`':
				return nil, false
			case '\\':
				// POSIX: an escape means the argument is not the text it looks
				// like. Windows: a backslash is a path separator, and the
				// metacharacters that do matter there are handled below.
				if goos == "windows" {
					cur.WriteByte(c)
				} else {
					return nil, false
				}
			case '{':
				// `{}` is find's placeholder, not brace expansion: bash expands
				// only a brace holding a comma or a range. Any other unquoted
				// brace is refused, because expansion can rewrite a *later*
				// argument — `find . {-delete,}` expands to `find . -delete`,
				// and a rule matcher comparing the text as written would then
				// match something the shell never passes.
				if strings.HasPrefix(segment[i:], "{}") && tokenEndsAt(segment, i+2) {
					cur.WriteString("{}")
					i++ // consume the '}'
				} else {
					return nil, false
				}
			case '}', '<', '>', '(', ')', '!', '*', '?', '[', ']', '&', '|', ';', '\n':
				// Redirection, subshell, brace expansion, history, glob, or a
				// separator that Split somehow left behind: all unprovable.
				// NOTE: this is why "[" cannot be a command name here — it is
				// rejected as a glob metacharacter before it could be matched.
				return nil, false
			case '%', '^':
				// cmd.exe performs variable expansion and caret escaping on
				// these, so a token containing one is not the token that runs.
				if goos == "windows" {
					return nil, false
				}
				cur.WriteByte(c)
			default:
				cur.WriteByte(c)
			}
		}
		started = true
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// tokenEndsAt reports whether offset i in segment ends the token being read —
// the segment ended, or whitespace follows. It exists so that `{}` is accepted
// as a token of its own and nothing longer: `{}}`, `{}x`, and `{}a,b}` are all
// refused, so the only brace this package ever passes through is the one the
// shell would also pass through unchanged.
func tokenEndsAt(segment string, i int) bool {
	return i >= len(segment) || segment[i] == ' ' || segment[i] == '\t'
}
