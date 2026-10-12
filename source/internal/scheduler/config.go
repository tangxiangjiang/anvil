// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"fmt"
	"os"
	"strings"

	"github.com/anvil-dev/anvil/internal/configfile"
	"github.com/anvil-dev/anvil/internal/daemon"
)

// Config is anvil-scheduler configuration (JSON or YAML).
type Config struct {
	ListenApp      string       `json:"listen_app" yaml:"listen_app"`           // HTTP e.g. 127.0.0.1:17800
	ListenCluster  string       `json:"listen_cluster" yaml:"listen_cluster"`   // tcp://127.0.0.1:17777
	TokenEnv       string       `json:"token_env" yaml:"token_env"`
	WebSessionsDB  string       `json:"web_sessions_db" yaml:"web_sessions_db"` // optional: same path as anvil-web db_path
	Name           string       `json:"name" yaml:"name"`
	Description    string       `json:"description" yaml:"description"`
	StaleSec       int          `json:"heartbeat_stale_sec" yaml:"heartbeat_stale_sec"`
	ModelsCacheSec int          `json:"models_cache_sec" yaml:"models_cache_sec"`
	LLM            LLMBootstrap `json:"llm" yaml:"llm"`
}

// LLMBootstrap is optional file/env seed for llm (app may overwrite via PUT /v1/llm).
type LLMBootstrap struct {
	BaseURL   string `json:"base_url" yaml:"base_url"`
	APIKeyEnv string `json:"api_key_env" yaml:"api_key_env"`
	APIKey    string `json:"-" yaml:"-"` // filled from env at Normalize
	Model     string `json:"model" yaml:"model"`
	Provider  string `json:"provider" yaml:"provider"`
	MaxSteps  int    `json:"max_steps" yaml:"max_steps"`
}

// DefaultConfig returns defaults.
func DefaultConfig() Config {
	return Config{
		ListenApp:      "127.0.0.1:17800",
		ListenCluster:  "tcp://127.0.0.1:17777",
		TokenEnv:       "ANVIL_SCHEDULER_TOKEN",
		StaleSec:       30,
		ModelsCacheSec: 15,
		LLM: LLMBootstrap{
			APIKeyEnv: "ANVIL_GATEWAY_TOKEN",
			Provider:  "openai",
		},
	}
}

// LoadFile loads JSON or YAML and normalizes.
func LoadFile(path string) (Config, error) {
	cfg := DefaultConfig()
	if err := configfile.Load(path, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Normalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Normalize validates and fills defaults.
func (c *Config) Normalize() error {
	if c.TokenEnv == "" {
		c.TokenEnv = "ANVIL_SCHEDULER_TOKEN"
	}
	if strings.TrimSpace(c.ListenApp) == "" {
		return fmt.Errorf("listen_app is required")
	}
	if strings.TrimSpace(c.ListenCluster) == "" {
		return fmt.Errorf("listen_cluster is required")
	}
	if c.StaleSec <= 0 {
		c.StaleSec = 30
	}
	if c.ModelsCacheSec <= 0 {
		c.ModelsCacheSec = 15
	}
	if c.LLM.APIKeyEnv == "" {
		c.LLM.APIKeyEnv = "ANVIL_GATEWAY_TOKEN"
	}
	if c.LLM.APIKey == "" {
		c.LLM.APIKey = os.Getenv(c.LLM.APIKeyEnv)
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = "openai"
	}
	return nil
}

// Token returns the shared app/cluster bearer from the environment.
func (c Config) Token() string {
	return os.Getenv(c.TokenEnv)
}

// BootstrapLLM returns the initial cluster llm body from file/env seed.
func (c Config) BootstrapLLM() daemon.ClusterLLMBody {
	return daemon.ClusterLLMBody{
		BaseURL:  c.LLM.BaseURL,
		APIKey:   c.LLM.APIKey,
		Model:    c.LLM.Model,
		Provider: c.LLM.Provider,
		MaxSteps: c.LLM.MaxSteps,
	}
}
