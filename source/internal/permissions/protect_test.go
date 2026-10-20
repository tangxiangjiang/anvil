// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergeForTest compiles the layers the way a session does: user rules, then
// workspace rules, with the protected paths of a workspace rooted at dir.
func mergeForTest(t *testing.T, dir string, user, workspace []Rule) *Set {
	t.Helper()
	u, err := New(user)
	if err != nil {
		t.Fatalf("user rules: %v", err)
	}
	w, err := New(workspace)
	if err != nil {
		t.Fatalf("workspace rules: %v", err)
	}
	// New would source these to the caller's list; they are file rules here, so
	// put them back where a reader would look. An empty list compiles to no set
	// at all, which Merge already handles.
	if u != nil {
		for i := range u.rules {
			u.rules[i].source = SourceUser
		}
	}
	if w != nil {
		for i := range w.rules {
			w.rules[i].source = SourceWorkspace
		}
	}
	set := Merge(u, w)
	if set == nil {
		t.Fatal("merge of two rule lists must not be nil")
	}
	return set.Protect(Protection{Root: dir, Home: dir, AnvilHome: filepath.Join(dir, ".anvil")})
}

// Severity decides across scopes: a workspace allow cannot weaken a user deny,
// and the same is true of every other pair. Within one effect the user's file is
// first, because it is the person's own statement and the merge order is user,
// workspace, code.
func TestSeverityDecidesAcrossScopes(t *testing.T) {
	dir := t.TempDir()
	allow := Rule{Effect: "allow", Tool: "shell", Pattern: "readonly"}
	ask := Rule{Effect: "ask", Tool: "shell", Pattern: "readonly"}
	deny := Rule{Effect: "deny", Tool: "shell", Pattern: "readonly"}
	call := CallFacts{Tool: "shell", Command: "git status"}

	for _, tc := range []struct {
		name      string
		user      []Rule
		workspace []Rule
		want      Effect
		wantSrc   string
	}{
		{"user deny beats workspace allow", []Rule{deny}, []Rule{allow}, Deny, SourceUser},
		{"workspace deny beats user allow", []Rule{allow}, []Rule{deny}, Deny, SourceWorkspace},
		{"user deny beats workspace ask", []Rule{deny}, []Rule{ask}, Deny, SourceUser},
		{"workspace deny beats user ask", []Rule{ask}, []Rule{deny}, Deny, SourceWorkspace},
		{"user ask beats workspace allow", []Rule{ask}, []Rule{allow}, Ask, SourceUser},
		{"workspace ask beats user allow", []Rule{allow}, []Rule{ask}, Ask, SourceWorkspace},
		{"first allow across the merge order wins", []Rule{allow}, []Rule{{Effect: "allow", Tool: "*"}}, Allow, SourceUser},
		{"first ask across the merge order wins", []Rule{ask}, []Rule{{Effect: "ask", Tool: "*"}}, Ask, SourceUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := mergeForTest(t, dir, tc.user, tc.workspace).Evaluate(call, "linux")
			if !d.Matched || d.Effect != tc.want || d.Source != tc.wantSrc {
				t.Fatalf("Evaluate = %+v, want %s from %s", d, tc.want, tc.wantSrc)
			}
			// Every citation counts inside its own file. Both lists are one rule
			// long here, so a merged position would have been 2 for the workspace.
			if d.Index != 0 {
				t.Errorf("Index = %d, want 0 in the deciding file", d.Index)
			}
		})
	}
}

// A same-severity conflict keeps both rules: the merge does not drop the loser,
// because the manager has to show a person that two files disagree.
func TestSameSeverityKeepsBothRulesAndBothSources(t *testing.T) {
	dir := t.TempDir()
	set := mergeForTest(t, dir,
		[]Rule{{Effect: "ask", Tool: "shell", Pattern: "readonly", Reason: "person"}},
		[]Rule{{Effect: "ask", Tool: "shell", Pattern: "readonly", Reason: "repository"}},
	)
	rules := set.Rules()
	if len(rules) != 2 {
		t.Fatalf("Rules() = %+v, want both rules", rules)
	}
	if rules[0].Source != SourceUser || rules[0].Index != 1 || rules[1].Source != SourceWorkspace || rules[1].Index != 1 {
		t.Fatalf("provenance = %+v", rules)
	}
	// The decision still has one winner, and it says which file it came from.
	d := set.Evaluate(CallFacts{Tool: "shell", Command: "git status"}, "linux")
	if d.Source != SourceUser || d.Reason != "person" {
		t.Fatalf("Evaluate = %+v, want the user rule", d)
	}
}

