// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"path/filepath"
	"testing"
)

// Holds on a case-insensitive filesystem (NTFS, the default macOS volume), where
// ".GIT" and ".git" name the same directory; on Linux they are different paths.
// audit #11: the .git segment and the .anvil/checkpoints prefix are compared
// byte-for-byte, so a differently cased path is not protected. NTFS is case
// insensitive, so on Windows the same directory is reached under either case.
func TestAuditProtectedPathIsCaseInsensitive(t *testing.T) {
	t.Skip("audit: open, see audit.md #11")
	p := Protection{Root: "/ws"}
	for _, rel := range []string{".GIT/config", ".ANVIL/checkpoints/x"} {
		if !p.protectedRelative(rel) {
			t.Errorf("%q is not protected, but the same directory is on a case-insensitive filesystem", rel)
		}
	}
}

// audit #10: a shell call carries Command and no Paths, so protectedPaths returns
// nothing and an allow rule for "rm" covers a command that deletes .git.
func TestAuditShellAllowDoesNotCoverProtectedPaths(t *testing.T) {
	t.Skip("audit: open, see audit.md #10")
	dir := t.TempDir()
	set, err := New([]Rule{{Effect: "allow", Tool: "shell", Pattern: "rm:*"}})
	if err != nil {
		t.Fatal(err)
	}
	set.Protect(Protection{Root: dir, Home: dir, AnvilHome: filepath.Join(dir, ".anvil")})
	d := set.Evaluate(CallFacts{Tool: "shell", Command: "rm -rf .git"}, "linux")
	if d.Matched && d.Effect == Allow {
		t.Fatalf("an allow rule for rm covered a command that deletes .git: %+v", d)
	}
}
