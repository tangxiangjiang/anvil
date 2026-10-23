// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

// Source is the rule files, read on demand rather than once.
//
// The files are the interface. A person edits them by hand while a session is
// open, and the editor's own approval card appends to the workspace file when
// somebody chooses "always allow"; a session that read them once would go on
// deciding by a policy the user has already changed, and the card's whole
// promise — that the choice it records is the choice in force — would be false
// until the next restart.
//
// So the set is composed per call from what the files hold now: the user file's
// rules first, then the workspace file's, then the caller's own rules, which are
// appended last for the same reason as at build time: within one effect the
// first rule decides, and the files have to be the part a reader sees on top.
type Source struct {
	// cwd is the workspace root, resolved once at construction so a working
	// directory that moves under the session cannot point the policy elsewhere.
	cwd string
	// userPath is the user file, empty when the caller asked for no user scope.
	userPath string
	// home resolves `~` in a protected-path glob; anvilHome is the directory
	// that is protected in full.
	home      string
	anvilHome string
	// code are the rules the caller supplied in Options.PermissionRules.
	code []Rule
	// last is the most recent set that read successfully. It is what the session
	// was built with until the first re-read succeeds.
	last *Set
}

// NewSource builds a reader over the workspace rule file at cwd and the user
// rule file at userPath (empty for none). home is what `~` expands to, and
// anvilHome is the user-level directory no allow rule may cover.
func NewSource(cwd, userPath, home, anvilHome string, code []Rule, initial *Set) *Source {
	return &Source{
		cwd:       cwd,
		userPath:  userPath,
		home:      home,
		anvilHome: anvilHome,
		code:      code,
		last:      initial,
	}
}

// Read returns the rules as the files hold them now.
//
// A file that has become unreadable — mistyped, half-saved, or gone — is
// reported with the set that last read successfully. The alternative is to
// refuse every call in the session because somebody is halfway through editing
// a policy file, which punishes the wrong person: the session already has a
// policy, and it is the one the file said a moment ago. Refusing a *malformed
// file at startup* stays a hard error; see Load.
// HasLast reports whether a read has ever succeeded, so there is a last good
// policy for a failed read to fall back on. Before the first success it is false.
func (s *Source) HasLast() bool {
	return s != nil && s.last != nil
}

func (s *Source) Read() (*Set, error) {
	if s == nil {
		return nil, nil
	}
	workspace, err := Load(s.cwd)
	if err != nil {
		return s.last, err
	}
	var user *Set
	var extra []string
	if s.userPath != "" {
		user, extra, err = LoadUser(s.userPath)
		if err != nil {
			return s.last, err
		}
	}
	code, err := New(s.code)
	if err != nil {
		// Unreachable in practice — the caller's rules compiled before — but a
		// policy that cannot be composed must not silently become "no policy".
		return s.last, err
	}
	merged := Merge(user, workspace, code)
	if merged != nil {
		merged.Protect(Protection{Root: s.cwd, Home: s.home, AnvilHome: s.anvilHome, Extra: extra})
	}
	s.last = merged
	return merged, nil
}