// Protected paths are a fact about the call, and an allow rule never covers one
// whatever it says. Deny and ask still decide; with no rule left, the mode
// decides exactly as it would have without the allow.
func TestAnAllowNeverCoversAProtectedPath(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	allowAll := Rule{Effect: "allow", Tool: "write_file", Pattern: "**"}
	call := func(p string) CallFacts { return CallFacts{Tool: "write_file", Paths: []string{p}} }

	set := mergeForTest(t, dir, nil, []Rule{allowAll})
	set.Protect(Protection{Root: dir, Home: home, AnvilHome: filepath.Join(home, ".anvil")})

	protected := []string{
		".git/config",
		"sub/.git/HEAD",
		".anvil/checkpoints/run-1",
		filepath.Join(dir, ".git", "config"),
		filepath.Join(home, ".anvil", "permissions.json"),
	}
	for _, p := range protected {
		d := set.Evaluate(call(p), "linux")
		if d.Matched {
			t.Errorf("%q: the allow rule matched a protected path: %+v", p, d)
		}
		if len(d.Protected) != 1 || d.Protected[0] != p {
			t.Errorf("%q: Protected = %v, want the path as the call spelled it", p, d.Protected)
		}
	}

	// A path outside the list is covered as before.
	plain := filepath.Join(dir, "src", "a.go")
	if d := set.Evaluate(call(plain), "linux"); !d.Matched || d.Effect != Allow || len(d.Protected) != 0 {
		t.Errorf("%q: Evaluate = %+v, want the allow and no protected path", plain, d)
	}

	// Deny and ask are not weakened by protection: they are what decides when no
	// allow may.
	for _, effect := range []string{"deny", "ask"} {
		s := mergeForTest(t, dir, nil, []Rule{allowAll, {Effect: effect, Tool: "write_file", Pattern: "**"}})
		s.Protect(Protection{Root: dir, Home: home, AnvilHome: filepath.Join(home, ".anvil")})
		d := s.Evaluate(call(".git/config"), "linux")
		if !d.Matched || d.Effect.String() != effect {
			t.Errorf("%s: Evaluate = %+v, want %s", effect, d, effect)
		}
		if len(d.Protected) != 1 {
			t.Errorf("%s: Protected = %v", effect, d.Protected)
		}
	}
}

// The user file's globs add to the fixed list, and a glob is not a rule: it adds
// no entry to the rule list and changes no citation.
func TestUserGlobsAddToTheFixedList(t *testing.T) {
	dir := t.TempDir()
	set := mergeForTest(t, dir, nil, []Rule{{Effect: "allow", Tool: "write_file", Pattern: "**"}})
	set.Protect(Protection{Root: dir, Extra: []string{"secrets/**", "*.pem"}})

	for _, p := range []string{"secrets/token.txt", "a.pem"} {
		if d := set.Evaluate(CallFacts{Tool: "write_file", Paths: []string{p}}, "linux"); d.Matched {
			t.Errorf("%q: an allow matched a user-protected path: %+v", p, d)
		}
	}
	// A glob is anchored the way a rule's path pattern is: `secrets/**` is the
	// directory at the workspace root, and a person who means any depth says so.
	if d := set.Evaluate(CallFacts{Tool: "write_file", Paths: []string{"nested/secrets/id_rsa"}}, "linux"); !d.Matched {
		t.Errorf("an anchored glob must not reach into a nested directory: %+v", d)
	}
	if d := set.Evaluate(CallFacts{Tool: "write_file", Paths: []string{"secrets/token.txt"}}, "linux"); len(d.Protected) != 1 {
		t.Errorf("Protected = %v", d.Protected)
	}
	if got := len(set.Rules()); got != 1 {
		t.Errorf("Rules() = %d, want the one rule: a glob is not a rule", got)
	}
}

// A shell rule matches the command line, not paths. This is the honest limit of
// the protection: a command that names .git is still covered by an allow rule,
// because the card never showed those paths as files.
func TestShellRulesAreNotPathProtected(t *testing.T) {
	dir := t.TempDir()
	set := mergeForTest(t, dir, nil, []Rule{{Effect: "allow", Tool: "shell", Pattern: "readonly"}})
	set.Protect(Protection{Root: dir})
	d := set.Evaluate(CallFacts{Tool: "shell", Command: "cat .git/config"}, "linux")
	if !d.Matched || d.Effect != Allow {
		t.Fatalf("Evaluate = %+v, want the command rule: protection is about paths", d)
	}
}

