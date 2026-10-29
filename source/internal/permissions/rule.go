// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package permissions decides one thing about a tool call: is it allowed,
// denied, or must somebody be asked? The rules are written by the user in
// .anvil/permissions.json and, since 0.46, in ~/.anvil/permissions.json, and
// this package is the only thing that reads them.
//
// It knows nothing about tools beyond the facts a call carries — see CallFacts —
// which is deliberate on two counts. There is no import back into
// internal/tools, so no cycle; and a rule can only match facts the approval card
// also shows, so nothing is ever allowed on evidence the user was not shown.
//
// See doc/design/v0.x/36-tool-permissions.md and, for scopes and protected
// paths, doc/design/v0.x/37-permission-modes.md.
package permissions

import "fmt"

// Rule sources. A compiled rule remembers where it was written, because a
// citation has to name a line the reader can open: with two files, a position
// in the merged list names no line in either.
const (
	// SourceUser is the user file, ~/.anvil/permissions.json.
	SourceUser = "user"
	// SourceWorkspace is the workspace file, .anvil/permissions.json.
	SourceWorkspace = "workspace"
	// SourceCode is a rule a program supplied through Options.PermissionRules.
	SourceCode = "code"
)

// sourceLabel is how a citation names a source: the file a reader opens, or the
// caller's list when there is no file.
func sourceLabel(source string) string {
	switch source {
	case SourceUser:
		return "user " + FileName
	case SourceCode:
		return "session rules"
	default:
		return ".anvil/" + FileName
	}
}

// Effect is what a matching rule does to a call.
type Effect uint8

const (
	// Allow runs the call without asking.
	Allow Effect = iota
	// Ask forces the approval prompt, for this call, even when the mode gate
	// would not have gated the tool.
	Ask
	// Deny refuses the call. Nothing overrides it — not a session allowlist,
	// not bypass. A deny is the policy the user wrote down, and the whole point
	// of evaluating rules outside the permission callback is that it holds even
	// where there is nobody to ask.
	Deny
)

func (e Effect) String() string {
	switch e {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	}
	return "unknown"
}

// ParseEffect reads one of the three effect names from a rule file.
func ParseEffect(s string) (Effect, bool) {
	switch s {
	case "allow":
		return Allow, true
	case "ask":
		return Ask, true
	case "deny":
		return Deny, true
	}
	return 0, false
}

// Version is the only rule-file version this package understands. An unknown
// version is refused rather than read on a guess: a future version may give the
// same words a different meaning, and a misread policy is worse than no policy.
const Version = 1

