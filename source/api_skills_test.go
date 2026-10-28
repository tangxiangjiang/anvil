// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeSkillPackage(t *testing.T, root, id, content string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListSkillsReportsWhatTheWorkspaceOffers(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".anvil", "skills")
	writeSkillPackage(t, root, "commit-style", "---\nname: Commit style\ndescription: How commits are written here.\n---\n\nImperative mood.\n")
	writeSkillPackage(t, root, "release-notes", "No front matter at all.\n")

	found, err := ListSkills(context.Background(), SkillListOptions{CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("want two skills, got %+v", found)
	}
	if found[0].ID != "commit-style" || found[0].Name != "Commit style" {
		t.Fatalf("unexpected first skill: %+v", found[0])
	}
	if found[0].Description != "How commits are written here." {
		t.Fatalf("front matter was not read: %+v", found[0])
	}
	if found[0].Path != ".anvil/skills/commit-style/SKILL.md" {
		t.Fatalf("path should be workspace-relative: %q", found[0].Path)
	}
	if found[0].Always {
		t.Fatalf("nothing marked this always-on: %+v", found[0])
	}
	// A skill with no `name:` still has to be callable, so it falls back to the
	// directory name rather than appearing blank in a list.
	if found[1].Name != "release-notes" {
		t.Fatalf("a nameless skill should fall back to its id: %+v", found[1])
	}
}

func TestListSkillsReadsTheAlwaysList(t *testing.T) {
	dir := t.TempDir()
	writeSkillPackage(t, filepath.Join(dir, ".anvil", "skills"), "commit-style", "Imperative mood.\n")
	writeSkillPackage(t, filepath.Join(dir, ".anvil", "skills"), "release-notes", "Summarize.\n")
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "skills.json"),
		[]byte(`{"always":["release-notes"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := ListSkills(context.Background(), SkillListOptions{CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, sk := range found {
		want := sk.ID == "release-notes"
		if sk.Always != want {
			t.Fatalf("%s: always=%v want %v", sk.ID, sk.Always, want)
		}
	}
}

// A skill reached through an extra directory is still listed, but it has no
// workspace-relative name, and inventing one would hand a caller a path that
// does not resolve.
func TestListSkillsLeavesOutsidePathsEmpty(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeSkillPackage(t, outside, "shared", "Shared across projects.\n")

	found, err := ListSkills(context.Background(), SkillListOptions{CWD: dir, SkillDirs: []string{outside}})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "shared" {
		t.Fatalf("extra dirs were not scanned: %+v", found)
	}
	if found[0].Path != "" {
		t.Fatalf("a skill outside the workspace should have no relative path: %q", found[0].Path)
	}
}
