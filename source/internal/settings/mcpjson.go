// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package settings loads Cursor-compatible project/user config files.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MCPServer is a parsed MCP server entry from mcp.json.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// Enabled turns a declared server off without deleting it. Absent means
	// enabled: a file written before the field existed keeps working.
	Enabled *bool `json:"enabled"`
	// Transport names the wire. Type is the older spelling some files use;
	// TransportName settles which one applies.
	Transport string `json:"transport"`
	Type      string `json:"type"`
	// OAuth supplies a client registration by hand for authorization servers
	// that do not support dynamic registration (0.44+). The secret never
	// leaves this file except to the server it belongs to.
	OAuth *MCPServerOAuth `json:"oauth"`
}

// MCPServerOAuth is the manual half of the sign-in configuration.
type MCPServerOAuth struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Scope        string `json:"scope"`
}

// TransportName is the transport the entry declares, normalized the way the
// client names it: streamable-http is the same server as http. An empty result
// means the entry did not say, and the command/url shape decides.
func (s MCPServer) TransportName() string {
	name := s.Transport
	if name == "" {
		name = s.Type
	}
	return foldTransport(name)
}

// foldTransport normalizes a transport spelling. streamable-http names the
// same wire as http; the client has one implementation for it.
func foldTransport(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "streamable-http" {
		return "http"
	}
	return name
}

type mcpFile struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// LoadMCPJSON reads a Cursor-style mcp.json file. Missing file → empty map, nil error.
func LoadMCPJSON(path string) (map[string]MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]MCPServer{}, nil
		}
		return nil, err
	}
	var f mcpFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.MCPServers == nil {
		return map[string]MCPServer{}, nil
	}
	// Two spellings of the same field that disagree make the entry's transport
	// guesswork, and guessing wrong would launch a process where a URL belongs.
	names := make([]string, 0, len(f.MCPServers))
	for name := range f.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := f.MCPServers[name]
		if s.Transport != "" && s.Type != "" && foldTransport(s.Transport) != foldTransport(s.Type) {
			return nil, fmt.Errorf("parse %s: server %q declares transport %q and type %q", path, name, s.Transport, s.Type)
		}
	}
	return f.MCPServers, nil
}

// ProjectMCPPath is {cwd}/.cursor/mcp.json.
func ProjectMCPPath(cwd string) string {
	return filepath.Join(cwd, ".cursor", "mcp.json")
}

// UserMCPPath is ~/.cursor/mcp.json.
func UserMCPPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cursor", "mcp.json"), nil
}

// EnvSlice converts a map env to KEY=VAL entries.
func EnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}
