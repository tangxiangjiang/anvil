// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/rules"
)

func writeRule(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListRuleFilesMatchesWhatLoadInjects(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, filepath.Join(dir, "AGENTS.md"), "Use tabs.\n")
	writeRule(t, filepath.Join(dir, ".anvil", "rules", "style.md"), "Prefer early returns.\n")
	writeRule(t, filepath.Join(dir, ".cursor", "rules", "layout.mdc"), "Go stays under source/.\n")

	files, err := ListRuleFiles(context.Background(), RuleListOptions{
		CWD:         dir,
		LoadProject: true,
		LoadCursor:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
		if !f.Included || f.Truncated || f.Bytes <= 0 {
			t.Fatalf("%s should have been included whole: %+v", f.Path, f)
		}
	}
	want := []string{"AGENTS.md", ".anvil/rules/style.md", ".cursor/rules/layout.mdc"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths=%v want %v", paths, want)
	}

	// The list is only useful if it describes the prompt. Every reported file
	// has to be named by the text Load produces, and vice versa.
	text, err := rules.Load(dir, rules.Options{LoadProject: true, LoadCursor: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if !strings.Contains(text, "## "+path) {
			t.Fatalf("Load did not inject %s:\n%s", path, text)
		}
	}
}

func TestListRuleFilesReportsTheBudget(t *testing.T) {
	dir := t.TempDir()
	// One file larger than the whole rules budget: it is cut, and everything
	// after it is found but never reaches the prompt.
	big := strings.Repeat("x", 30*1024)
	writeRule(t, filepath.Join(dir, "AGENTS.md"), big)
	writeRule(t, filepath.Join(dir, ".anvil", "rules", "later.md"), "This one never fits.\n")

	files, err := ListRuleFiles(context.Background(), RuleListOptions{CWD: dir, LoadProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want both files reported, got %+v", files)
	}
	if !files[0].Included || !files[0].Truncated {
		t.Fatalf("the big file should be included but truncated: %+v", files[0])
	}
	if files[1].Included || files[1].Truncated {
		t.Fatalf("the file past the budget should be reported as unused: %+v", files[1])
	}

	text, err := rules.Load(dir, rules.Options{LoadProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "This one never fits") {
		t.Fatal("Load injected a file the report called unused")
	}
}

func TestListRuleFilesIsEmptyWithoutALayer(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, filepath.Join(dir, "AGENTS.md"), "Use tabs.\n")
	files, err := ListRuleFiles(context.Background(), RuleListOptions{CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("no layer was asked for, got %+v", files)
	}
}
