// Package settings loads Cursor-compatible project/user config files.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MCPServer is a parsed MCP server entry from mcp.json.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
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
