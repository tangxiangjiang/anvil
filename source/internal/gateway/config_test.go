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
