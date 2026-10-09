// Package hooks loads minimal file-based policy hooks (0.10).
package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// File is the on-disk schema (best-effort subset; not full Cursor hooks catalog).
type File struct {
	Version     int          `json:"version"`
	BeforeShell []ShellRule  `json:"beforeShell"`
	BeforeWrite []MutateRule `json:"beforeWrite"`
	BeforeEdit  []MutateRule `json:"beforeEdit"`
}

// ShellRule gates the shell tool.
type ShellRule struct {
	DenyPattern  string `json:"denyPattern"`  // regexp
	DenyContains string `json:"denyContains"` // substring
	Message      string `json:"message"`
}

// MutateRule gates write_file / edit_file / apply_patch by tool name and/or args text.
type MutateRule struct {
	DenyTool     string `json:"denyTool"`
	DenyContains string `json:"denyContains"`
	DenyPattern  string `json:"denyPattern"`
	Message      string `json:"message"`
}

// Load reads .anvil/hooks.json then .cursor/hooks.json (first found wins).
// Missing files → nil, nil.
func Load(cwd string) (*File, string, error) {
	candidates := []string{
		filepath.Join(cwd, ".anvil", "hooks.json"),
		filepath.Join(cwd, ".cursor", "hooks.json"),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", err
		}
		var f File
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, "", fmt.Errorf("parse %s: %w", path, err)
		}
		return &f, path, nil
	}
	return nil, "", nil
}

// CheckShell returns a deny message if the command is blocked.
func (f *File) CheckShell(command string) (deny bool, message string) {
	if f == nil {
		return false, ""
	}
	for _, rule := range f.BeforeShell {
		if rule.DenyContains != "" && strings.Contains(command, rule.DenyContains) {
			return true, msgOr(rule.Message, "shell denied by hooks.json")
		}
		if rule.DenyPattern != "" {
			re, err := regexp.Compile(rule.DenyPattern)
			if err == nil && re.MatchString(command) {
				return true, msgOr(rule.Message, "shell denied by hooks.json")
			}
		}
	}
	return false, ""
}

// CheckMutate returns a deny message for write/edit/patch tools.
func (f *File) CheckMutate(toolName string, argsJSON string) (deny bool, message string) {
	if f == nil {
		return false, ""
	}
	var rules []MutateRule
	switch toolName {
	case "write_file":
		rules = f.BeforeWrite
	case "edit_file", "apply_patch":
		rules = append(f.BeforeEdit, f.BeforeWrite...)
	default:
		return false, ""
	}
	for _, rule := range rules {
		if rule.DenyTool != "" && rule.DenyTool != toolName {
			continue
		}
		if rule.DenyContains != "" && strings.Contains(argsJSON, rule.DenyContains) {
			return true, msgOr(rule.Message, toolName+" denied by hooks.json")
		}
		if rule.DenyPattern != "" {
			re, err := regexp.Compile(rule.DenyPattern)
			if err == nil && re.MatchString(argsJSON) {
				return true, msgOr(rule.Message, toolName+" denied by hooks.json")
			}
		}
		if rule.DenyTool == toolName && rule.DenyContains == "" && rule.DenyPattern == "" {
			return true, msgOr(rule.Message, toolName+" denied by hooks.json")
		}
	}
	return false, ""
}

func msgOr(msg, fallback string) string {
	if strings.TrimSpace(msg) == "" {
		return fallback
	}
	return msg
}
