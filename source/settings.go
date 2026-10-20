// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"github.com/anvil-dev/anvil/internal/settings"
)

// mergeMCPFromSources loads file MCP layers and merges with inline MCPServers.
// Precedence (high → low): inline Create options → project → user.
func mergeMCPFromSources(cwd string, opts Options) (Options, error) {
	merged := map[string]MCPServerConfig{}
	fileNames := map[string]bool{}
	if opts.Local.HasSettingSource(SettingSourceUser) {
		path, err := settings.UserMCPPath()
		if err != nil {
			return opts, newError("resolve user mcp path", false, err)
		}
		servers, err := settings.LoadMCPJSON(path)
		if err != nil {
			return opts, newError("load user mcp.json", false, err)
		}
		for name, s := range servers {
			merged[name] = mcpFromSettings(s)
			fileNames[name] = true
		}
	}
	if opts.Local.HasSettingSource(SettingSourceProject) {
		servers, err := settings.LoadMCPJSON(settings.ProjectMCPPath(cwd))
		if err != nil {
			return opts, newError("load project mcp.json", false, err)
		}
		for name, s := range servers {
			merged[name] = mcpFromSettings(s)
			fileNames[name] = true
		}
	}
	// Inline options are the caller's direct intent, so they replace the file's
	// entry and are never filtered by `enabled`.
	inline := make(map[string]bool, len(opts.MCPServers))
	for name, cfg := range opts.MCPServers {
		merged[name] = cfg
		inline[name] = true
	}
	// A disabled file entry has already shadowed any lower layer while merging
	// (the user file loses to the project file even when the project entry is
	// off). Dropping it now means "shadowed and not started", not "fall back".
	for name := range fileNames {
		if inline[name] {
			continue
		}
		if cfg, ok := merged[name]; ok && cfg.Enabled != nil && !*cfg.Enabled {
			delete(merged, name)
		}
	}
	opts.MCPServers = merged
	return opts, nil
}

func mcpFromSettings(s settings.MCPServer) MCPServerConfig {
	cfg := MCPServerConfig{
		Transport: s.TransportName(),
		Command:   s.Command,
		Args:      s.Args,
		Env:       settings.EnvSlice(s.Env),
		URL:       s.URL,
		Headers:   s.Headers,
		Enabled:   s.Enabled,
	}
	if cfg.Transport == "" && cfg.URL != "" && cfg.Command == "" {
		cfg.Transport = "http"
	}
	return cfg
}
