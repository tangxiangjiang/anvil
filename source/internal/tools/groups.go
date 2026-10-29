// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import "strings"

// groupTools is the built-in tool group table. It is the single source of truth
// for every selector accepted by Options.BuiltinTools, Options.DisallowedTools,
// and Options.PermissionGroups.
//
// The MCP entry expands to the sentinel "__mcp__" rather than a name list,
// because MCP tool names are only known once a server reports its catalogue.
var groupTools = map[string][]string{
	"shell":  {"shell"},
	"read":   {"read_file"},
	"write":  {"write_file"},
	"edit":   {"edit_file", "apply_patch"},
	"search": {"grep", "glob", "semantic_search"},
	"skills": {"list_skills", "read_skill"},
	"web":    {"web_search", "fetch_url"},
	"lsp":    {"diagnostics", "definition", "references", "hover", "rename_symbol"},
	"git":    {"git_status", "git_diff", "git_log"},
	"verify": {"run_tests", "build"},
	"vision": {"read_image"},
	"mcp":    {mcpSentinel},
	// agent covers the tools that create or manage other agents. They are gateable
	// like any other built-in so a session can require a card before it spends
	// tokens on a subagent; see doc/design/v0.x/41-subagents-and-tasks.md.
	"agent": {"spawn_agent", "delegate", "run_workflow", "task_list", "task_output", "task_cancel"},
}

// mcpSentinel marks "every tool whose name starts with mcp_".
const mcpSentinel = "__mcp__"

// GroupNames lists the accepted group selectors.
func GroupNames() []string {
	out := make([]string, 0, len(groupTools))
	for name := range groupTools {
		out = append(out, name)
	}
	return out
}

// ExpandToolSelectors resolves a mixed list of group names and literal tool
// names into the concrete tool names they cover.
//
// A selector that is not a known group is kept verbatim, so callers may name a
// tool directly. "test" is an alias for "verify".
func ExpandToolSelectors(selectors []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range selectors {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			continue
		}
		if s == "test" {
			s = "verify"
		}
		if names, ok := groupTools[s]; ok {
			for _, n := range names {
				out[n] = true
			}
			continue
		}
		out[s] = true
	}
	return out
}

// SelectorSetMatches reports whether name is covered by an expanded set.
func SelectorSetMatches(name string, set map[string]bool) bool {
	if set[name] {
		return true
	}
	return set[mcpSentinel] && strings.HasPrefix(name, "mcp_")
}

// SelectorListMatches reports whether name is covered by raw selectors.
func SelectorListMatches(selectors []string, name string) bool {
	return SelectorSetMatches(name, ExpandToolSelectors(selectors))
}

// GroupOf returns the group a built-in tool belongs to. The second result is
// false for tools that are not part of the built-in group table — MCP servers,
// plugins, and caller-registered Options.Tools.
func GroupOf(name string) (string, bool) {
	for group, names := range groupTools {
		for _, n := range names {
			if n == name {
				return group, true
			}
		}
	}
	return "", false
}

// IsExternalTool reports whether t was registered by someone other than the
// runtime: an MCP server, a plugin, or a caller-supplied Options.Tools entry.
//
// Every such tool is registered as a *FuncTool, while each tool the runtime
// ships is either a concrete type or a FuncTool marked Builtin (the tools that
// need the surrounding agent). That is what lets the permission gate decide
// whether a tool has a group at all — external tools do not, so once a caller
// opts into group gating they are gated unconditionally rather than silently
// exempted.
func IsExternalTool(t Tool) bool {
	f, isFunc := t.(*FuncTool)
	return isFunc && !f.Builtin
}
