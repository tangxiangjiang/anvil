// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"path/filepath"
	"strings"
)

// Protection is what an allow rule may never cover.
//
// The point is not that these paths cannot be written — the mode still decides
// that, as it does for every other path. The point is that a remembered
// "always allow" cannot silently include them: an allow rule is skipped when a
// call touches one, so the call falls through to the mode and, when the mode
// asks, to a person.
//
// The fixed list lives in protectedRelative: any `.git` segment, the
// workspace's `.anvil/checkpoints/`, and the Anvil home directory. A user file
// may add globs of its own through Extra.
type Protection struct {
	// Root is the workspace root that relative paths are resolved against.
	Root string
	// Home is the user's home directory, what a `~` in a glob or path expands
	// to. Empty means a `~` is left as written and matches nothing.
	Home string
	// AnvilHome is the directory holding user-level Anvil state, protected in
	// full.
	AnvilHome string
	// Extra are the user file's protected-path globs, matched against the path
	// in the form a path rule uses.
	Extra []string
}

// Protect makes protected paths matter to this set. It is called by the code
// that builds a set for a workspace; a set with no Protection protects nothing,
// which is the right answer for a rule list compiled in isolation.
func (s *Set) Protect(p Protection) *Set {
	if s == nil {
		return nil
	}
	s.protection = &p
	return s
}

// protectedPaths reports the paths of a call that an allow rule may not cover,
// in the order the call named them. It reports the paths as the call spelled
// them, because that is what the card and the result show.
func (s *Set) protectedPaths(f CallFacts) []string {
	if s == nil || s.protection == nil || len(f.Paths) == 0 {
		return nil
	}
	var out []string
	for _, p := range f.Paths {
		if s.protection.isProtected(p) {
			out = append(out, p)
		}
	}
	return out
}

// isProtected reports whether one path is one an allow rule may not cover.
func (p Protection) isProtected(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	expanded := p.expandHome(s)
	if filepath.IsAbs(expanded) {
		abs := filepath.ToSlash(filepath.Clean(expanded))
		if p.AnvilHome != "" {
			home := filepath.ToSlash(filepath.Clean(p.AnvilHome))
			if abs == home || strings.HasPrefix(abs, home+"/") {
				return true
			}
		}
		if p.Root != "" {
			if rel, err := filepath.Rel(p.Root, filepath.FromSlash(abs)); err == nil &&
				rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return p.protectedRelative(cleanPath(filepath.ToSlash(rel)))
			}
		}
		return p.protectedRelative(cleanPath(abs))
	}
	return p.protectedRelative(cleanPath(expanded))
}

// protectedRelative applies the fixed list and the user's globs to a path in
// the form a path rule uses: `/` separators, no leading `./`.
func (p Protection) protectedRelative(n string) bool {
	if n == "" {
		return false
	}
	for _, segment := range strings.Split(n, "/") {
		if segment == ".git" {
			return true
		}
	}
	if n == ".anvil/checkpoints" || strings.HasPrefix(n, ".anvil/checkpoints/") {
		return true
	}
	for _, g := range p.Extra {
		g = cleanPath(g)
		if g == "" {
			continue
		}
		if matchGlob(g, n) {
			return true
		}
	}
	return false
}

// expandHome turns a leading `~` into the home directory. A path without one is
// returned as written.
func (p Protection) expandHome(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") && !strings.HasPrefix(s, `~\`) {
		return s
	}
	if p.Home == "" {
		return s
	}
	rest := strings.TrimLeft(s[1:], `/\`)
	if rest == "" {
		return p.Home
	}
	return filepath.Join(p.Home, rest)
}

// Merge composes sets in the order they are given. For a workspace that is user
// rules, then workspace rules, then code rules: severity still decides across
// the whole set, and within one effect the first rule of that order wins.
//
// Each rule keeps the source and position it was compiled with, so a citation
// still names a line in the file the reader has to open. The first non-empty
// default mode survives, and so does the first protection.
func Merge(sets ...*Set) *Set {
	var out *Set
	for _, s := range sets {
		if s == nil {
			continue
		}
		if out == nil {
			out = &Set{}
		}
		if out.defaultMode == "" {
			out.defaultMode = s.defaultMode
		}
		if out.protection == nil && s.protection != nil {
			out.protection = s.protection
		}
		out.rules = append(out.rules, s.rules...)
	}
	// A layer with no rules is still a layer: a file that only picks a starting
	// mode, or only adds protected globs, says something and must survive the
	// merge. Only nothing at all is nil.
	if out == nil || (len(out.rules) == 0 && out.defaultMode == "" && out.protection == nil) {
		return nil
	}
	return out
}
