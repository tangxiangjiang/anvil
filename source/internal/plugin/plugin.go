// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package plugin implements Anvil worker/SDK plugin Host + loaders (0.20).
package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/anvil-dev/anvil/internal/tools"
)

// Plugin is an in-tree (or linked) capability pack.
type Plugin interface {
	ID() string
	Init(ctx context.Context, host Host) error
	Close(ctx context.Context) error
}

// Factory constructs an in-tree plugin.
type Factory func() Plugin

// LoadOptions configures plugin resolution.
type LoadOptions struct {
	// Factories maps id → in-tree constructor (SDK / worker builtins).
	Factories map[string]Factory
	// SearchPaths are directories that may contain <id>/plugin.json for exec plugins.
	SearchPaths []string
	// Policy gates which ids/permissions may load (nil = allow requested).
	Policy *Policy
	Logger *slog.Logger
	// Embed is optional embeddings config for plugins that opt into vectors (0.21+).
	Embed EmbedConfig
}

// Manager holds initialized plugins for Close.
type Manager struct {
	items []loaded
}

type loaded struct {
	id     string
	plugin Plugin // in-tree; may be nil for exec-only
	exec   *ExecSession
}

// Close shuts down all loaded plugins.
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	var first error
	for i := len(m.items) - 1; i >= 0; i-- {
		it := m.items[i]
		if it.exec != nil {
			if err := it.exec.Close(); err != nil && first == nil {
				first = err
			}
		}
		if it.plugin != nil {
			if err := it.plugin.Close(ctx); err != nil && first == nil {
				first = err
			}
		}
	}
	m.items = nil
	return first
}

// IDs returns loaded plugin ids.
func (m *Manager) IDs() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.items))
	for _, it := range m.items {
		out = append(out, it.id)
	}
	return out
}

// Capabilities aggregates manifest capability labels from loaded plugins.
func (m *Manager) Capabilities() []string {
	if m == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range m.items {
		var caps []string
		if it.exec != nil {
			caps = it.exec.Manifest().Capabilities
		}
		for _, c := range caps {
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
		if !seen["plugin."+it.id] {
			seen["plugin."+it.id] = true
			out = append(out, "plugin."+it.id)
		}
	}
	return out
}

// Load initializes the requested plugin ids into reg.
func Load(ctx context.Context, cwd string, ids []string, reg *tools.Registry, opts LoadOptions) (*Manager, error) {
	if len(ids) == 0 {
		return &Manager{}, nil
	}
	host := NewHostWithEmbed(cwd, reg, opts.Logger, opts.Embed)
	mgr := &Manager{}
	seen := map[string]bool{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if opts.Policy != nil {
			if err := opts.Policy.AllowID(id); err != nil {
				_ = mgr.Close(ctx)
				return nil, err
			}
		}
		if f := opts.Factories[id]; f != nil {
			p := f()
			if p == nil {
				_ = mgr.Close(ctx)
				return nil, fmt.Errorf("plugin %q: factory returned nil", id)
			}
			if err := p.Init(ctx, host); err != nil {
				_ = mgr.Close(ctx)
				return nil, fmt.Errorf("plugin %q init: %w", id, err)
			}
			mgr.items = append(mgr.items, loaded{id: id, plugin: p})
			continue
		}
		man, dir, err := findManifest(id, opts.SearchPaths)
		if err != nil {
			_ = mgr.Close(ctx)
			return nil, err
		}
		if opts.Policy != nil {
			if err := opts.Policy.AllowManifest(man); err != nil {
				_ = mgr.Close(ctx)
				return nil, err
			}
		}
		if man.Command == "" {
			_ = mgr.Close(ctx)
			return nil, fmt.Errorf("plugin %q: not an in-tree builtin and manifest has no command", id)
		}
		cmdPath := man.Command
		if !filepath.IsAbs(cmdPath) && dir != "" {
			cand := filepath.Join(dir, cmdPath)
			if _, err := os.Stat(cand); err == nil {
				cmdPath = cand
			}
		}
		sess, err := StartExec(ctx, ExecConfig{
			ID:      id,
			Command: cmdPath,
			Args:    man.Args,
			CWD:     cwd,
			Manifest: man,
		}, host)
		if err != nil {
			_ = mgr.Close(ctx)
			return nil, err
		}
		mgr.items = append(mgr.items, loaded{id: id, exec: sess})
	}
	return mgr, nil
}

func findManifest(id string, search []string) (Manifest, string, error) {
	for _, root := range search {
		if root == "" {
			continue
		}
		dir := filepath.Join(root, id)
		path := filepath.Join(dir, "plugin.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		m, err := ParseManifest(data)
		if err != nil {
			return Manifest{}, "", err
		}
		if m.ID != id {
			return Manifest{}, "", fmt.Errorf("plugin manifest at %s: id %q != requested %q", path, m.ID, id)
		}
		return m, dir, nil
	}
	return Manifest{}, "", fmt.Errorf("unknown plugin %q", id)
}
