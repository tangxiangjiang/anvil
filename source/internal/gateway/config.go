// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package gateway implements the Anvil model gateway (catalog + auth + OpenAI-compat proxy).
package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// UpstreamConfig describes one LLM HTTP backend.
type UpstreamConfig struct {
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env"`
}

// ModelRoute maps a public model id to an upstream.
type ModelRoute struct {
	ID            string   `json:"id"`
	Upstream      string   `json:"upstream"`
	UpstreamModel string   `json:"upstream_model"`
	Provider      string   `json:"provider"` // advisory: openai (default)
	Params        []string `json:"params,omitempty"`
	Vision        bool     `json:"vision,omitempty"` // advisory: model accepts image parts (0.19+)
	OwnedBy       string   `json:"owned_by,omitempty"`
}

// Config is anvil-gateway configuration (JSON).
type Config struct {
	Listen       string                    `json:"listen"`
	TokenEnv     string                    `json:"token_env"`
	DefaultModel string                    `json:"default_model,omitempty"`
	Upstreams    map[string]UpstreamConfig `json:"upstreams"`
	Models       []ModelRoute              `json:"models"`
}

// DefaultConfig returns defaults.
func DefaultConfig() Config {
	return Config{
		Listen:   "127.0.0.1:8080",
		TokenEnv: "ANVIL_GATEWAY_TOKEN",
		Upstreams: map[string]UpstreamConfig{},
	}
}

// LoadJSONFile loads and normalizes config.
func LoadJSONFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Normalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Normalize validates config.
func (c *Config) Normalize() error {
	if c.Listen == "" {
		return fmt.Errorf("listen is required")
	}
	if c.TokenEnv == "" {
		c.TokenEnv = "ANVIL_GATEWAY_TOKEN"
	}
	if len(c.Models) == 0 {
		return fmt.Errorf("models: at least one route is required")
	}
	if c.Upstreams == nil {
		c.Upstreams = map[string]UpstreamConfig{}
	}
	seen := map[string]struct{}{}
	for i := range c.Models {
		m := &c.Models[i]
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			return fmt.Errorf("models[%d]: id is required", i)
		}
		if _, ok := seen[m.ID]; ok {
			return fmt.Errorf("models: duplicate id %q", m.ID)
		}
		seen[m.ID] = struct{}{}
		if m.Upstream == "" {
			return fmt.Errorf("models[%d] (%s): upstream is required", i, m.ID)
		}
		up, ok := c.Upstreams[m.Upstream]
		if !ok {
			return fmt.Errorf("models[%d] (%s): unknown upstream %q", i, m.ID, m.Upstream)
		}
		if strings.TrimSpace(up.BaseURL) == "" {
			return fmt.Errorf("upstreams.%s: base_url is required", m.Upstream)
		}
		if strings.TrimSpace(up.APIKeyEnv) == "" {
			return fmt.Errorf("upstreams.%s: api_key_env is required", m.Upstream)
		}
		c.Upstreams[m.Upstream] = UpstreamConfig{
			BaseURL:   strings.TrimRight(up.BaseURL, "/"),
			APIKeyEnv: up.APIKeyEnv,
		}
		if m.UpstreamModel == "" {
			m.UpstreamModel = m.ID
		}
		if m.Provider == "" {
			m.Provider = "openai"
		}
		if m.OwnedBy == "" {
			m.OwnedBy = "gateway"
		}
	}
	if c.DefaultModel != "" {
		if _, ok := seen[c.DefaultModel]; !ok {
			return fmt.Errorf("default_model %q is not in models", c.DefaultModel)
		}
	}
	return nil
}

// Token returns the client bearer token from the environment.
func (c Config) Token() string {
	return os.Getenv(c.TokenEnv)
}

// ResolveModel returns the route for a public model id (or default_model if id empty).
func (c Config) ResolveModel(id string) (ModelRoute, UpstreamConfig, error) {
	if id == "" {
		id = c.DefaultModel
	}
	if id == "" {
		return ModelRoute{}, UpstreamConfig{}, fmt.Errorf("model id required")
	}
	for _, m := range c.Models {
		if m.ID == id {
			return m, c.Upstreams[m.Upstream], nil
		}
	}
	return ModelRoute{}, UpstreamConfig{}, fmt.Errorf("unknown model %q", id)
}
