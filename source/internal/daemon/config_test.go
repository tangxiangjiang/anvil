// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigNormalizeRejectsZeroWorkers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:0"
	cfg.MaxWorkers = 0
	if err := cfg.Normalize(); err == nil {
		t.Fatal("expected error for max_workers=0")
	}
}

func TestConfigNormalizeRequiresAbsoluteCWD(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:9"
	cfg.Repos = []RepoEntry{{ID: "r1", CWD: dir}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cfg.Repos[0].CWD) {
		t.Fatalf("cwd not abs: %s", cfg.Repos[0].CWD)
	}
}

func TestConfigDuplicateRepoID(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:9"
	cfg.Repos = []RepoEntry{
		{ID: "same", CWD: dir},
		{ID: "same", CWD: dir},
	}
	if err := cfg.Normalize(); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestConfigEmptyRepoCWD(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:9"
	cfg.Repos = []RepoEntry{{ID: "r1", CWD: ""}}
	if err := cfg.Normalize(); err == nil {
		t.Fatal("expected empty cwd error")
	}
}

func TestLoadJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentd.json")
	body := `{"listen":"tcp://127.0.0.1:19876","max_workers":3,"repos":[{"id":"demo","cwd":` + jsonQuote(dir) + `}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadJSONFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxWorkers != 3 {
		t.Fatalf("max_workers=%d", cfg.MaxWorkers)
	}
	cwd, err := cfg.ResolveRepo("demo")
	if err != nil || cwd != filepath.Clean(dir) && cwd != dir {
		// Abs may clean; compare Abs
		want, _ := filepath.Abs(dir)
		if cwd != want {
			t.Fatalf("resolve=%q want=%q err=%v", cwd, want, err)
		}
	}
}

func jsonQuote(s string) string {
	b, _ := MarshalBody(s)
	// MarshalBody of string includes quotes
	return string(b)
}

func TestResolveUnknownRepo(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:9"
	_ = cfg.Normalize()
	if _, err := cfg.ResolveRepo("nope"); err == nil {
		t.Fatal("expected unknown repo")
	}
}
