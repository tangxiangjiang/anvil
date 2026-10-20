// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"encoding/json"
	"runtime"

	"github.com/anvil-dev/anvil/internal/shellcmd"
	"github.com/anvil-dev/anvil/internal/tools"
)

// AutoApproveCall reports whether auto mode may run this call without asking
// (0.41+).
//
// It is the classifier behind the client's `auto` rung, and it is deliberately
// not a model call: it reads the same facts a permission rule matches on — see
// tools.CallFactsFor — so "auto allowed this" and "rule 4 allowed this" rest on
// identical evidence, and the answer costs no request. The facts come from the
// arguments, never from the preview, so classification cannot depend on a
// rendering that was truncated for display.
//
// Three shapes are approved, each for a reason the SDK already guarantees:
//
//   - a shell call whose whole command line shellcmd.Analyze proves read-only.
//     Nothing about the line can change a file or start a lasting process;
//     this is the same proof the "readonly" rule pattern trusts. A line that is
//     mutating, unlexable, expands a variable, redirects, or contains a segment
//     the analyzer cannot classify is not approved — asking is always allowed.
//   - write_file, edit_file and apply_patch. Every path they name is resolved
//     through the workspace sandbox before the tool runs, so an edit is
//     confined to the workspace; auto does not read the file to form an opinion
//     about the diff. A patch that cannot be parsed names no paths and is not
//     approved, because there is nothing to establish.
//   - run_tests and build. Their argv is fixed by the operator's Options or
//     project.json; the model may append validated tokens only, and no shell
//     interprets them. The command is the session owner's own configuration,
//     not the model's.
//
// Everything else — egress, MCP, plugins, a tool this build does not know — is
// left to the approval card. "Read-only" is a statement about mutation, not
// about disclosure: a proved read-only command may still read a file outside
// the workspace, exactly as a rule using the readonly pattern already allows.
// A client that must not run such a command should not use this mode.
func AutoApproveCall(tool string, args json.RawMessage) bool {
	facts := tools.CallFactsFor(tool, args)
	switch {
	case facts.Command != "":
		// Only the shell carries a command. The analyzer's ReadOnly verdict is
		// the whole claim: "cannot prove" is not "harmless", and it asks.
		return shellcmd.Analyze(facts.Command, runtime.GOOS) == shellcmd.ReadOnly
	case len(facts.Paths) > 0:
		switch tool {
		case "write_file", "edit_file", "apply_patch":
			return true
		}
		return false
	}
	switch tool {
	case "run_tests", "build":
		return true
	}
	return false
}
