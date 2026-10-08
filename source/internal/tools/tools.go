package tools

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

	"github.com/anvil-dev/anvil/internal/llm"
)

const defaultMaxOutput = 64 * 1024

// Result is a tool execution outcome.
type Result struct {
	Content string
	IsError bool
}

// Tool is an executable tool.
type Tool interface {
	Def() llm.ToolDef
	Execute(ctx context.Context, args json.RawMessage) Result
}

// Registry maps tool name -> Tool.
type Registry struct {
	cwd        string
	allowShell bool
	maxOutput  int
	tools      map[string]Tool
	permission PermissionFunc
}

// NewRegistry builds the built-in tool set.
func NewRegistry(cwd string, allowShell bool) *Registry {
	r := &Registry{
		cwd:        cwd,
		allowShell: allowShell,
		maxOutput:  defaultMaxOutput,
		tools:      make(map[string]Tool),
	}
	r.register(&readFileTool{r: r})
	r.register(&writeFileTool{r: r})
	r.register(&editFileTool{r: r})
	r.register(&applyPatchTool{r: r})
	r.register(&grepTool{r: r})
	r.register(&globTool{r: r})
	if allowShell {
		r.register(&shellTool{r: r})
	}
	return r
}

func (r *Registry) register(t Tool) {
	r.tools[t.Def().Name] = t
}

// Register adds a tool. Returns an error if the name collides.
func (r *Registry) Register(t Tool) error {
	name := t.Def().Name
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("tool already registered: %s", name)
	}
	r.tools[name] = t
	return nil
}

// SetPermission installs a mutating-tool gate. nil clears the gate.
func (r *Registry) SetPermission(fn PermissionFunc) {
	r.permission = fn
}

// PermissionFunc decides allow/deny/rewrite for mutating tools.
type PermissionFunc func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error)

var mutatingTools = map[string]bool{
	"write_file":  true,
	"edit_file":   true,
	"apply_patch": true,
	"shell":       true,
}

// IsMutating reports whether a built-in tool changes the workspace or runs commands.
func IsMutating(name string) bool {
	return mutatingTools[name]
}

// Defs returns tool schemas for the LLM.
func (r *Registry) Defs() []llm.ToolDef {
	out := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Def())
	}
	return out
}

// Execute runs a named tool.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (res Result) {
	defer func() {
		if rec := recover(); rec != nil {
			res = Result{Content: fmt.Sprintf("tool panic: %v", rec), IsError: true}
		}
	}()
	t, ok := r.tools[name]
	if !ok {
		return Result{Content: "unknown tool: " + name, IsError: true}
	}
	execArgs := args
	if r.permission != nil && IsMutating(name) {
		next, err := r.permission(ctx, name, args)
		if err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
		if next != nil {
			execArgs = next
		}
	}
	res = t.Execute(ctx, execArgs)
	res.Content = truncate(res.Content, r.maxOutput)
	return res
}

func (r *Registry) resolvePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	cwdResolved, err := filepath.EvalSymlinks(r.cwd)
	if err != nil {
		cwdResolved = filepath.Clean(r.cwd)
	} else {
		cwdResolved = filepath.Clean(cwdResolved)
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(cwdResolved, p))
	}
	// Resolve symlinks when the path exists; for new files keep cleaned abs under cwd.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(cwdResolved, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes cwd: %s", p)
	}
	return abs, nil
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n…[truncated]"
}

func mustSchema(s string) json.RawMessage { return json.RawMessage(s) }

// --- read_file ---

type readFileTool struct{ r *Registry }

func (t *readFileTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "read_file",
		Description: "Read a file under the workspace. Optional offset/limit are 1-based line numbers.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"offset":{"type":"integer"},
				"limit":{"type":"integer"}
			},
			"required":["path"]
		}`),
	}
}

func (t *readFileTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	abs, err := t.r.resolvePath(in.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	text := string(data)
	if in.Offset > 0 || in.Limit > 0 {
		lines := strings.Split(text, "\n")
		start := 0
		if in.Offset > 0 {
			start = in.Offset - 1
			if start > len(lines) {
				start = len(lines)
			}
		}
		end := len(lines)
		if in.Limit > 0 && start+in.Limit < end {
			end = start + in.Limit
		}
		text = strings.Join(lines[start:end], "\n")
	}
	return Result{Content: text}
}

// --- write_file ---

type writeFileTool struct{ r *Registry }

func (t *writeFileTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "write_file",
		Description: "Create or overwrite a file under the workspace.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"content":{"type":"string"}
			},
			"required":["path","content"]
		}`),
	}
}

