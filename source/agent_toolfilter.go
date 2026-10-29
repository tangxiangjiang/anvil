// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
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

// expandToolSelectors resolves group names and literal tool names. The table
// lives in internal/tools so that Options.BuiltinTools, Options.DisallowedTools,
// and Options.PermissionGroups share one definition.
func expandToolSelectors(selectors []string) map[string]bool {
	return tools.ExpandToolSelectors(selectors)
}

func toolAllowed(name string, set map[string]bool) bool {
	return tools.SelectorSetMatches(name, set)
}
