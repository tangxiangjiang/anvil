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

// Config is anvil-web configuration.
type Config struct {
	Listen            string `json:"listen" yaml:"listen"`
	DBPath            string `json:"db_path" yaml:"db_path"`
	SchedulerURL      string `json:"scheduler_url" yaml:"scheduler_url"`
	SchedulerTokenEnv string `json:"scheduler_token_env" yaml:"scheduler_token_env"`
	SessionTTLHours   int    `json:"session_ttl_hours" yaml:"session_ttl_hours"`
	AdminUserEnv      string `json:"admin_user_env" yaml:"admin_user_env"`
	AdminPasswordEnv  string `json:"admin_password_env" yaml:"admin_password_env"`
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
	return nil
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