func (t *writeFileTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	abs, err := t.r.resolvePath(in.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if err := os.WriteFile(abs, []byte(in.Content), 0o644); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: "wrote " + in.Path}
}

// --- edit_file ---

type editFileTool struct{ r *Registry }

func (t *editFileTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "edit_file",
		Description: "Exact string replace in a file. old_string must appear exactly once.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"old_string":{"type":"string"},
				"new_string":{"type":"string"}
			},
			"required":["path","old_string","new_string"]
		}`),
	}
}

func (t *editFileTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	var in struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	abs, err := t.r.resolvePath(in.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	text := string(data)
	count := strings.Count(text, in.OldString)
	if count == 0 {
		hint := nearestContextHint(text, in.OldString)
		msg := "old_string not found"
		if hint != "" {
			msg += "; nearest context:\n" + hint
		}
		msg += "; tip: read_file first or use apply_patch with more context"
		return Result{Content: msg, IsError: true}
	}
	if count > 1 {
		return Result{Content: fmt.Sprintf(
			"old_string found %d times; must be unique. Include surrounding lines in old_string, or use apply_patch with @@ hunks.",
			count,
		), IsError: true}
	}
	updated := strings.Replace(text, in.OldString, in.NewString, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: "edited " + in.Path}
}

// --- grep ---

type grepTool struct{ r *Registry }

func (t *grepTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "grep",
		Description: "Search file contents under the workspace with a Go regexp.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"pattern":{"type":"string"},
				"path":{"type":"string"},
				"glob":{"type":"string"}
			},
			"required":["pattern"]
		}`),
	}
}

func (t *grepTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	root := t.r.cwd
	if in.Path != "" {
		root, err = t.r.resolvePath(in.Path)
		if err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
	}
	var matches []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if in.Glob != "" {
			base := filepath.Base(path)
			ok, _ := filepath.Match(in.Glob, base)
			if !ok {
				return nil
			}
		}
		rel, err := filepath.Rel(t.r.cwd, path)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), i+1, line))
				if len(matches) >= 200 {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if len(matches) == 0 {
		return Result{Content: "no matches"}
	}
	return Result{Content: strings.Join(matches, "\n")}
}

// --- glob ---

type globTool struct{ r *Registry }

func (t *globTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "glob",
		Description: "List files under the workspace matching a glob pattern (matched against relative paths).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"pattern":{"type":"string"}
			},
			"required":["pattern"]
		}`),
	}
}

func (t *globTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	var in struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var hits []string
	_ = filepath.WalkDir(t.r.cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(t.r.cwd, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		ok, _ := filepath.Match(in.Pattern, relSlash)
		if !ok {
			ok, _ = filepath.Match(in.Pattern, filepath.Base(relSlash))
		}
		// Also support **/. style via simple contains of suffix
		if !ok && strings.HasPrefix(in.Pattern, "**/") {
			ok, _ = filepath.Match(strings.TrimPrefix(in.Pattern, "**/"), filepath.Base(relSlash))
		}
		if ok {
			hits = append(hits, relSlash)
			if len(hits) >= 500 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return Result{Content: "no files"}
	}
	return Result{Content: strings.Join(hits, "\n")}
}

// --- shell ---

type shellTool struct{ r *Registry }

func (t *shellTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "shell",
		Description: "Run a shell command in the workspace cwd. Prefer targeted commands.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"command":{"type":"string"}
			},
			"required":["command"]
		}`),
	}
}

func (t *shellTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if in.Command == "" {
		return Result{Content: "command is required", IsError: true}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if isWindows() {
		cmd = exec.CommandContext(ctx, "cmd", "/C", in.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", in.Command)
	}
	cmd.Dir = t.r.cwd
	out, err := cmd.CombinedOutput()
	content := string(out)
	if err != nil {
		if content == "" {
			content = err.Error()
		} else {
			content = content + "\n" + err.Error()
		}
		return Result{Content: content, IsError: true}
	}
	if content == "" {
		content = "(no output)"
	}
	return Result{Content: content}
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}

func nearestContextHint(text, needle string) string {
	lines := strings.Split(text, "\n")
	needleLines := strings.Split(needle, "\n")
	key := needleLines[0]
	if key == "" && len(needleLines) > 1 {
		key = needleLines[1]
	}
	if key == "" {
		return ""
	}
	for i, line := range lines {
		if strings.Contains(line, key) || (len(key) > 8 && strings.Contains(line, key[:min(16, len(key))])) {
			start := i - 1
			if start < 0 {
				start = 0
			}
			end := i + 2
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[start:end], "\n")
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
