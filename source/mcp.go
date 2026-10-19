// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"os"
	"sort"

	"github.com/anvil-dev/anvil/internal/settings"
)

// MCPListing is what the selected file layers declare, and which files were
// read to find it.
//
// It describes configuration, not connection. A server appears here because a
// file mentions it; nothing in this type says it started, or which tools it
// brought. The SDK exposes no introspection for that, and a field that could
// only be filled from "the file mentioned it" would be a green light about the
// one thing a person actually cares about.
type MCPListing struct {
	// Sources are the layers that were inspected, lowest precedence first, in
	// the order the session applies them. A source whose file is absent is
	// still reported: "you turned this on and there is no file" is different
	// from "you turned it off", and the client can only say the first if it
	// hears about the layer.
	Sources []MCPSourceInfo
	// Servers are the declared servers, sorted by name, each naming the file
	// that declared it. A name declared by both layers appears once, with the
	// project entry — that is the one a session would use.
	Servers []MCPServerInfo
}

// MCPSourceInfo is one config file a listing looked at.
type MCPSourceInfo struct {
	Source SettingSource
	Path   string
	// Exists is false when the file is not there. A missing file is not an
	// error: it is an empty layer, which is how the loader treats it too.
	Exists bool
}

// MCPServerInfo is one declared MCP server.
type MCPServerInfo struct {
	Name string
	// Transport is stdio, http or sse, decided the way the SDK decides it. It
	// is empty for an entry that names neither a command nor a URL.
	Transport string
	// Source is the config file that declared this server.
	Source string
}

// ListMCPServers reports the MCP servers the given setting sources declare.
//
// Only SettingSourceUser and SettingSourceProject name a file; other values
// are ignored. Precedence is the loader's: user first, then project, so a name
// declared in both files is reported with the project's entry. A file that
// will not parse is an error — an empty listing would say "nothing is
// configured" about a file that says something.
func ListMCPServers(cwd string, sources []SettingSource) (MCPListing, error) {
	dir, err := resolveListCWD(cwd)
	if err != nil {
		return MCPListing{}, err
	}
	enabled := func(src SettingSource) bool {
		for _, s := range sources {
			if s == src {
				return true
			}
		}
		return false
	}

	type layer struct {
		source SettingSource
		path   string
	}
	layers := make([]layer, 0, 2)
	if enabled(SettingSourceUser) {
		path, err := settings.UserMCPPath()
		if err != nil {
			return MCPListing{}, newError("resolve user mcp path", false, err)
		}
		layers = append(layers, layer{source: SettingSourceUser, path: path})
	}
	if enabled(SettingSourceProject) {
		layers = append(layers, layer{source: SettingSourceProject, path: settings.ProjectMCPPath(dir)})
	}

	out := MCPListing{Sources: make([]MCPSourceInfo, 0, len(layers)), Servers: []MCPServerInfo{}}
	byName := make(map[string]MCPServerInfo, len(layers))
	for _, l := range layers {
		exists := true
		if _, err := os.Stat(l.path); err != nil {
			if !os.IsNotExist(err) {
				return MCPListing{}, newError("inspect "+string(l.source)+" mcp.json", false, err)
			}
			exists = false
		}
		out.Sources = append(out.Sources, MCPSourceInfo{Source: l.source, Path: l.path, Exists: exists})
		// The same parser the loader uses, so the listing cannot describe a
		// file differently from the way a session reads it.
		servers, err := settings.LoadMCPJSON(l.path)
		if err != nil {
			return MCPListing{}, newError("load "+string(l.source)+" mcp.json", false, err)
		}
		for name, s := range servers {
			cfg := mcpFromSettings(s)
			byName[name] = MCPServerInfo{Name: name, Transport: mcpTransport(cfg), Source: l.path}
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Servers = append(out.Servers, byName[name])
	}
	return out, nil
}

// mcpTransport names the transport the SDK would pick for cfg, mirroring
// internal/mcp.Start: an explicit transport wins, then a command means stdio,
// then a URL means http. An entry with neither is reported as no transport at
// all, because that is what it is.
func mcpTransport(cfg MCPServerConfig) string {
	if cfg.Transport != "" {
		return cfg.Transport
	}
	if cfg.Command != "" {
		return "stdio"
	}
	if cfg.URL != "" {
		return "http"
	}
	return ""
}