// Each source is cited by the file a reader opens, and a position is counted
// inside it.
func TestRefNamesItsOwnSource(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{SourceUser, "user permissions.json rule 2"},
		{SourceWorkspace, ".anvil/permissions.json rule 2"},
		{SourceCode, "session rules rule 2"},
	} {
		d := Decision{Matched: true, Effect: Deny, Source: tc.source, Index: 1, Tool: "shell", Pattern: "rm -rf:*"}
		if got := d.Ref(); !strings.HasPrefix(got, tc.want+" ") {
			t.Errorf("Ref() = %q, want it to start %q", got, tc.want)
		}
	}
}

// The user file has its own shape. A repository may not add protected paths (it
// would be widening what it is allowed to change without asking), and a
// machine-wide file may not pick a session's starting mode.
func TestTheTwoFileShapesStaySeparate(t *testing.T) {
	if _, _, err := ParseUser([]byte(`{"defaultMode":"accept_edits"}`)); err == nil {
		t.Error("a user file must not accept defaultMode")
	}
	if _, err := Parse([]byte(`{"protected":["secrets/**"]}`)); err == nil {
		t.Error("a workspace file must not accept protected")
	}
	set, protected, err := ParseUser([]byte(`{"protected":[" secrets/** ",""],"rules":[{"effect":"allow","tool":"shell"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(protected) != 1 || protected[0] != "secrets/**" {
		t.Errorf("protected = %q, want the trimmed glob and no empty entry", protected)
	}
	if len(set.Rules()) != 1 || set.Rules()[0].Source != SourceUser || set.Rules()[0].Index != 1 {
		t.Errorf("Rules() = %+v", set.Rules())
	}
}

// A missing user file is no preferences, not an error; a malformed one names the
// path and the place, exactly as the workspace file does.
func TestLoadUserReportsWhatItFinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	set, protected, err := LoadUser(path)
	if err != nil || set != nil || protected != nil {
		t.Fatalf("missing file: %v %v %v", set, protected, err)
	}

	if err := os.WriteFile(path, []byte("{\n  \"version\": 1,\n  \"rules\": [{\"effect\": \"allow\", \"tool\": \"shell\", \"pattern\": [}]\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = LoadUser(path)
	if err == nil {
		t.Fatal("a malformed user file must be reported")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file", err)
	}
	if line, _ := Position(err); line == 0 {
		t.Errorf("error %q does not name the place", err)
	}
}

// The source composes the two files per read, user first. An edit to either is
// in force at the next call, and a user file that disappears withdraws only its
// own rules.
func TestSourceMergesTheUserFileFirstAndRereads(t *testing.T) {
	dir := t.TempDir()
	userDir := t.TempDir()
	userPath := filepath.Join(userDir, FileName)
	if err := os.WriteFile(userPath, []byte(`{"version":1,"rules":[{"effect":"deny","tool":"shell","pattern":"rm -rf:*"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`)

	src := NewSource(dir, userPath, dir, filepath.Join(dir, ".anvil"), nil, nil)
	set, err := src.Read()
	if err != nil {
		t.Fatal(err)
	}
	// The user deny holds over the workspace's allow of the same call.
	d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf build"}, "linux")
	if !d.Matched || d.Effect != Deny || d.Source != SourceUser || d.Index != 0 {
		t.Fatalf("Evaluate = %+v, want the user's deny", d)
	}
	// The workspace file still allows what the user did not deny.
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "git status"}, "linux"); d.Effect != Allow || d.Source != SourceWorkspace {
		t.Fatalf("Evaluate = %+v, want the workspace's allow", d)
	}

	// Removing the user file withdraws its deny without disturbing the workspace.
	if err := os.Remove(userPath); err != nil {
		t.Fatal(err)
	}
	set, err = src.Read()
	if err != nil {
		t.Fatal(err)
	}
	if d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf build"}, "linux"); d.Matched {
		t.Fatalf("Evaluate = %+v, want no rule after the user file is gone", d)
	}
}

// Merge takes the first declared mode and the first protection: the mode belongs
// to the workspace layer, whose set comes after the user's, so the first one
// found is the only one that exists in practice.
func TestMergeKeepsTheFirstModeAndProtection(t *testing.T) {
	mode := "accept_edits"
	w := &Set{defaultMode: mode, protection: &Protection{Root: "w"}}
	u := &Set{}
	got := Merge(u, w)
	if got == nil || got.DefaultMode() != mode || got.protection == nil || got.protection.Root != "w" {
		t.Fatalf("Merge = %+v", got)
	}
	if Merge(nil, nil) != nil {
		t.Error("merging nothing is no set at all, not an empty policy")
	}
}
