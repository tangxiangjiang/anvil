// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package plugin

import (
	"fmt"
	"log/slog"

	"github.com/anvil-dev/anvil/internal/tools"
)

// Host is the sandbox-facing surface plugins use during Init.
type Host interface {
	CWD() string
	RegisterTool(tools.Tool) error
	Logger() *slog.Logger
}

type registryHost struct {
	cwd string
	reg *tools.Registry
	log *slog.Logger
}

// NewHost binds a tool registry to a cwd sandbox.
func NewHost(cwd string, reg *tools.Registry, log *slog.Logger) Host {
	if log == nil {
		log = slog.Default()
	}
	return &registryHost{cwd: cwd, reg: reg, log: log}
}

func (h *registryHost) CWD() string { return h.cwd }

func (h *registryHost) Logger() *slog.Logger { return h.log }

func (h *registryHost) RegisterTool(t tools.Tool) error {
	if h == nil || h.reg == nil {
		return fmt.Errorf("plugin host: nil registry")
	}
	return h.reg.Register(t)
}
