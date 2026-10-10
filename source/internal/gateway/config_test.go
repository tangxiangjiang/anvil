// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigNormalizeRequiresModels(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Normalize(); err == nil {
		t.Fatal("expected error")
	}
}

func TestConfigUnknownUpstream(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Models = []ModelRoute{{ID: "m", Upstream: "missing", UpstreamModel: "x"}}
	if err := cfg.Normalize(); err == nil {
		t.Fatal("expected unknown upstream")
	}
}

func TestConfigDuplicateModel(t *testing.T) {
	cfg := fixtureConfig()
	cfg.Models = append(cfg.Models, ModelRoute{ID: "demo", Upstream: "u1", UpstreamModel: "x"})
	if err := cfg.Normalize(); err == nil {
		t.Fatal("expected duplicate")
	}
}

func TestLoadJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gw.json")
	body := `{
  "listen":"127.0.0.1:0",
  "token_env":"ANVIL_GATEWAY_TOKEN",
  "upstreams":{"u1":{"base_url":"http://127.0.0.1:9/v1","api_key_env":"UP_KEY"}},
  "models":[{"id":"demo","upstream":"u1","upstream_model":"real","provider":"openai"}]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadJSONFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models[0].UpstreamModel != "real" {
		t.Fatalf("%+v", cfg.Models[0])
	}
}

func TestLoadYAMLFileParity(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "gw.json")
	yamlPath := filepath.Join(dir, "gw.yaml")
	jsonBody := `{
  "listen":"127.0.0.1:0",
  "token_env":"ANVIL_GATEWAY_TOKEN",
  "default_model":"demo",
  "upstreams":{"u1":{"base_url":"http://127.0.0.1:9/v1","api_key_env":"UP_KEY"}},
  "models":[{"id":"demo","upstream":"u1","upstream_model":"real","provider":"openai","vision":true}]
}`
	yamlBody := `listen: 127.0.0.1:0
token_env: ANVIL_GATEWAY_TOKEN
default_model: demo
upstreams:
  u1:
    base_url: http://127.0.0.1:9/v1
    api_key_env: UP_KEY
models:
  - id: demo
    upstream: u1
    upstream_model: real
    provider: openai
    vision: true
`
	if err := os.WriteFile(jsonPath, []byte(jsonBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := LoadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	y, err := LoadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if j.DefaultModel != y.DefaultModel || j.Models[0].UpstreamModel != y.Models[0].UpstreamModel || j.Models[0].Vision != y.Models[0].Vision {
		t.Fatalf("json=%+v yaml=%+v", j, y)
	}
	if j.Upstreams["u1"].BaseURL != y.Upstreams["u1"].BaseURL {
		t.Fatalf("upstream mismatch")
	}
}

func fixtureConfig() Config {
	cfg := DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.Upstreams = map[string]UpstreamConfig{
		"u1": {BaseURL: "http://127.0.0.1:9/v1", APIKeyEnv: "UP_KEY"},
	}
	cfg.Models = []ModelRoute{
		{ID: "demo", Upstream: "u1", UpstreamModel: "real-model", Provider: "openai", Params: []string{"temperature"}},
	}
	_ = cfg.Normalize()
	return cfg
}
