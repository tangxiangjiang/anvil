// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

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
// commits alongside its code; see LocalOptions.LoadPermissions. Rules given here
// are appended after the file's, so the file keeps the order a reader sees.
//
// See doc/design/v1.0/36-tool-permissions.md.
type PermissionRule struct {
	Effect  string
	Tool    string
	Pattern string
	Reason  string
}
