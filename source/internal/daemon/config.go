// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/configfile"
	"github.com/anvil-dev/anvil/internal/plugin"
)

func pluginPolicyFrom(c PluginsConfig) *plugin.Policy {
	return &plugin.Policy{
		Available: append([]string(nil), c.Available...),
		Deny:      append([]string(nil), c.Deny...),
	}
}

// OverloadPolicy is what happens when all workers are busy.
type OverloadPolicy string

const (
	OverloadReject OverloadPolicy = "reject"
	// OverloadQueue reserved; 0.11 MVP uses reject only.
)

// RepoEntry maps a stable repo id to an absolute cwd.
type RepoEntry struct {
	ID  string `json:"id" yaml:"id"`
	CWD string `json:"cwd" yaml:"cwd"`
}

// ClusterConfig enables the optional cluster edge (p_0.11.4).
type ClusterConfig struct {
	Enabled      bool   `json:"enabled" yaml:"enabled"`
	URL          string `json:"url" yaml:"url"` // e.g. tcp://127.0.0.1:17777
	NodeID       string `json:"node_id" yaml:"node_id"`
	TokenEnv     string `json:"token_env" yaml:"token_env"` // default ANVIL_CLUSTER_TOKEN
	HeartbeatSec int    `json:"heartbeat_sec" yaml:"heartbeat_sec"`
	// WorkspaceToken admits this Dev Node into a Clusters Workspace (0.34+).
	WorkspaceToken string `json:"workspace_token" yaml:"workspace_token"`
}

// LLMConfig holds defaults injected into embedded workers (0.23+).
// File configs use APIKeyEnv; scheduler may set APIKey in-memory (0.24+).
// agentd does not proxy LLM HTTP.
type LLMConfig struct {
	BaseURL   string `json:"base_url" yaml:"base_url"`
	APIKeyEnv string `json:"api_key_env" yaml:"api_key_env"` // default ANVIL_API_KEY
	// APIKey is provisioned by the scheduler (not loaded from YAML).
	APIKey   string `json:"-" yaml:"-"`
	Model    string `json:"model" yaml:"model"`
	Provider string `json:"provider" yaml:"provider"`
	MaxSteps int    `json:"max_steps" yaml:"max_steps"`
}

// MergeClusterLLM overlays non-empty cluster llm fields onto c.
func (c *LLMConfig) MergeClusterLLM(src ClusterLLMBody) {
	if c == nil {
		return
	}
	if src.BaseURL != "" {
		c.BaseURL = src.BaseURL
	}
	if src.APIKey != "" {
		c.APIKey = src.APIKey
	}
	if src.Model != "" {
		c.Model = src.Model
	}
	if src.Provider != "" {
		c.Provider = src.Provider
	}
	if src.MaxSteps > 0 {
		c.MaxSteps = src.MaxSteps
	}
}

// Config is agentd configuration (JSON or YAML file + flags).
type Config struct {
	// Listen is a unix socket path, or tcp://host:port for loopback testing.
	Listen string `json:"listen" yaml:"listen"`
	// TokenEnv is the env var holding the controller auth token (default ANVIL_AGENTD_TOKEN).
	TokenEnv string `json:"token_env" yaml:"token_env"`
	// MaxWorkers is concurrent job capacity (required >= 1).
	MaxWorkers int `json:"max_workers" yaml:"max_workers"`
	// Overload is reject (default) when at capacity.
	Overload OverloadPolicy `json:"overload" yaml:"overload"`
	// WorkerBin is the agent-worker executable; empty = embedded runner.
	WorkerBin string `json:"worker_bin" yaml:"worker_bin"`
	// Repos is the cwd allowlist.
	Repos []RepoEntry `json:"repos" yaml:"repos"`
	// CancelGrace is how long to wait after cancel before force-kill (worker process).
	CancelGrace time.Duration `json:"-"`
	// CancelGraceSec is JSON-friendly seconds for CancelGrace.
	CancelGraceSec int `json:"cancel_grace_sec" yaml:"cancel_grace_sec"`
	// Cluster optional edge.
	Cluster ClusterConfig `json:"cluster" yaml:"cluster"`
	// NodeID labels this daemon in hello_ok (defaults to hostname).
	NodeID string `json:"node_id" yaml:"node_id"`
	// Name is a human label for the node (fleet UI).
	Name string `json:"name" yaml:"name"`
	// Description is optional operator text for the node.
	Description string `json:"description" yaml:"description"`
	// SandboxDir is the root directory for all project workers (0.29+).
	// Worker project paths must resolve under this directory.
	SandboxDir string `json:"sandbox_dir" yaml:"sandbox_dir"`
	// Plugins is the worker-side plugin allowlist (0.20+).
	Plugins PluginsConfig `json:"plugins" yaml:"plugins"`
	// LLM defaults for embedded workers (0.23+). Env / job.assign may override.
	LLM LLMConfig `json:"llm" yaml:"llm"`
}

