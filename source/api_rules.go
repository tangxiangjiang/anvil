// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"path/filepath"

	"github.com/anvil-dev/anvil/internal/rules"
)

// RuleFile is one project rule file, described as the system prompt sees it.
//
// The flags come from the same pass that builds the prompt text, so this list
// cannot disagree with what the model was given: Included is false for a file
// the character budget never reached, and Truncated is true for the one file
// that was cut to fit.
type RuleFile struct {
	// Path is workspace-relative — "AGENTS.md", ".anvil/rules/x.md" — which is
	// also the heading the prompt writes above it.
	Path string
	// Bytes is the size of the file's body after trimming.
	Bytes int
	// Included reports whether the body reached the system prompt.
	Included bool
	// Truncated reports that the body was cut short by the character budget.
	Truncated bool
}

// RuleListOptions selects which rule layers to report.
type RuleListOptions struct {
	// CWD is the project root. Empty means ".".
	CWD string
	// LoadProject enables AGENTS.md and .anvil/rules/*.md.
	LoadProject bool
	// LoadCursor enables .cursor/rules/*.
	LoadCursor bool
}

// ListRuleFiles reports the rule files that would be loaded for the given
// layers. It reads them, because "empty" and "unreadable" have to be decided
// the same way the prompt decides them.
func ListRuleFiles(ctx context.Context, opts RuleListOptions) ([]RuleFile, error) {
	_ = ctx
	cwd := opts.CWD
	if cwd == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, newError("resolve cwd", false, err)
	}
	found, err := rules.Discover(abs, rules.Options{
		LoadProject: opts.LoadProject,
		LoadCursor:  opts.LoadCursor,
	})
	if err != nil {
		return nil, newError("load project rules", false, err)
	}
	out := make([]RuleFile, 0, len(found))
	for _, f := range found {
		out = append(out, RuleFile{
			Path:      f.Label,
			Bytes:     f.Bytes,
			Included:  f.Included,
			Truncated: f.Truncated,
		})
	}
	return out, nil
}