// Rule is one entry of a rule file.
//
// Pattern is interpreted per call rather than per tool name — see (*Set).Evaluate
// — because the pattern that describes a command line is not the pattern that
// describes a path. An empty Pattern has the plain meaning: match by tool name.
type Rule struct {
	Effect  string `json:"effect"`
	Tool    string `json:"tool"`
	Pattern string `json:"pattern,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Unless makes a deny rule cover a write whose paths are not all inside these
	// globs: the rule fires when any path a call touches is outside them, and when
	// the paths cannot be read at all. It is how a delegated child is confined to its
	// brief's files. It is a deny only, and the file parsers refuse the key (json:"-"
	// with DisallowUnknownFields), so it exists for rules a program passes in code.
	Unless []string `json:"-"`

	// Source and Index are where the rule was read from, not part of the file:
	// they are filled by a reader and ignored when writing. Index is one-based,
	// counted in that source's own file, so a citation lands on a line the reader
	// can find.
	Source string `json:"-"`
	Index  int    `json:"-"`
}

// File is the on-disk shape of .anvil/permissions.json.
//
// The field order is the order a rewrite writes them, so a human reading the
// file meets what it says about the workspace before the list of rules.
type File struct {
	Version int `json:"version,omitempty"`
	// DefaultMode is the mode a new session starts in when the client sends none.
	//
	// It is a pointer so that an absent key and an empty value stay different
	// files: a mode name is a word, so `""` is a mistake to point at rather than a
	// declaration that says nothing. Which words are *modes* is the host's
	// vocabulary, not this package's — see doc/design/v0.x/37-permission-modes.md
	// — so only the shape is checked here.
	DefaultMode *string `json:"defaultMode,omitempty"`
	Rules       []Rule  `json:"rules"`
}

// UserFile is the on-disk shape of ~/.anvil/permissions.json.
//
// It is deliberately not the workspace file's shape. A user file may add
// protected-path globs; a workspace file may declare a starting mode. Neither
// parser accepts the other's key, so a repository cannot widen what a person may
// rewrite without asking, and a machine-wide file cannot choose the mode of a
// repository's sessions.
type UserFile struct {
	Version int `json:"version,omitempty"`
	// Protected are globs for paths an allow rule may not cover, in addition to
	// the fixed list in Protection.
	Protected []string `json:"protected,omitempty"`
	Rules     []Rule   `json:"rules"`
}

// modeWord reports whether s could be a mode name at all: one lowercase word of
// letters, digits and underscores. It is a shape, not a vocabulary — `yolo` passes
// it and the host refuses it, while `Bypass` and `accept edits` never get that far.
func modeWord(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// CallFacts are the facts a rule is allowed to match on: exactly what the
// approval card shows for this call. None of it is computed by running the tool,
// so matching is cheap and can never depend on something the user has not seen.
type CallFacts struct {
	// Tool is the tool name, e.g. "shell" or "mcp_fetch".
	Tool string
	// Command is the shell command line as written. Empty for every other tool.
	Command string
	// Paths are the paths the call touches, as the card shows them. Empty when
	// the call touches no file, or when they could not be determined — and a
	// call whose paths are unknown never matches a path rule.
	Paths []string
}

// Decision is what a rule set concluded about a call.
type Decision struct {
	// Matched is false when no rule applied. The caller then decides exactly as
	// it did before there were rules at all.
	Matched bool
	Effect  Effect
	// Source is where the deciding rule was written: SourceUser,
	// SourceWorkspace or SourceCode. Empty reads as the workspace, which is what
	// a decision built by hand for display has always meant.
	Source string
	// Index is the matching rule's position in its own file, counted from zero.
	Index int
	// Tool and Pattern are the rule as written, so a client can say which rule
	// decided.
	Tool    string
	Pattern string
	// Reason is the rule's reason, empty when it gave none.
	Reason string
	// Protected lists the paths this call touches that an allow rule may not
	// cover — see Protection. It is set whether or not a rule matched, because
	// "this path is protected" is a fact about the call, not about a rule. An
	// allow rule is skipped for such a call; deny and ask still decide.
	Protected []string
}

// String names the rule the way the file spells it, for a message a reader can
// search for.
func (d Decision) String() string {
	if !d.Matched {
		return "no rule"
	}
	if d.Pattern == "" {
		return fmt.Sprintf("%s %s", d.Effect, d.Tool)
	}
	return fmt.Sprintf("%s %s %q", d.Effect, d.Tool, d.Pattern)
}

// Ref names the rule the way a reader of the file finds it: the file, the
// rule's position counted the way a reader counts, the rule as written, and its
// reason. A denial and an approval card both quote this, so the same words
// appear wherever the decision is explained. The position is counted inside the
// rule's own source, so `user permissions.json rule 2` and
// `.anvil/permissions.json rule 1` are both lines a reader can open.
func (d Decision) Ref() string {
	ref := fmt.Sprintf("%s rule %d (%s)", sourceLabel(d.Source), d.Index+1, d.String())
	if d.Reason != "" {
		ref += ": " + d.Reason
	}
	return ref
}

// Set is a compiled rule set. A nil *Set holds no rules and has no opinion about
// anything, so a caller can carry nil to mean "this workspace has no rule file"
// without a special case at every use.
type Set struct {
	rules []rule
	// defaultMode is the mode the file declares for new sessions, as written, and
	// empty when it declares none. A successfully parsed file cannot hold an empty
	// declaration — Parse refuses one — so empty means "nothing declared".
	defaultMode string
	// protection is what an allow rule may not cover; nil means nothing is
	// protected, which is the right answer for a set nobody gave a workspace.
	protection *Protection
}

// rule is a validated Rule: the effect is already parsed, so the decision path
// does no string comparisons of its own. source and index are where it was read
// from; index is zero-based and counted inside that source.
type rule struct {
	effect          Effect
	tool            string   // a glob over the tool name
	pattern         string   // as written; what it is compared against depends on the call
	unless          []string // cleaned globs; the rule covers a call with a path outside them
	unlessAsWritten []string // the same globs as given, so Rules() round-trips through New
	reason          string
	source          string
	index           int
}

// Rules returns the rules as written, in the order they decide, each carrying
// the source it came from and its one-based position there.
func (s *Set) Rules() []Rule {
	if s == nil {
		return nil
	}
	out := make([]Rule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, Rule{
			Effect:  r.effect.String(),
			Tool:    r.tool,
			Pattern: r.pattern,
			Reason:  r.reason,
			Unless:  append([]string(nil), r.unlessAsWritten...),
			Source:  r.source,
			Index:   r.index + 1,
		})
	}
	return out
}

// Len reports how many rules the set holds.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.rules)
}

// DefaultMode is the mode the file declares for new sessions, empty when it
// declares none. Whether the host will honour it is a separate question: the
// vocabulary is the host's, and a word this package accepted as a shape may still
// be one no mode answers to.
func (s *Set) DefaultMode() string {
	if s == nil {
		return ""
	}
	return s.defaultMode
}

// Describe names the rule at position i, counted from zero in the order Rules
// returns, the way a reader of the file would cite it. It goes through Decision
// so that a list of rules and the explanation of a decision spell a rule the
// same way; a position outside the set describes nothing.
func (s *Set) Describe(i int) string {
	if s == nil || i < 0 || i >= len(s.rules) {
		return ""
	}
	r := s.rules[i]
	return Decision{
		Matched: true,
		Effect:  r.effect,
		Source:  r.source,
		Index:   r.index,
		Tool:    r.tool,
		Pattern: r.pattern,
		Reason:  r.reason,
	}.Ref()
}

// Evaluate decides a call. Rules are tried in the set's order — user, then
// workspace, then code, for a set built by Merge — and the outcome is:
//
//   - across effects, deny > ask > allow — one matching deny defeats every
//     allow and ask, wherever in the set it sits;
//   - within one effect, the first rule in that order wins, and Source and Index
//     name it.
//
// "Most specific wins" is deliberately not implemented: specificity is not
// written in the file, so a reader would have to recompute a metric nobody
// agrees on. File order can be read top to bottom instead.
//
// A call that touches a protected path is never decided by an allow rule: the
// allow is skipped, and deny and ask decide as usual. With no rule left the
// call falls through to the mode, exactly as it would have without the allow.
// The protected paths are reported either way, because "this path is protected"
// is a fact about the call that survives whichever rule (or mode) decided it.
//
// goos selects the shell dialect the analyzer models; it is ignored for every
// call that carries no command.
func (s *Set) Evaluate(f CallFacts, goos string) Decision {
	if s == nil {
		return Decision{}
	}
	protected := s.protectedPaths(f)
	var best Decision
	for i := range s.rules {
		r := s.rules[i]
		if len(protected) > 0 && r.effect == Allow {
			continue
		}
		if !r.covers(f, goos) {
			continue
		}
		// Ordered so a later rule replaces the incumbent only by being a
		// stronger effect, which is what keeps the first match within one
		// effect.
		if best.Matched && r.effect <= best.Effect {
			continue
		}
		best = Decision{
			Matched: true,
			Effect:  r.effect,
			Source:  r.source,
			Index:   r.index,
			Tool:    r.tool,
			Pattern: r.pattern,
			Reason:  r.reason,
		}
	}
	best.Protected = protected
	return best
}
