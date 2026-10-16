// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package permissions decides one thing about a tool call: is it allowed,
// denied, or must somebody be asked? The rules are written by the user in
// .anvil/permissions.json, and this package is the only thing that reads them.
//
// It knows nothing about tools beyond the facts a call carries — see CallFacts —
// which is deliberate on two counts. There is no import back into
// internal/tools, so no cycle; and a rule can only match facts the approval card
// also shows, so nothing is ever allowed on evidence the user was not shown.
//
// See doc/design/v1.0/36-tool-permissions.md.
package permissions

import "fmt"

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
	// vocabulary, not this package's — see doc/design/v1.0/37-permission-modes.md
	// — so only the shape is checked here.
	DefaultMode *string `json:"defaultMode,omitempty"`
	Rules       []Rule  `json:"rules"`
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
	// Index is the matching rule's position in the file, counted from zero, in
	// the order the file lists them.
	Index int
	// Tool and Pattern are the rule as written, so a client can say which rule
	// decided.
	Tool    string
	Pattern string
	// Reason is the rule's reason, empty when it gave none.
	Reason string
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
// appear wherever the decision is explained.
func (d Decision) Ref() string {
	ref := fmt.Sprintf(".anvil/%s rule %d (%s)", FileName, d.Index+1, d.String())
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
}

// rule is a validated Rule: the effect is already parsed, so the decision path
// does no string comparisons of its own.
type rule struct {
	effect  Effect
	tool    string // a glob over the tool name
	pattern string // as written; what it is compared against depends on the call
	reason  string
}

// Rules returns the rules as written, in the order they decide.
func (s *Set) Rules() []Rule {
	if s == nil {
		return nil
	}
	out := make([]Rule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, Rule{Effect: r.effect.String(), Tool: r.tool, Pattern: r.pattern, Reason: r.reason})
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
		Index:   i,
		Tool:    r.tool,
		Pattern: r.pattern,
		Reason:  r.reason,
	}.Ref()
}

// Evaluate decides a call. Rules are tried in file order and the outcome is:
//
//   - across effects, deny > ask > allow — one matching deny defeats every
//     allow and ask, wherever in the file it sits;
//   - within one effect, the first rule in the file wins, and Index names it.
//
// "Most specific wins" is deliberately not implemented: specificity is not
// written in the file, so a reader would have to recompute a metric nobody
// agrees on. File order can be read top to bottom instead.
//
// goos selects the shell dialect the analyzer models; it is ignored for every
// call that carries no command.
func (s *Set) Evaluate(f CallFacts, goos string) Decision {
	if s == nil {
		return Decision{}
	}
	var best Decision
	for i := range s.rules {
		r := s.rules[i]
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
			Index:   i,
			Tool:    r.tool,
			Pattern: r.pattern,
			Reason:  r.reason,
		}
	}
	return best
}
