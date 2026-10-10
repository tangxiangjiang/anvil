// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package plugin

import (
	"fmt"
	"log/slog"

	"github.com/anvil-dev/anvil/internal/tools"
)

// EmbedConfig is optional OpenAI-compatible embeddings exposed to plugins (0.21+).
type EmbedConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Host is the sandbox-facing surface plugins use during Init.
type Host interface {
	CWD() string
	RegisterTool(tools.Tool) error
	Logger() *slog.Logger
	EmbedConfig() EmbedConfig
}

type registryHost struct {
	cwd   string
	reg   *tools.Registry
	log   *slog.Logger
	embed EmbedConfig
}

// NewHost binds a tool registry to a cwd sandbox.
func NewHost(cwd string, reg *tools.Registry, log *slog.Logger) Host {
	return NewHostWithEmbed(cwd, reg, log, EmbedConfig{})
}

// NewHostWithEmbed is NewHost plus optional embed credentials for plugins.
func NewHostWithEmbed(cwd string, reg *tools.Registry, log *slog.Logger, embed EmbedConfig) Host {
	if log == nil {
		log = slog.Default()
	}
	return &registryHost{cwd: cwd, reg: reg, log: log, embed: embed}
}

func (h *registryHost) CWD() string { return h.cwd }

func (h *registryHost) Logger() *slog.Logger { return h.log }

func (h *registryHost) EmbedConfig() EmbedConfig { return h.embed }

func (h *registryHost) RegisterTool(t tools.Tool) error {
	if h == nil || h.reg == nil {
		return fmt.Errorf("plugin host: nil registry")
	}
	return h.reg.Register(t)
}
