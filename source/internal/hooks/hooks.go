// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package hooks loads file-based policy hooks (0.10+; deepened in 0.14).
package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/redact"
)

const (
	defaultCmdTimeoutMs   = 3000
	maxCmdOutputRunes     = 500
	maxSessionStartRunes  = 4000
	maxAfterAppendRunes   = 400
)

// File is the on-disk schema (best-effort subset; not full Cursor hooks catalog).
type File struct {
	Version      int           `json:"version"`
	BeforeShell  []Rule        `json:"beforeShell"`
	BeforeWrite  []Rule        `json:"beforeWrite"`
	BeforeEdit   []Rule        `json:"beforeEdit"`
	BeforeMCP    []Rule        `json:"beforeMCP"`
	BeforeRead   []Rule        `json:"beforeRead"`
	AfterTool    []AfterRule   `json:"afterTool"`
	SessionStart []SessionRule `json:"sessionStart"`
	SessionEnd   []SessionRule `json:"sessionEnd"`
}

// Rule gates a tool invoke (matcher fields + optional command hook).
type Rule struct {
	DenyPattern  string `json:"denyPattern"`
	DenyContains string `json:"denyContains"`
	DenyTool     string `json:"denyTool"`
	Message      string `json:"message"`
	Command      string `json:"command"`
	TimeoutMs    int    `json:"timeoutMs"`
}

// AfterRule optionally annotates a tool result (bounded).
type AfterRule struct {
	DenyTool     string `json:"denyTool"`
	DenyContains string `json:"denyContains"`
	DenyPattern  string `json:"denyPattern"`
	Message      string `json:"message"` // appended when matched
}

// SessionRule injects notes (sessionStart) or optional end commands (sessionEnd).
type SessionRule struct {
	Message   string `json:"message"`
	Command   string `json:"command"`
	TimeoutMs int    `json:"timeoutMs"`
}

