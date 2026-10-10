// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package verify runs configured test/build argv commands (0.18).
package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultTestTimeout  = 10 * time.Minute
	defaultBuildTimeout = 5 * time.Minute
	maxCaptureBytes     = 256 << 10
	logTailRunes        = 6000
)

// CommandSpec is an argv command from Options or project.json.
type CommandSpec struct {
	Command []string      `json:"command"`
	Timeout time.Duration `json:"-"`
	Dir     string        `json:"dir"` // relative package dir under cwd
}

// ProjectFile is `.anvil/project.json`.
type ProjectFile struct {
	Test  *projectCmd `json:"test"`
	Build *projectCmd `json:"build"`
}

type projectCmd struct {
	Command   []string `json:"command"`
	TimeoutMs int      `json:"timeout_ms"`
	Dir       string   `json:"dir"`
}

// LoadProject reads `.anvil/project.json` if present.
func LoadProject(cwd string) (ProjectFile, error) {
	path := filepath.Join(cwd, ".anvil", "project.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectFile{}, nil
		}
		return ProjectFile{}, err
	}
	var pf ProjectFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return ProjectFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return pf, nil
}

func (p *projectCmd) toSpec() CommandSpec {
	if p == nil {
		return CommandSpec{}
	}
	spec := CommandSpec{Command: append([]string{}, p.Command...), Dir: p.Dir}
	if p.TimeoutMs > 0 {
		spec.Timeout = time.Duration(p.TimeoutMs) * time.Millisecond
	}
	return spec
}

// TestSpec returns the project test command spec.
func (p ProjectFile) TestSpec() CommandSpec { return p.Test.toSpec() }

// BuildSpec returns the project build command spec.
func (p ProjectFile) BuildSpec() CommandSpec { return p.Build.toSpec() }

// MergeSpec prefers explicit Options over project file.
func MergeSpec(explicit, fromFile CommandSpec) CommandSpec {
	if len(explicit.Command) > 0 {
		out := explicit
		if out.Timeout <= 0 {
			out.Timeout = fromFile.Timeout
		}
		if out.Dir == "" {
			out.Dir = fromFile.Dir
		}
		return out
	}
	return fromFile
}

// ValidateToken rejects shell metacharacters in model-supplied args.
func ValidateToken(s string) error {
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, ";|&`$<>\n\r") || strings.Contains(s, "$(") || strings.Contains(s, "`") {
		return fmt.Errorf("rejected unsafe argument %q", s)
	}
	if strings.Contains(s, "&&") || strings.Contains(s, "||") {
		return fmt.Errorf("rejected unsafe argument %q", s)
	}
	return nil
}

// ValidateTokens validates each token.
func ValidateTokens(tokens []string) error {
	for _, t := range tokens {
		if err := ValidateToken(t); err != nil {
			return err
		}
	}
	return nil
}

// Result is a structured command outcome.
type Result struct {
	OK       bool
	ExitCode int
	Summary  string
	Failed   []string
	Errors   []string
	LogTail  string
}

