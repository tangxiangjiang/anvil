// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OverloadPolicy is what happens when all workers are busy.
type OverloadPolicy string

const (
	OverloadReject OverloadPolicy = "reject"
	// OverloadQueue reserved; 0.11 MVP uses reject only.
)

// RepoEntry maps a stable repo id to an absolute cwd.
type RepoEntry struct {
	ID  string `json:"id"`
	CWD string `json:"cwd"`
}

// ClusterConfig enables the optional cluster edge (p_0.11.4).
type ClusterConfig struct {
	Enabled      bool   `json:"enabled"`
	URL          string `json:"url"` // e.g. tcp://127.0.0.1:17777
	NodeID       string `json:"node_id"`
	TokenEnv     string `json:"token_env"` // default ANVIL_CLUSTER_TOKEN
	HeartbeatSec int    `json:"heartbeat_sec"`
}

// Config is agentd configuration (JSON file + flags).
type Config struct {
	// Listen is a unix socket path, or tcp://host:port for loopback testing.
	Listen string `json:"listen"`
	// TokenEnv is the env var holding the controller auth token (default ANVIL_AGENTD_TOKEN).
	TokenEnv string `json:"token_env"`
	// MaxWorkers is concurrent job capacity (required >= 1).
	MaxWorkers int `json:"max_workers"`
	// Overload is reject (default) when at capacity.
	Overload OverloadPolicy `json:"overload"`
	// WorkerBin is the agent-worker executable; empty = embedded runner.
	WorkerBin string `json:"worker_bin"`
	// Repos is the cwd allowlist.
	Repos []RepoEntry `json:"repos"`
	// CancelGrace is how long to wait after cancel before force-kill (worker process).
	CancelGrace time.Duration `json:"-"`
	// CancelGraceSec is JSON-friendly seconds for CancelGrace.
	CancelGraceSec int `json:"cancel_grace_sec"`
	// Cluster optional edge.
	Cluster ClusterConfig `json:"cluster"`
	// NodeID labels this daemon in hello_ok (defaults to hostname).
	NodeID string `json:"node_id"`
}

// DefaultConfig returns sensible defaults (listen path filled by caller/platform).
func DefaultConfig() Config {
	return Config{
		TokenEnv:       "ANVIL_AGENTD_TOKEN",
		MaxWorkers:     2,
		Overload:       OverloadReject,
		CancelGraceSec: 5,
		Cluster: ClusterConfig{
			TokenEnv:     "ANVIL_CLUSTER_TOKEN",
			HeartbeatSec: 10,
		},
	}
}

// LoadJSONFile reads config from path.
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

// Normalize validates and fills defaults.
func (c *Config) Normalize() error {
	if c.TokenEnv == "" {
		c.TokenEnv = "ANVIL_AGENTD_TOKEN"
	}
	if c.MaxWorkers < 1 {
		return fmt.Errorf("max_workers must be >= 1, got %d", c.MaxWorkers)
	}
	if c.Overload == "" {
		c.Overload = OverloadReject
	}
	if c.Overload != OverloadReject {
		return fmt.Errorf("overload %q not supported in 0.11 (use reject)", c.Overload)
	}
	if c.Listen == "" {
		return fmt.Errorf("listen is required (unix path or tcp://host:port)")
	}
	if c.CancelGraceSec > 0 {
		c.CancelGrace = time.Duration(c.CancelGraceSec) * time.Second
	} else {
		c.CancelGrace = 5 * time.Second
	}
	if c.Cluster.TokenEnv == "" {
		c.Cluster.TokenEnv = "ANVIL_CLUSTER_TOKEN"
	}
	if c.Cluster.HeartbeatSec <= 0 {
		c.Cluster.HeartbeatSec = 10
	}
	seen := make(map[string]struct{})
	for i := range c.Repos {
		r := &c.Repos[i]
		r.ID = strings.TrimSpace(r.ID)
		if r.ID == "" {
			return fmt.Errorf("repos[%d]: id is required", i)
		}
		if _, ok := seen[r.ID]; ok {
			return fmt.Errorf("repos: duplicate id %q", r.ID)
		}
		seen[r.ID] = struct{}{}
		if r.CWD == "" {
			return fmt.Errorf("repos[%d] (%s): cwd is required", i, r.ID)
		}
		abs, err := filepath.Abs(r.CWD)
		if err != nil {
			return fmt.Errorf("repos[%d] (%s): abs cwd: %w", i, r.ID, err)
		}
		r.CWD = abs
		if !filepath.IsAbs(r.CWD) {
			return fmt.Errorf("repos[%d] (%s): cwd must be absolute", i, r.ID)
		}
	}
	if c.NodeID == "" {
		h, _ := os.Hostname()
		c.NodeID = h
	}
	return nil
}

// ResolveRepo returns the absolute cwd for repoID.
func (c Config) ResolveRepo(repoID string) (string, error) {
	for _, r := range c.Repos {
		if r.ID == repoID {
			return r.CWD, nil
		}
	}
	return "", fmt.Errorf("unknown repo_id %q", repoID)
}

// Token returns the controller auth token from the environment.
func (c Config) Token() string {
	return os.Getenv(c.TokenEnv)
}

// ClusterToken returns the cluster auth token.
func (c Config) ClusterToken() string {
	return os.Getenv(c.Cluster.TokenEnv)
}
