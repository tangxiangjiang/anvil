// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/skills"
)

func writeSkill(t *testing.T, root, id, fmBody string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fmBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAndFrontMatter(t *testing.T) {
	dir := t.TempDir()
	skillsRoot := filepath.Join(dir, ".anvil", "skills")
	writeSkill(t, skillsRoot, "commit-style", `---
name: commit-style
description: Conventional commits and changelog
---
Use feat/fix prefixes.
`)
	list, err := skills.Discover(dir, skills.DiscoverOpts{ScanProject: true})
	if err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	if list[0].Name != "commit-style" || !strings.Contains(list[0].Body, "feat/fix") {
		t.Fatalf("%+v", list[0])
	}
}

func TestRouterKeyword(t *testing.T) {
	all := []skills.Skill{
		{ID: "go-test", Name: "go-test", Description: "golang testing patterns testify"},
		{ID: "css", Name: "css", Description: "stylesheet layout"},
	}
	picked := skills.Router(all, nil, "please help with golang testify tests", 2, 6000)
	if len(picked) == 0 || picked[0].ID != "go-test" {
		t.Fatalf("%+v", picked)
	}
}

func TestResolveReferenceSandbox(t *testing.T) {
	dir := t.TempDir()
	sk := filepath.Join(dir, "skill")
	_ = os.MkdirAll(filepath.Join(sk, "references"), 0o755)
	_ = os.WriteFile(filepath.Join(sk, "references", "a.md"), []byte("ok"), 0o644)
	outside := filepath.Join(dir, "secret.txt")
	_ = os.WriteFile(outside, []byte("no"), 0o644)

	p, err := skills.ResolveReference(sk, "references/a.md")
	if err != nil || !strings.Contains(p, "a.md") {
		t.Fatalf("%v %s", err, p)
	}
	if _, err := skills.ResolveReference(sk, "../secret.txt"); err == nil {
		t.Fatal("expected deny")
	}
}

func TestFormatAlwaysCap(t *testing.T) {
	sk := skills.Skill{ID: "x", Name: "x", Body: strings.Repeat("a", 10000)}
	out := skills.FormatAlways([]skills.Skill{sk}, 500)
	if len([]rune(out)) > 520 {
		t.Fatalf("len=%d", len([]rune(out)))
	}
}
