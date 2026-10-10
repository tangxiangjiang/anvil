// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package sample is the in-tree Anvil plugin used by milestone 0.20 tests/docs.
package sample

import (
	"context"
	"encoding/json"

	"github.com/anvil-dev/anvil/internal/plugin"
	"github.com/anvil-dev/anvil/internal/tools"
)

// New returns the sample plugin factory product.
func New() plugin.Plugin {
	return &pingPlugin{}
}

type pingPlugin struct{}

func (p *pingPlugin) ID() string { return "sample" }

func (p *pingPlugin) Init(ctx context.Context, host plugin.Host) error {
	_ = ctx
	return host.RegisterTool(&tools.FuncTool{
		Name:        "plugin_ping",
		Description: "Sample in-tree plugin tool; returns pong and the host cwd.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			_ = ctx
			_ = args
			return "pong cwd=" + host.CWD(), nil
		},
	})
}

func (p *pingPlugin) Close(ctx context.Context) error {
	_ = ctx
	return nil
}

// Manifest describes the sample pack (for docs / capability advertise).
func Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "sample",
		Version:      "0.1.0",
		AnvilAPI:     plugin.APIVersion,
		Capabilities: []string{"tools.plugin_ping"},
		Permissions:  []plugin.Permission{plugin.PermFSRead, plugin.PermNetNone},
	}
}