// PluginsConfig gates which plugin ids jobs may load.
type PluginsConfig struct {
	// Available is the allowlist. Empty → no plugins unless job is rejected when requesting any.
	// When non-empty, job.plugins must be a subset (empty job.plugins → load Available).
	Available []string `json:"available" yaml:"available"`
	Deny      []string `json:"deny" yaml:"deny"`
	// SearchPaths are roots containing <id>/plugin.json for exec plugins.
	SearchPaths []string `json:"search_paths" yaml:"search_paths"`
}

// DefaultConfig returns sensible defaults (listen path filled by caller/platform).
func DefaultConfig() Config {
	return Config{
		TokenEnv:       "ANVIL_AGENTD_TOKEN",
		MaxWorkers:     0,
		Overload:       OverloadReject,
		CancelGraceSec: 5,
		Cluster: ClusterConfig{
			TokenEnv:     "ANVIL_CLUSTER_TOKEN",
			HeartbeatSec: 10,
		},
		LLM: LLMConfig{
			APIKeyEnv: "ANVIL_API_KEY",
		},
	}
}

// LoadFile reads JSON or YAML config by extension and normalizes.
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

// LoadJSONFile is an alias for LoadFile.
func LoadJSONFile(path string) (Config, error) {
	return LoadFile(path)
}

// Normalize validates and fills defaults.
func (c *Config) Normalize() error {
	if c.TokenEnv == "" {
		c.TokenEnv = "ANVIL_AGENTD_TOKEN"
	}
	if c.MaxWorkers < 0 {
		return fmt.Errorf("max_workers must be >= 0, got %d", c.MaxWorkers)
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
	if c.LLM.APIKeyEnv == "" {
		c.LLM.APIKeyEnv = "ANVIL_API_KEY"
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
	c.SandboxDir = strings.TrimSpace(c.SandboxDir)
	if c.SandboxDir != "" {
		abs, err := filepath.Abs(c.SandboxDir)
		if err != nil {
			return fmt.Errorf("sandbox_dir: %w", err)
		}
		c.SandboxDir = abs
		if err := os.MkdirAll(c.SandboxDir, 0o755); err != nil {
			return fmt.Errorf("sandbox_dir mkdir: %w", err)
		}
	}
	return nil
}

// ResolveRepo returns the absolute cwd for repoID.
func (c Config) ResolveRepo(repoID string) (string, error) {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return "", fmt.Errorf("unknown repo_id %q", repoID)
	}
	for _, r := range c.Repos {
		if r.ID == repoID {
			return r.CWD, nil
		}
	}
	return "", fmt.Errorf("unknown repo_id %q", repoID)
}

// ResolvePlugins intersects job plugin ids with daemon policy.
func (c Config) ResolvePlugins(requested []string) ([]string, error) {
	pol := pluginPolicyFrom(c.Plugins)
	return pol.ResolveIDs(requested)
}

// PluginCapabilityLabels returns hello/heartbeat labels for available plugins.
func (c Config) PluginCapabilityLabels() []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range c.Plugins.Available {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		denied := false
		for _, d := range c.Plugins.Deny {
			if strings.TrimSpace(d) == id {
				denied = true
				break
			}
		}
		if denied {
			continue
		}
		label := "plugin." + id
		if !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}
	return out
}

// Token returns the controller auth token from the environment.
func (c Config) Token() string {
	return os.Getenv(c.TokenEnv)
}

// ClusterToken returns the cluster auth token.
func (c Config) ClusterToken() string {
	return os.Getenv(c.Cluster.TokenEnv)
}
