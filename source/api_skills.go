// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/anvil-dev/anvil/internal/skills"
)

// SkillInfo is operator-facing metadata for one discovered skill package
// (0.14+). It describes what can be selected; it is not what was injected.
type SkillInfo struct {
	ID          string
	Name        string
	Description string
	// Path is the skill's SKILL.md, workspace-relative when the skill lives
	// under CWD. Empty for a skill discovered under a directory outside it,
	// because a caller has no way to name that path from the workspace.
	Path string
	// Always reports that .anvil/skills.json marks this skill for injection
	// into every session, so it needs no selection.
	Always bool
}

// SkillListOptions selects the skill roots to scan.
type SkillListOptions struct {
	// CWD is the project root. Empty means ".".
	CWD string
	// SkillDirs are extra directories containing skill packages. A relative
	// entry is resolved against CWD.
	SkillDirs []string
	// ScanUser includes ~/.anvil/skills. Off by default: a listing should not
	// walk outside the project unless it was asked to.
	ScanUser bool
}

// ListSkills returns the skill packages discoverable under the given roots.
//
// This is the *catalogue*, not the session's selection. A skill listed here is
// used by a session only when Options.Skills names its id, or when
// .anvil/skills.json lists it as always-on. Listing is deliberately wider than
// loading: an editor has to show what is available before a session exists.
func ListSkills(ctx context.Context, opts SkillListOptions) ([]SkillInfo, error) {
	_ = ctx
	cwd := opts.CWD
	if cwd == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, newError("resolve cwd", false, err)
	}
	found, err := skills.Discover(abs, skills.DiscoverOpts{
		SkillDirs:   opts.SkillDirs,
		ScanProject: true,
		ScanUser:    opts.ScanUser,
	})
	if err != nil {
		return nil, newError("discover skills", false, err)
	}
	always := map[string]bool{}
	for _, id := range skills.LoadAlwaysIDs(abs) {
		always[id] = true
	}
	out := make([]SkillInfo, 0, len(found))
	for _, sk := range found {
		out = append(out, SkillInfo{
			ID:          sk.ID,
			Name:        displayName(sk),
			Description: sk.Description,
			Path:        relativeTo(abs, filepath.Join(sk.Dir, "SKILL.md")),
			Always:      always[sk.ID],
		})
	}
	return out, nil
}

// displayName falls back to the id: a skill without a `name:` in its front
// matter still has to be callable, and an empty label in a picker is worse than
// a repeated one.
func displayName(sk skills.Skill) string {
	if strings.TrimSpace(sk.Name) != "" {
		return sk.Name
	}
	return sk.ID
}

// relativeTo renders path relative to root, or "" when it is not under it.
func relativeTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}
