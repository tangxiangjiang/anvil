// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package gitutil runs read-only git commands with fixed argv (0.17).
package gitutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/redact"
)

const (
	defaultTimeout   = 30 * time.Second
	maxStatusEntries = 200
	maxDiffRunes     = 48_000
	maxLogEntries    = 50
	defaultLogN      = 20
)

// Runner invokes git with fixed arguments (no shell).
type Runner struct {
	CWD     string
	GitBin  string // default "git"
	Timeout time.Duration
}

func (r Runner) bin() string {
	if r.GitBin != "" {
		return r.GitBin
	}
	return "git"
}

func (r Runner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return defaultTimeout
}

// Run executes git args in CWD. Returns stdout (stderr merged into error on failure).
func (r Runner) Run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin(), args...)
	cmd.Dir = r.CWD
	cmd.Env = append(scrubEnv(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("git timed out")
		}
		return out, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

func scrubEnv() []string {
	keep := []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "COMSPEC", "TMP", "TEMP", "HOME", "USERPROFILE", "GIT_EXEC_PATH"}
	var env []string
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// IsWorkTree reports whether cwd is inside a git work tree.
func (r Runner) IsWorkTree(ctx context.Context) bool {
	out, err := r.Run(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// ResolvePath ensures relOrAbs stays under the work tree / cwd.
func (r Runner) ResolvePath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	cwd, err := filepath.Abs(r.CWD)
	if err != nil {
		return "", err
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(cwd, p))
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes cwd: %s", p)
	}
	return filepath.ToSlash(rel), nil
}

// StatusSummary is a capped porcelain-derived status.
type StatusSummary struct {
	Branch    string
	Upstream  string
	Clean     bool
	Staged    []string
	Unstaged  []string
	Untracked []string
	Truncated bool
}

// Status runs git status --porcelain=v1 -b.
func (r Runner) Status(ctx context.Context) (*StatusSummary, error) {
	if !r.IsWorkTree(ctx) {
		return nil, fmt.Errorf("not a git work tree")
	}
	out, err := r.Run(ctx, "status", "--porcelain=v1", "-b")
	if err != nil {
		return nil, err
	}
	sum := &StatusSummary{}
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			sum.Branch, sum.Upstream = parseBranchLine(line[3:])
			continue
		}
		if len(line) < 3 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		// rename: "R  old -> new"
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		switch {
		case xy[0] != ' ' && xy[0] != '?':
			sum.Staged = append(sum.Staged, fmt.Sprintf("%c %s", xy[0], path))
		}
		switch {
		case xy[1] != ' ' && xy[1] != '?':
			sum.Unstaged = append(sum.Unstaged, fmt.Sprintf("%c %s", xy[1], path))
		case xy == "??":
			sum.Untracked = append(sum.Untracked, path)
		}
	}
	sum.Staged, sum.Truncated = capList(sum.Staged, maxStatusEntries, sum.Truncated)
	sum.Unstaged, sum.Truncated = capList(sum.Unstaged, maxStatusEntries, sum.Truncated)
	sum.Untracked, sum.Truncated = capList(sum.Untracked, maxStatusEntries, sum.Truncated)
	sum.Clean = len(sum.Staged) == 0 && len(sum.Unstaged) == 0 && len(sum.Untracked) == 0
	return sum, nil
}

func parseBranchLine(s string) (branch, upstream string) {
	// main...origin/main [ahead 1]
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "..."); i >= 0 {
		branch = s[:i]
		rest := s[i+3:]
		if j := strings.IndexAny(rest, " \t"); j >= 0 {
			upstream = rest[:j]
		} else {
			upstream = rest
		}
		return branch, upstream
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], ""
	}
	return s, ""
}

func capList(in []string, max int, already bool) ([]string, bool) {
	if len(in) <= max {
		return in, already
	}
	return in[:max], true
}

// FormatStatus renders status for the model.
func FormatStatus(s *StatusSummary) string {
	if s == nil {
		return "(no status)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "branch: %s", s.Branch)
	if s.Upstream != "" {
		fmt.Fprintf(&b, " (upstream %s)", s.Upstream)
	}
	b.WriteByte('\n')
	if s.Clean {
		b.WriteString("clean: yes\n")
		return strings.TrimSpace(b.String())
	}
	b.WriteString("clean: no\n")
	writeSection(&b, "staged", s.Staged)
	writeSection(&b, "unstaged", s.Unstaged)
	writeSection(&b, "untracked", s.Untracked)
	if s.Truncated {
		b.WriteString("…[truncated]\n")
	}
	return strings.TrimSpace(b.String())
}

func writeSection(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "%s (%d):\n", title, len(items))
	for _, it := range items {
		fmt.Fprintf(b, "  %s\n", it)
	}
}

// DiffOpts controls git_diff.
type DiffOpts struct {
	Staged bool
	Paths  []string
	Context int // -U; 0 = git default
}

// Diff returns a unified diff (redacted + truncated).
func (r Runner) Diff(ctx context.Context, opts DiffOpts) (string, error) {
	if !r.IsWorkTree(ctx) {
		return "", fmt.Errorf("not a git work tree")
	}
	args := []string{"diff", "--no-ext-diff", "--no-color"}
	if opts.Staged {
		args = append(args, "--cached")
	}
	if opts.Context > 0 {
		args = append(args, "-U"+strconv.Itoa(opts.Context))
	}
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		for _, p := range opts.Paths {
			rel, err := r.ResolvePath(p)
			if err != nil {
				return "", err
			}
			args = append(args, rel)
		}
	}
	out, err := r.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	out = redact.String(out)
	if out == "" {
		return "(empty diff)", nil
	}
	if utf8.RuneCountInString(out) > maxDiffRunes {
		out = string([]rune(out)[:maxDiffRunes]) + "\n…[truncated]"
	}
	return out, nil
}

// LogOpts controls git_log.
type LogOpts struct {
	N      int
	Path   string
	Author string
}

// LogEntry is one commit line.
type LogEntry struct {
	Hash    string
	Subject string
}

// Log returns recent commits.
func (r Runner) Log(ctx context.Context, opts LogOpts) ([]LogEntry, error) {
	if !r.IsWorkTree(ctx) {
		return nil, fmt.Errorf("not a git work tree")
	}
	n := opts.N
	if n <= 0 {
		n = defaultLogN
	}
	if n > maxLogEntries {
		n = maxLogEntries
	}
	args := []string{"log", "-n", strconv.Itoa(n), "--format=%H%x09%s"}
	if opts.Author != "" {
		args = append(args, "--author="+opts.Author)
	}
	if opts.Path != "" {
		rel, err := r.ResolvePath(opts.Path)
		if err != nil {
			return nil, err
		}
		args = append(args, "--", rel)
	}
	out, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		hash, sub, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		entries = append(entries, LogEntry{Hash: hash, Subject: redact.String(sub)})
	}
	return entries, nil
}

// FormatLog renders log entries.
func FormatLog(entries []LogEntry) string {
	if len(entries) == 0 {
		return "(no commits)"
	}
	var b strings.Builder
	for _, e := range entries {
		short := e.Hash
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Fprintf(&b, "%s %s\n", short, e.Subject)
	}
	return strings.TrimSpace(b.String())
}
