// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"fmt"

	"github.com/anvil-dev/anvil/internal/permissions"
)

// PermissionRule is one rule of the workspace rule set (0.36+).
//
// Rules decide a tool call before Options.Permission is consulted, which is why
// they hold even for a caller that installed no permission callback: a deny is
// the policy the workspace wrote down, not a prompt somebody skipped.
//
//   - Effect is "allow", "ask", or "deny". Across effects deny beats ask beats
//     allow; within one effect the first rule in the file wins.
//   - Tool names the tool, and may be a glob: "shell", "write_file", "mcp_*".
//   - Pattern is what the call is compared against, and its meaning follows the
//     call rather than the tool name. For the shell it is the literal
//     "readonly" (the analyzer proves the whole command line non-mutating), an
//     exact command, or a prefix such as "git status:*"; for a tool that
//     touches files it is a path glob, and it has to cover every path the call
//     touches. Left empty, the rule matches the tool by name alone.
//   - Reason is free text, shown when the rule decides a call.
//
// The same rules can be written in .anvil/permissions.json, which a workspace
// commits alongside its code, and in ~/.anvil/permissions.json, which belongs to
// the person; see LocalOptions.LoadPermissions. Rules given here are appended
// after both, so the files keep the order a reader sees.
//
// Source and Index are read-only: a reader fills them with the file the rule came
// from and its one-based position there. A rule given in code leaves them empty.
//
// See doc/design/v0.x/36-tool-permissions.md and, for the second scope,
// doc/design/v0.x/37-permission-modes.md.
type PermissionRule struct {
	Effect  string
	Tool    string
	Pattern string
	Reason  string
	// Source is "user", "workspace" or "code" on a rule that was read; empty for
	// a rule being written.
	Source string
	// Index is the rule's one-based position in its source.
	Index int
}

// Permission rule scopes (0.46+). They name which file a rule belongs to: the
// person's ~/.anvil/permissions.json or the workspace's .anvil/permissions.json.
const (
	PermissionScopeUser      = "user"
	PermissionScopeWorkspace = "workspace"
)

// Permission rule effects (0.36+). These are the three words a rule file may
// write in its "effect" field, and the three a client has to render.
const (
	// PermissionEffectAllow runs a matching call without asking.
	PermissionEffectAllow = "allow"
	// PermissionEffectAsk forces the approval prompt for a matching call, even
	// when the mode gate would not have gated that tool.
	PermissionEffectAsk = "ask"
	// PermissionEffectDeny refuses a matching call. Nothing overrides it — not a
	// session allowlist, not bypass.
	PermissionEffectDeny = "deny"
)

// PermissionRuleMatch names the rule that decided one call, so a client can say
// why a call ran without asking, or why it was refused (0.36+).
//
// It is the rule as the file spells it: Index is its position among the file's
// rules counted from zero, and Pattern and Reason are what the user wrote.
// Describe renders it the way a denial does, which is the wording to show when a
// person needs to find the rule and change it.
type PermissionRuleMatch struct {
	Effect string
	// Source is where the rule was written: "user", "workspace" or "code".
	Source string
	// Index is its position among that source's rules, counted from zero.
	Index   int
	Tool    string
	Pattern string
	Reason  string
	// Protected lists the paths this call touches that an allow rule may not
	// cover. It is set whether or not a rule matched.
	Protected []string
}

// Describe names the rule the way a reader of the file finds it, e.g.
// `.anvil/permissions.json rule 4 (deny shell "rm -rf:*"): destructive`.
func (m *PermissionRuleMatch) Describe() string {
	if m == nil {
		return ""
	}
	return decisionOf(m).Ref()
}

// PermissionRuleError reports a rule file that could not be read. Path is
// absolute, Message never repeats it, and Line and Column are zero when the
// problem belongs to no particular place — a misspelled key, or a version this
// build does not read.
type PermissionRuleError struct {
	Path    string
	Message string
	Line    int
	Column  int
}

func (e *PermissionRuleError) Error() string {
	where := e.Path
	if e.Line > 0 {
		where = fmt.Sprintf("%s:%d:%d", e.Path, e.Line, e.Column)
	}
	if e.Message == "" {
		return where
	}
	return where + ": " + e.Message
}

// decisionOf projects the public match back onto the internal decision, so the
// wording lives in one place rather than in every client that explains a rule.
func decisionOf(m *PermissionRuleMatch) permissions.Decision {
	if m == nil {
		return permissions.Decision{}
	}
	effect, ok := permissions.ParseEffect(m.Effect)
	if !ok {
		effect = permissions.Allow
	}
	return permissions.Decision{
		Matched:   true,
		Effect:    effect,
		Source:    m.Source,
		Index:     m.Index,
		Tool:      m.Tool,
		Pattern:   m.Pattern,
		Reason:    m.Reason,
		Protected: m.Protected,
	}
}

// permissionRuleMatch projects an internal decision onto the public type.
func permissionRuleMatch(d *permissions.Decision) *PermissionRuleMatch {
	if d == nil || !d.Matched {
		return nil
	}
	return &PermissionRuleMatch{
		Effect:    d.Effect.String(),
		Source:    d.Source,
		Index:     d.Index,
		Tool:      d.Tool,
		Pattern:   d.Pattern,
		Reason:    d.Reason,
		Protected: d.Protected,
	}
}
