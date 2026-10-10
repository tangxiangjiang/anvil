// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"strings"

	"github.com/anvil-dev/anvil/internal/tools"
)

// applyBuiltinToolFilters removes built-in/MCP tools per allow/deny lists.
// Custom Tools from Options are never removed (matched by exact registered name from Tools).
func applyBuiltinToolFilters(reg *tools.Registry, opts Options) {
	if reg == nil {
		return
	}
	custom := map[string]bool{}
	for _, spec := range opts.Tools {
		custom[spec.Name] = true
	}

	allow := expandToolSelectors(opts.BuiltinTools)
	deny := expandToolSelectors(opts.DisallowedTools)

	for _, name := range reg.Names() {
		if custom[name] {
			continue
		}
		if len(allow) > 0 && !toolAllowed(name, allow) {
			reg.Unregister(name)
			continue
		}
		if toolAllowed(name, deny) {
			reg.Unregister(name)
		}
	}
}

func expandToolSelectors(selectors []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range selectors {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			continue
		}
		switch s {
		case "shell":
			out["shell"] = true
		case "read":
			out["read_file"] = true
		case "write":
			out["write_file"] = true
		case "edit":
			out["edit_file"] = true
			out["apply_patch"] = true
		case "search":
			out["grep"] = true
			out["glob"] = true
			out["semantic_search"] = true
		case "skills":
			out["list_skills"] = true
			out["read_skill"] = true
		case "web":
			out["web_search"] = true
			out["fetch_url"] = true
		case "lsp":
			out["diagnostics"] = true
			out["definition"] = true
			out["references"] = true
			out["hover"] = true
			out["rename_symbol"] = true
		case "git":
			out["git_status"] = true
			out["git_diff"] = true
			out["git_log"] = true
		case "verify", "test":
			out["run_tests"] = true
			out["build"] = true
		case "vision":
			out["read_image"] = true
		case "mcp":
			out["__mcp__"] = true
		default:
			out[s] = true
		}
	}
	return out
}

func toolAllowed(name string, set map[string]bool) bool {
	if set[name] {
		return true
	}
	if set["__mcp__"] && strings.HasPrefix(name, "mcp_") {
		return true
	}
	return false
}
