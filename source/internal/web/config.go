// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/configfile"
)

// FleetEntry is a scheduler fleet advertised to mobile clients.
type FleetEntry struct {
	ID           string `json:"id" yaml:"id"`
	Name         string `json:"name" yaml:"name"`
	SchedulerURL string `json:"scheduler_url" yaml:"scheduler_url"`
}

// Config is anvil-web configuration.
type Config struct {
	Listen            string       `json:"listen" yaml:"listen"`
	DBPath            string       `json:"db_path" yaml:"db_path"`
	SchedulerURL      string       `json:"scheduler_url" yaml:"scheduler_url"`
	SchedulerTokenEnv string       `json:"scheduler_token_env" yaml:"scheduler_token_env"`
	SessionTTLHours   int          `json:"session_ttl_hours" yaml:"session_ttl_hours"`
	AdminUserEnv      string       `json:"admin_user_env" yaml:"admin_user_env"`
	AdminPasswordEnv  string       `json:"admin_password_env" yaml:"admin_password_env"`
	Fleets            []FleetEntry `json:"fleets" yaml:"fleets"` // optional; default = one fleet from scheduler_url
	// DeliveryMergeRoot is an optional absolute parent of Worker project_rel checkouts
	// used for Accept→merge main (0.39.15). Empty → stub merge only.
	DeliveryMergeRoot string `json:"delivery_merge_root" yaml:"delivery_merge_root"`
	// Assist configures the drafting assistant (0.46.6).
	//
	// This is deliberately its own block rather than reusing the worker's model
	// settings: anvil-web drafting *planning text* is a different act from a Worker
	// executing a job, and design 23 says the worker owns cognition. See
	// doc/design/v0.x/40-web-assist.md for the boundary this draws.
	Assist AssistConfig `json:"assist" yaml:"assist"`
}

// AssistConfig is the drafting assistant's provider settings.
//
// Empty means off, and off means the surfaces say so rather than producing a fake
// reply: "AI assist is not configured" is honest, a canned answer is not.
type AssistConfig struct {
	// BaseURL is an OpenAI-compatible root (…/v1).
	BaseURL string `json:"base_url" yaml:"base_url"`
	// Model is the model id used for drafting.
	Model string `json:"model" yaml:"model"`
	// APIKeyEnv names the environment variable holding the key. The key never
	// travels through the config file, the wire, or a log.
	APIKeyEnv string `json:"api_key_env" yaml:"api_key_env"`
	// TimeoutSec bounds one drafting request (default 60).
	TimeoutSec int `json:"timeout_sec" yaml:"timeout_sec"`
}

// Enabled reports whether the assistant is configured well enough to try.
func (a AssistConfig) Enabled() bool {
	return strings.TrimSpace(a.BaseURL) != "" && strings.TrimSpace(a.Model) != ""
}

// APIKey reads the key from the environment the config names.
func (a AssistConfig) APIKey() string {
	env := strings.TrimSpace(a.APIKeyEnv)
	if env == "" {
		env = "ANVIL_ASSIST_API_KEY"
	}
	return os.Getenv(env)
}

// DefaultConfig returns defaults.
func DefaultConfig() Config {
	return Config{
		Listen:            "127.0.0.1:17900",
		DBPath:            "anvil-web.db",
		SchedulerURL:      "http://127.0.0.1:17800",
		SchedulerTokenEnv: "ANVIL_SCHEDULER_TOKEN",
		SessionTTLHours:   24 * 7,
		AdminUserEnv:      "ANVIL_WEB_ADMIN_USER",
		AdminPasswordEnv:  "ANVIL_WEB_ADMIN_PASSWORD",
		Assist: AssistConfig{
			APIKeyEnv:  "ANVIL_ASSIST_API_KEY",
			TimeoutSec: 60,
		},
	}
}

// LoadFile loads JSON/YAML config.
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

// Normalize fills defaults.
func (c *Config) Normalize() error {
	if strings.TrimSpace(c.Listen) == "" {
		return fmt.Errorf("listen is required")
	}
	if strings.TrimSpace(c.DBPath) == "" {
		return fmt.Errorf("db_path is required")
	}
	if strings.TrimSpace(c.SchedulerURL) == "" {
		return fmt.Errorf("scheduler_url is required")
	}
	if c.SchedulerTokenEnv == "" {
		c.SchedulerTokenEnv = "ANVIL_SCHEDULER_TOKEN"
	}
	if c.SessionTTLHours <= 0 {
		c.SessionTTLHours = 24 * 7
	}
	if c.AdminUserEnv == "" {
		c.AdminUserEnv = "ANVIL_WEB_ADMIN_USER"
	}
	if c.AdminPasswordEnv == "" {
		c.AdminPasswordEnv = "ANVIL_WEB_ADMIN_PASSWORD"
	}
	c.Assist.BaseURL = strings.TrimRight(strings.TrimSpace(c.Assist.BaseURL), "/")
	if c.Assist.APIKeyEnv == "" {
		c.Assist.APIKeyEnv = "ANVIL_ASSIST_API_KEY"
	}
	if c.Assist.TimeoutSec <= 0 {
		c.Assist.TimeoutSec = 60
	}
	c.Fleets = normalizeFleets(c.Fleets, c.SchedulerURL)
	return nil
}

func normalizeFleets(in []FleetEntry, fallbackURL string) []FleetEntry {
	if len(in) == 0 {
		return []FleetEntry{{
			ID:           "default",
			Name:         "Default fleet",
			SchedulerURL: strings.TrimRight(fallbackURL, "/"),
		}}
	}
	out := make([]FleetEntry, 0, len(in))
	for i, f := range in {
		id := strings.TrimSpace(f.ID)
		name := strings.TrimSpace(f.Name)
		url := strings.TrimRight(strings.TrimSpace(f.SchedulerURL), "/")
		if url == "" {
			url = strings.TrimRight(fallbackURL, "/")
		}
		if id == "" {
			id = fmt.Sprintf("fleet-%d", i+1)
		}
		if name == "" {
			name = id
		}
		out = append(out, FleetEntry{ID: id, Name: name, SchedulerURL: url})
	}
	return out
}

// FleetList returns fleets for the mobile/API catalog.
func (c Config) FleetList() []FleetEntry {
	return append([]FleetEntry(nil), c.Fleets...)
}

// SchedulerToken returns the server-side scheduler bearer.
func (c Config) SchedulerToken() string {
	return os.Getenv(c.SchedulerTokenEnv)
}

// SessionTTL is the session lifetime.
func (c Config) SessionTTL() time.Duration {
	return time.Duration(c.SessionTTLHours) * time.Hour
}

// AdminUser bootstrap username.
func (c Config) AdminUser() string {
	u := os.Getenv(c.AdminUserEnv)
	if u == "" {
		return "admin"
	}
	return u
}

// AdminPassword bootstrap password (required when DB empty).
func (c Config) AdminPassword() string {
	return os.Getenv(c.AdminPasswordEnv)
}
