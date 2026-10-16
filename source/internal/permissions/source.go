// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

// Source is the workspace rule file, read on demand rather than once.
//
// The file is the interface. A person edits it by hand while a session is
// open, and the editor's own approval card appends to it when somebody chooses
// "always allow"; a session that read it once would go on deciding by a policy
// the user has already changed, and the card's whole promise — that the choice
// it records is the choice in force — would be false until the next restart.
//
// So the set is composed per call from what the file holds now plus the
// caller's own rules, which are appended after the file's for the same reason
// as at build time: within one effect the first rule decides, and the file has
// to be the part a reader of the file sees on top.
type Source struct {
	// cwd is the workspace root, resolved once at construction so a working
	// directory that moves under the session cannot point the policy elsewhere.
	cwd string
	// code are the rules the caller supplied in Options.PermissionRules.
	code []Rule
	// last is the most recent set that read successfully. It is what the session
	// was built with until the first re-read succeeds.
	last *Set
}

// NewSource builds a reader over cwd's rule file. initial is the policy the
// session starts under — what a read that fails falls back to — and code are
// the caller's own rules, appended after the file's.
func NewSource(cwd string, code []Rule, initial *Set) *Source {
	return &Source{cwd: cwd, code: code, last: initial}
}

// Read returns the rules as the file holds them now.
//
// A file that has become unreadable — mistyped, half-saved, or gone — is
// reported with the set that last read successfully. The alternative is to
// refuse every call in the session because somebody is halfway through editing
// a policy file, which punishes the wrong person: the session already has a
// policy, and it is the one the file said a moment ago. Refusing a *malformed
// file at startup* stays a hard error; see Load.
func (s *Source) Read() (*Set, error) {
	if s == nil {
		return nil, nil
	}
	set, err := Load(s.cwd)
	if err != nil {
		return s.last, err
	}
	merged, mergeErr := New(append(set.Rules(), s.code...))
	if mergeErr != nil {
		// Unreachable in practice — both halves compiled before — but a policy
		// that cannot be composed must not silently become "no policy".
		return s.last, mergeErr
	}
	s.last = merged
	return merged, nil
}