// CheckOpts controls command-hook execution.
type CheckOpts struct {
	AllowCommands bool
	CWD           string
	Ctx           context.Context
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
func (f *File) CheckShell(command string, opts CheckOpts) (deny bool, message string) {
	if f == nil {
		return false, ""
	}
	return f.evalRules(f.BeforeShell, "shell", command, opts, "shell denied by hooks.json")
}

// CheckMutate returns a deny message for write/edit/patch tools.
func (f *File) CheckMutate(toolName string, argsJSON string, opts CheckOpts) (deny bool, message string) {
	if f == nil {
		return false, ""
	}
	var rules []Rule
	switch toolName {
	case "write_file":
		rules = f.BeforeWrite
	case "edit_file", "apply_patch":
		rules = append(append([]Rule{}, f.BeforeEdit...), f.BeforeWrite...)
	default:
		return false, ""
	}
	return f.evalRules(rules, toolName, argsJSON, opts, toolName+" denied by hooks.json")
}

// CheckMCP gates MCP tool invokes (registered name and/or short MCP tool name).
func (f *File) CheckMCP(registeredName, mcpToolName, argsJSON string, opts CheckOpts) (deny bool, message string) {
	if f == nil {
		return false, ""
	}
	input := registeredName + " " + mcpToolName + " " + argsJSON
	return f.evalRules(f.BeforeMCP, registeredName, input, opts, "mcp denied by hooks.json")
}

// CheckRead gates read_file when beforeRead rules are present.
func (f *File) CheckRead(argsJSON string, opts CheckOpts) (deny bool, message string) {
	if f == nil || len(f.BeforeRead) == 0 {
		return false, ""
	}
	return f.evalRules(f.BeforeRead, "read_file", argsJSON, opts, "read denied by hooks.json")
}

// ApplyAfter may append a short note to tool output (never grows beyond cap).
func (f *File) ApplyAfter(toolName, argsJSON, content string) string {
	if f == nil || len(f.AfterTool) == 0 {
		return content
	}
	var notes []string
	for _, rule := range f.AfterTool {
		if rule.DenyTool != "" && rule.DenyTool != toolName && !strings.HasPrefix(toolName, rule.DenyTool) {
			continue
		}
		hay := toolName + " " + argsJSON + " " + content
		matched := false
		if rule.DenyContains != "" && strings.Contains(hay, rule.DenyContains) {
			matched = true
		}
		if rule.DenyPattern != "" {
			if re, err := regexp.Compile(rule.DenyPattern); err == nil && re.MatchString(hay) {
				matched = true
			}
		}
		if rule.DenyTool != "" && rule.DenyContains == "" && rule.DenyPattern == "" {
			matched = true
		}
		if matched && strings.TrimSpace(rule.Message) != "" {
			notes = append(notes, strings.TrimSpace(rule.Message))
		}
	}
	if len(notes) == 0 {
		return content
	}
	extra := strings.Join(notes, "; ")
	if utf8.RuneCountInString(extra) > maxAfterAppendRunes {
		extra = string([]rune(extra)[:maxAfterAppendRunes]) + "…"
	}
	if content == "" {
		return "[hook] " + extra
	}
	return content + "\n[hook] " + extra
}

// SessionStartNotes returns capped notes from sessionStart.message entries.
func (f *File) SessionStartNotes() string {
	if f == nil {
		return ""
	}
	var parts []string
	n := 0
	for _, r := range f.SessionStart {
		msg := strings.TrimSpace(r.Message)
		if msg == "" {
			continue
		}
		if n+utf8.RuneCountInString(msg) > maxSessionStartRunes {
			remain := maxSessionStartRunes - n
			if remain <= 0 {
				break
			}
			runes := []rune(msg)
			if len(runes) > remain {
				msg = string(runes[:remain]) + "…"
			}
			parts = append(parts, msg)
			break
		}
		parts = append(parts, msg)
		n += utf8.RuneCountInString(msg) + 1
	}
	return strings.Join(parts, "\n")
}

// FireSessionEnd runs optional sessionEnd commands (best-effort; never errors to caller).
func (f *File) FireSessionEnd(opts CheckOpts) {
	if f == nil || !opts.AllowCommands {
		return
	}
	for _, r := range f.SessionEnd {
		if strings.TrimSpace(r.Command) == "" {
			continue
		}
		_, _ = runCommand(opts, r.Command, r.TimeoutMs, "")
	}
}

func (f *File) evalRules(rules []Rule, toolName, input string, opts CheckOpts, fallback string) (bool, string) {
	for _, rule := range rules {
		if rule.DenyTool != "" && rule.DenyTool != toolName && !strings.Contains(input, rule.DenyTool) {
			// Allow DenyTool to match MCP short name inside input.
			if !strings.HasSuffix(toolName, "_"+rule.DenyTool) && toolName != rule.DenyTool {
				continue
			}
		}
		if rule.DenyContains != "" && strings.Contains(input, rule.DenyContains) {
			return true, msgOr(rule.Message, fallback)
		}
		if rule.DenyPattern != "" {
			re, err := regexp.Compile(rule.DenyPattern)
			if err == nil && re.MatchString(input) {
				return true, msgOr(rule.Message, fallback)
			}
		}
		if rule.DenyTool != "" && rule.DenyContains == "" && rule.DenyPattern == "" && rule.Command == "" {
			if rule.DenyTool == toolName || strings.HasSuffix(toolName, "_"+rule.DenyTool) || strings.Contains(input, rule.DenyTool) {
				return true, msgOr(rule.Message, fallback)
			}
		}
		if rule.Command != "" {
			if !opts.AllowCommands {
				continue // ignored when disabled
			}
			ok, detail := runCommand(opts, rule.Command, rule.TimeoutMs, input)
			if !ok {
				if detail != "" {
					return true, msgOr(rule.Message, fallback) + ": " + detail
				}
				return true, msgOr(rule.Message, fallback)
			}
		}
	}
	return false, ""
}

func runCommand(opts CheckOpts, command string, timeoutMs int, input string) (ok bool, detail string) {
	cwd := opts.CWD
	if cwd == "" {
		cwd = "."
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return false, "hook cwd"
	}
	cmdPath := command
	if !filepath.IsAbs(cmdPath) {
		cmdPath = filepath.Join(absCwd, command)
	}
	cmdPath = filepath.Clean(cmdPath)
	rel, err := filepath.Rel(absCwd, cmdPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, "hook command escapes cwd"
	}
	if _, err := os.Stat(cmdPath); err != nil {
		return false, "hook command missing"
	}

	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if timeoutMs <= 0 {
		timeoutMs = defaultCmdTimeoutMs
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		ext := strings.ToLower(filepath.Ext(cmdPath))
		if ext == ".bat" || ext == ".cmd" {
			c = exec.CommandContext(ctx, "cmd.exe", "/C", cmdPath)
		} else {
			c = exec.CommandContext(ctx, cmdPath)
		}
	} else {
		c = exec.CommandContext(ctx, cmdPath)
	}
	c.Dir = absCwd
	c.Env = scrubEnv(input)
	out, err := c.CombinedOutput()
	snippet := redact.String(string(out))
	if utf8.RuneCountInString(snippet) > maxCmdOutputRunes {
		snippet = string([]rune(snippet)[:maxCmdOutputRunes]) + "…"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return false, "hook timed out"
	}
	if err != nil {
		if snippet == "" {
			return false, "hook exit non-zero"
		}
		return false, snippet
	}
	return true, ""
}

func scrubEnv(input string) []string {
	keep := []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "COMSPEC", "TMP", "TEMP", "HOME", "USERPROFILE"}
	var env []string
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	if input != "" {
		if utf8.RuneCountInString(input) > 4000 {
			input = string([]rune(input)[:4000])
		}
		env = append(env, "ANVIL_HOOK_INPUT="+input)
	}
	return env
}

func msgOr(msg, fallback string) string {
	if strings.TrimSpace(msg) == "" {
		return fallback
	}
	return msg
}