// Run executes argv under cwd (optional subdir), capturing truncated output.
func Run(ctx context.Context, cwd string, spec CommandSpec, extra []string, timeout time.Duration) (*Result, error) {
	if len(spec.Command) == 0 {
		return nil, fmt.Errorf("command is not configured")
	}
	if err := ValidateTokens(extra); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	dir := abs
	if spec.Dir != "" {
		if err := ValidateToken(spec.Dir); err != nil {
			return nil, err
		}
		sub := filepath.Clean(filepath.Join(abs, spec.Dir))
		rel, err := filepath.Rel(abs, sub)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("command dir escapes cwd")
		}
		dir = sub
	}
	if timeout <= 0 {
		timeout = spec.Timeout
	}
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}
	argv := append([]string{}, spec.Command...)
	argv = append(argv, extra...)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = scrubEnv()
	var buf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &buf, n: maxCaptureBytes}
	cmd.Stderr = &limitedWriter{w: &buf, n: maxCaptureBytes}
	err = cmd.Run()
	raw := buf.String()
	exit := 0
	ok := true
	if err != nil {
		ok = false
		if ctx.Err() == context.DeadlineExceeded {
			return &Result{
				OK: false, ExitCode: -1,
				Summary: "timeout",
				LogTail: tail(raw, logTailRunes),
			}, nil
		}
		if ee, okExit := err.(*exec.ExitError); okExit {
			exit = ee.ExitCode()
		} else {
			return nil, fmt.Errorf("run %v: %w", argv, err)
		}
	}
	failed := ParseFailedTests(raw)
	errors := ParseBuildErrors(raw)
	summary := "ok"
	if !ok {
		summary = "failed"
		if len(failed) > 0 {
			summary = fmt.Sprintf("failed (%d)", len(failed))
		} else if len(errors) > 0 {
			summary = fmt.Sprintf("errors (%d)", len(errors))
		}
	}
	return &Result{
		OK:       ok,
		ExitCode: exit,
		Summary:  summary,
		Failed:   failed,
		Errors:   errors,
		LogTail:  tail(raw, logTailRunes),
	}, nil
}

type limitedWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	remain := l.n - l.w.Len()
	if remain <= 0 {
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = l.w.Write(p[:remain])
		return len(p), nil
	}
	return l.w.Write(p)
}

func scrubEnv() []string {
	keep := []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "COMSPEC", "TMP", "TEMP", "HOME", "USERPROFILE", "GOROOT", "GOPATH", "GOMODCACHE", "GOTOOLCHAIN"}
	var env []string
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func tail(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no output)"
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return "…[truncated head]\n" + string(r[len(r)-maxRunes:])
}

var (
	goFailRe     = regexp.MustCompile(`(?m)^--- FAIL: ([A-Za-z0-9_/\.]+)`)
	genericFail  = regexp.MustCompile(`(?mi)^(?:FAIL|FAILED|Error)\b[:\s]+(.+)$`)
	goBuildErrRe = regexp.MustCompile(`(?m)(\S+\.go:\d+:\d+: .+)`)
)

// ParseFailedTests extracts failed test names (go test + generic).
func ParseFailedTests(log string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
		if len(out) >= 30 {
			return
		}
	}
	for _, m := range goFailRe.FindAllStringSubmatch(log, 40) {
		add(m[1])
	}
	if len(out) == 0 {
		for _, m := range genericFail.FindAllStringSubmatch(log, 40) {
			add(m[1])
		}
	}
	return out
}

// ParseBuildErrors extracts compiler-like error lines.
func ParseBuildErrors(log string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range goBuildErrRe.FindAllStringSubmatch(log, 40) {
		line := strings.TrimSpace(m[1])
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if len(out) >= 30 {
			break
		}
	}
	return out
}

// FormatResult renders a tool response.
func FormatResult(kind string, res *Result) string {
	if res == nil {
		return "(nil result)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s exit=%d\n", kind, res.Summary, res.ExitCode)
	if len(res.Failed) > 0 {
		b.WriteString("failed:\n")
		for _, f := range res.Failed {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	if len(res.Errors) > 0 {
		b.WriteString("errors:\n")
		for _, e := range res.Errors {
			fmt.Fprintf(&b, "  - %s\n", e)
		}
	}
	b.WriteString("logTail:\n")
	b.WriteString(res.LogTail)
	return strings.TrimSpace(b.String())
}

// DefaultTimeouts returns package defaults.
func DefaultTestTimeout() time.Duration  { return defaultTestTimeout }
func DefaultBuildTimeout() time.Duration { return defaultBuildTimeout }

// LooksLikeGoTest reports whether argv is a go test invocation.
func LooksLikeGoTest(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	base := filepath.Base(argv[0])
	return (base == "go" || base == "go.exe") && argv[1] == "test"
}
