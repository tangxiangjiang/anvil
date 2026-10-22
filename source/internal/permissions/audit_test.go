// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"os"
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

// audit #12: when the workspace root is a symlink, an absolute path given in its
// resolved form does not Rel() against the root, so the protected-path test falls
// back to comparing the whole absolute string and misses .anvil/checkpoints.
func TestAuditProtectionFollowsSymlinkedRoot(t *testing.T) {
	t.Skip("audit: open, see audit.md #12")
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ws-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	p := Protection{Root: link}
	inside := filepath.Join(real, ".anvil", "checkpoints", "x")
	if !p.isProtected(filepath.ToSlash(inside)) {
		t.Fatalf("%s is not protected though the root is %s", inside, link)
	}
}
