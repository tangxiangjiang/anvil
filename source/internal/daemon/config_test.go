// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/worker"
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

func TestLoadYAMLFileWithLLM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentd.yaml")
	body := "listen: tcp://127.0.0.1:19877\nmax_workers: 2\nrepos:\n  - id: demo\n    cwd: " + filepath.ToSlash(dir) + "\nllm:\n  base_url: http://127.0.0.1:8080/v1\n  model: private/demo\n  provider: openai\n  max_steps: 64\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.BaseURL != "http://127.0.0.1:8080/v1" || cfg.LLM.Model != "private/demo" || cfg.LLM.MaxSteps != 64 {
		t.Fatalf("llm=%+v", cfg.LLM)
	}
	if cfg.LLM.APIKeyEnv != "ANVIL_API_KEY" {
		t.Fatalf("api_key_env default=%q", cfg.LLM.APIKeyEnv)
	}
}

func TestApplyLLMDefaultsFillsBaseURL(t *testing.T) {
	t.Setenv("ANVIL_BASE_URL", "")
	t.Setenv("ANVIL_MODEL", "")
	t.Setenv("ANVIL_PROVIDER", "")
	t.Setenv("ANVIL_MAX_STEPS", "")
	opts := worker.OptionsFromEnv()
	applyLLMDefaults(&opts, LLMConfig{
		BaseURL:  "http://gw.local/v1",
		Model:    "m1",
		Provider: "anthropic",
		MaxSteps: 10,
	})
	if opts.BaseURL != "http://gw.local/v1" || opts.Model.ID != "m1" || opts.MaxSteps != 10 {
		t.Fatalf("%+v", opts)
	}
	if string(opts.Provider) != "anthropic" {
		t.Fatalf("provider=%s", opts.Provider)
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
