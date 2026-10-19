// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/checkpoint"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/lsp"
	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/tasks"
)

const defaultMaxOutput = 64 * 1024

// Heavy / generated trees that make full-repo WalkDir unusable (e.g. Unreal Engine).
var skipDirNames = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	"node_modules": true, ".next": true, "dist": true, "build": true,
	"Intermediate": true, "Binaries": true, "DerivedDataCache": true,
	"Saved": true, "__pycache__": true, ".anvil": true,
}

const (
	grepMaxMatches   = 200
	globMaxHits      = 500
	grepMaxFileBytes = 2 << 20 // skip files larger than 2MiB in walk fallback
	searchBudget     = 45 * time.Second
)

func shouldSkipDir(name string) bool {
	return skipDirNames[name]
}

// Result is a tool execution outcome.
type Result struct {
	Content string
	IsError bool
	// Rule is the workspace rule that decided this call, when one did. It rides
	// on the result rather than being announced where it is decided because the
	// registry has no event sink: the runtime, which has one, reads it from here
	// and puts it on the tool_result, so a card can say which rule allowed a call
	// that never asked.
	Rule *permissions.Decision
	// Todos is the list a todo_write call left behind. It is read only from a
	// successful call named that: an empty slice is a list that was cleared,
	// which is a different statement from a call that wrote none.
	Todos []TodoItem
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
	permGroups map[string]bool
	permRules  *permissions.Set
	// permSource, when set, re-reads the rule file for each call and supersedes
	// permRules. See SetPermissionRulesSource.
	permSource *permissions.Source
	preExec    PreExecFunc
	postExec   PostExecFunc
	indexEmbed IndexEmbed
	skillsCfg  SkillsConfig
	webCfg     WebConfig
	lsp        *lsp.Supervisor
	fileSync   func(ctx context.Context, absPath string)
	verifyCfg  VerifyConfig
	visionCfg  VisionConfig
	visionMu   sync.Mutex
	visionQ    []llm.ContentPart
	// cpDir is the checkpoint root. Empty means capture is off, which is what
	// every caller that does not ask for it gets.
	cpDir string
	// cpAgent and cpRun name the run whose pre-images are being written. They
	// are set before a run starts and cleared with it; an empty either keeps
	// capture off, so a bare Registry stays unaffected.
	cpAgent string
	cpRun   string
	// agentID and depth identify the agent this registry executes for, so a
	// permission request can say which agent is asking and how deep it is
	// nested. Set once at creation; runID comes from SetRunContext.
	agentID string
	depth   int
	// taskMgr is the agent tree's background table. It is set before any run
	// starts; the task tools and a background shell read it at call time.
	taskMgr *tasks.Manager
}

// PreExecFunc runs before every tool (hooks like beforeMCP / beforeRead).
type PreExecFunc func(ctx context.Context, name string, args json.RawMessage) error

// PostExecFunc may rewrite tool results (afterTool).
type PostExecFunc func(ctx context.Context, name string, args json.RawMessage, res Result) Result

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
	r.register(&semanticSearchTool{r: r})
	r.register(&listSkillsTool{r: r})
	r.register(&readSkillTool{r: r})
	r.register(&gitStatusTool{r: r})
	r.register(&gitDiffTool{r: r})
	r.register(&gitLogTool{r: r})
	r.register(&todoWriteTool{r: r})
	if allowShell {
		r.register(&shellTool{r: r})
		r.EnsureTaskTools()
	}
	return r
}

// EnsureTaskTools registers task_list, task_output and task_cancel when they
// are absent. NewRegistry calls it beside shell; buildRegistry calls it when
// subagents are enabled, so a child without shell can still read the tree's
// background work.
func (r *Registry) EnsureTaskTools() {
	if r == nil {
		return
	}
	if _, ok := r.tools["task_list"]; !ok {
		r.register(&taskListTool{r: r})
		r.register(&taskOutputTool{r: r})
		r.register(&taskCancelTool{r: r})
	}
}

// SetTaskManager attaches the agent tree's background task table.
func (r *Registry) SetTaskManager(mgr *tasks.Manager) {
	if r != nil {
		r.taskMgr = mgr
	}
}

// TaskManager returns the table, or nil when the registry was built without one.
func (r *Registry) TaskManager() *tasks.Manager {
	if r == nil {
		return nil
	}
	return r.taskMgr
}

// SetPreExec installs a gate that runs for all tools before execute.
func (r *Registry) SetPreExec(fn PreExecFunc) {
	if r != nil {
		r.preExec = fn
	}
}

// SetPostExec installs an after-tool rewriter.
func (r *Registry) SetPostExec(fn PostExecFunc) {
	if r != nil {
		r.postExec = fn
	}
}

// SetCheckpoints turns pre-run capture on, writing under dir. Empty dir is off.
func (r *Registry) SetCheckpoints(dir string) {
	if r != nil {
		r.cpDir = dir
	}
}

// SetRunContext names the run capture belongs to. An empty id switches capture
// off for this registry, which is what a closed or not-yet-started run is.
func (r *Registry) SetRunContext(agentID, runID string) {
	if r != nil {
		r.cpAgent = agentID
		r.cpRun = runID
	}
}

// SetIdentity names the agent this registry serves and its nesting depth. It is
// called once, when the agent is created; a permission request carries both so a
// client can say which subagent is asking.
func (r *Registry) SetIdentity(agentID string, depth int) {
	if r != nil {
		r.agentID = agentID
		r.depth = depth
	}
}

// Identity returns the agent this registry executes for and its depth.
func (r *Registry) Identity() (string, int) {
	if r == nil {
		return "", 0
	}
	return r.agentID, r.depth
}

// RunID returns the run currently executing, or "" outside one. It is the same
// value capture is scoped to, read here so a permission request can name the run
// that asked rather than the one a session happens to have active.
func (r *Registry) RunID() string {
	if r == nil {
		return ""
	}
	return r.cpRun
}

// callIDKey is the context key for the executing tool call id.
type callIDKey struct{}

// WithCallID tags ctx with the tool call id the runtime is executing. A tool
// that starts work on another agent's behalf (spawn_agent) reads it so the events
// it bridges land under the card for that call.
func WithCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, callIDKey{}, id)
}

// CallID returns the tool call id in ctx, or "" when the call did not come from
// the runtime's tool loop.
func CallID(ctx context.Context) string {
	id, _ := ctx.Value(callIDKey{}).(string)
	return id
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

// Unregister removes a tool by name. No-op if missing.
func (r *Registry) Unregister(name string) {
	if r == nil {
		return
	}
	delete(r.tools, name)
}

// Names returns registered tool names (unsorted).
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	return out
}

// SetPermission installs a tool gate. nil clears the gate.
func (r *Registry) SetPermission(fn PermissionFunc) {
	r.permission = fn
}

// SetPermissionGroups widens the gate beyond the mutating built-ins.
//
// Empty leaves the historical behaviour untouched: only write_file, edit_file,
// apply_patch, and shell are gated. Non-empty gates the built-in tools covered
// by the selectors (group names or literal tool names) plus every external tool
// — MCP servers, plugins, and caller-registered Options.Tools — because those
// belong to no group and would otherwise run unattended.
//
// Groups alone open no gate: with no PermissionFunc installed there is nobody
// to ask, and Execute keeps running every tool. A caller that wants an approval
// prompt must install both.
func (r *Registry) SetPermissionGroups(selectors []string) {
	if r == nil {
		return
	}
	r.permGroups = ExpandToolSelectors(selectors)
}

// SetPermissionRules installs the workspace rule set. nil clears it.
//
// The rules are evaluated in Execute rather than inside PermissionFunc, and the
// paragraph above is the reason: a callback is optional, so a policy that has to
// hold for a caller which installed none cannot live in one. A rule therefore
// denies for the CLI and for a library embedder, and it denies in bypass, where
// there is no prompt to answer; a rule that allows skips a prompt that was never
// going to be answered differently.
//
// With no rules installed, the decision path is exactly what it was before this
// existed.
func (r *Registry) SetPermissionRules(set *permissions.Set) {
	if r == nil {
		return
	}
	r.permRules = set
}

// SetPermissionRulesSource installs a rule set that is re-read for each call,
// which is what a policy file in an open workspace needs: the user edits it, and
// the editor's approval card appends to it, while the session is running.
//
// A source replaces any static set. Callers whose rules are constants keep using
// SetPermissionRules, where the evaluation path is untouched by any of this.
func (r *Registry) SetPermissionRulesSource(src *permissions.Source) {
	if r == nil {
		return
	}
	r.permSource = src
}

// PermissionFunc decides allow/deny/rewrite for a gated tool call, given the
// call's preview and, when one asked for it, the rule that did.
type PermissionFunc func(ctx context.Context, call GatedCall) (json.RawMessage, error)

// GatedCall is one call reaching the permission hook.
type GatedCall struct {
	Name string
	Args json.RawMessage
	// Preview is what the call would do, computed without doing it.
	Preview Preview
	// Rule is the workspace rule that insisted on this prompt, when one did. It
	// is nil when the mode gate alone asked, so a client can say why it is being
	// asked instead of only that it is.
	Rule *permissions.Decision
}

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

// gated reports whether the permission hook applies to this call.
func (r *Registry) gated(name string, t Tool) bool {
	// todo_write writes nothing a permission could protect: its only effect is
	// an in-memory list and a count in the transcript. A gate on it could only
	// stop the model from showing its plan.
	if name == TodoToolName {
		return false
	}
	if len(r.permGroups) == 0 {
		return IsMutating(name)
	}
	if IsExternalTool(t) {
		return true
	}
	return SelectorSetMatches(name, r.permGroups)
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
	if r.preExec != nil {
		if err := r.preExec(ctx, name, execArgs); err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
	}
	// The workspace's written policy is consulted before the callback, because
	// the callback is optional and the policy is not. See SetPermissionRules.
	rules := r.permRules
	if r.permSource != nil {
		// The file is the interface, so it is read per call: a rule the user just
		// wrote — by hand, or by choosing "always allow" on an approval card —
		// decides the next call rather than the next session. A read that fails
		// yields the last policy that read cleanly; see permissions.Source.Read.
		rules, _ = r.permSource.Read()
	}
	var decision permissions.Decision
	if rules.Len() > 0 {
		decision = rules.Evaluate(callFacts(name, args), runtime.GOOS)
	}
	var decidedBy *permissions.Decision
	if decision.Matched {
		decidedBy = &decision
	}
	if decision.Matched && decision.Effect == permissions.Deny {
		return Result{Content: ruleDenyMessage(name, decision), IsError: true, Rule: decidedBy}
	}
	// A rule that asks insists on a prompt even for a tool the gate would not
	// have covered; a rule that allows runs the call without one; and no rule at
	// all leaves the gate to decide, exactly as before.
	ask := r.gated(name, t)
	if decision.Matched {
		ask = decision.Effect == permissions.Ask
	}
	// askedBy is what the callback is told: the rule that asked, or nothing when
	// the mode gate is the reason. An allow never reaches the callback at all.
	var askedBy *permissions.Decision
	if ask && decision.Matched {
		askedBy = decidedBy
	}
	if ask {
		switch {
		case r.permission != nil:
			// A denied call is reported to the model as a tool error rather than
			// aborting the run, so the model can adapt instead of dying.
			next, err := r.permission(ctx, GatedCall{
				Name:    name,
				Args:    args,
				Preview: r.PreviewFor(name, args),
				Rule:    askedBy,
			})
			if err != nil {
				return Result{Content: err.Error(), IsError: true, Rule: decidedBy}
			}
			if next != nil {
				execArgs = next
			}
		case decision.Matched:
			// Nobody to ask, and a rule that says "ask" must never quietly become
			// "run it".
			return Result{Content: ruleNoChannelMessage(name, decision), IsError: true, Rule: decidedBy}
		}
	}
	// The pre-image is taken from the arguments the call will actually run
	// with: an approval may have rewritten them, and the file that changes is
	// the one in the rewritten call, not the one the model first named.
	r.captureBefore(name, execArgs)
	res = t.Execute(ctx, execArgs)
	res.Content = truncate(res.Content, r.maxOutput)
	if r.postExec != nil {
		res = r.postExec(ctx, name, execArgs, res)
		res.Content = truncate(res.Content, r.maxOutput)
	}
	res.Rule = decidedBy
	return res
}

// callFacts are the facts a permission rule may match on: the tool name, the
// shell command line, and the paths the call touches. All three are functions of
// the arguments alone, so a rule costs no dry run and no disk read — and a rule
// can only ever match what the approval card shows.
func callFacts(name string, args json.RawMessage) permissions.CallFacts {
	return permissions.CallFacts{
		Tool:    name,
		Command: commandOf(name, args),
		Paths:   pathsOf(name, args),
	}
}

// captureBefore records the pre-image of every path this call names.
//
// It is the last thing done before execution and the first thing that can
// observe the arguments a permission rewrite produced. Nothing here may turn a
// successful call into a failed one, so every error is dropped: a missing
// checkpoint is a gap in the undo history, while a refusal here would be a
// tool that stops working because a backup could not be filed.
func (r *Registry) captureBefore(name string, args json.RawMessage) {
	if r.cpDir == "" || r.cpAgent == "" || r.cpRun == "" {
		return
	}
	root := r.cwd
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = filepath.Clean(resolved)
	}
	for _, p := range pathsOf(name, args) {
		abs, err := r.resolvePath(p)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		_ = checkpoint.Capture(r.cpDir, r.cpAgent, r.cpRun, filepath.ToSlash(rel), abs)
	}
}

// CallFactsFor is callFacts, exported for the SDK's public rule helpers.
//
// A client that explains a rule decision — which "always allow" may be written
// for a call, whether a rule would decide it — has to evaluate rules against the
// same facts the registry itself decides on. Letting it derive those facts from
// the wire instead is how an explanation starts disagreeing with the decision it
// explains, so the facts are read here, from the arguments, exactly once.
func CallFactsFor(name string, args json.RawMessage) permissions.CallFacts {
	return callFacts(name, args)
}

// ruleDenyMessage explains a denial to the model the way the file spells the
// rule, so it can be quoted back and found.
func ruleDenyMessage(name string, d permissions.Decision) string {
	return name + " denied by " + d.Ref()
}

// ruleNoChannelMessage is the fail-closed answer when a rule asks for
// confirmation in a run that has no way to ask for it.
func ruleNoChannelMessage(name string, d permissions.Decision) string {
	return name + " denied: " + d.Ref() + " asks for confirmation and no approval channel is installed"
}

func (r *Registry) resolvePath(p string) (string, error) {
	return ResolveInRoot(r.cwd, p)
}

// ResolveInRoot resolves p against root and refuses anything that would leave
// it. It is the one sandbox check: the tools use it before touching a file, the
// approval preview uses it before reading one, and checkpoint restore uses it
// before writing one back — so a path a checkpoint manifest names is held to
// exactly the boundary a tool argument is.
func ResolveInRoot(root, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	cwdResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		cwdResolved = filepath.Clean(root)
	} else {
		cwdResolved = filepath.Clean(cwdResolved)
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(cwdResolved, p))
	}
	// Resolve symlink components even when the final leaf does not exist yet
	// (e.g. write through a directory symlink that points outside cwd).
	abs, err = resolveSymlinkPath(abs)
	if err != nil {
		return "", fmt.Errorf("path escapes cwd: %s", p)
	}
	rel, err := filepath.Rel(cwdResolved, abs)
	if err != nil || !pathInsideRoot(rel) {
		return "", fmt.Errorf("path escapes cwd: %s", p)
	}
	return abs, nil
}

// resolveSymlinkPath returns abs with all existing symlink prefixes evaluated.
func resolveSymlinkPath(abs string) (string, error) {
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	// Walk up to the longest existing ancestor, resolve it, then re-join the suffix.
	suffix := make([]string, 0, 4)
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		suffix = append([]string{filepath.Base(cur)}, suffix...)
		if _, err := os.Lstat(parent); err != nil {
			cur = parent
			continue
		}
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return abs, nil
		}
		return filepath.Clean(filepath.Join(append([]string{resolvedParent}, suffix...)...)), nil
	}
	return abs, nil
}

func pathInsideRoot(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
	t.r.notifyFileWrite(ctx, abs)
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
	t.r.notifyFileWrite(ctx, abs)
	return Result{Content: "edited " + in.Path}
}

// --- grep ---

type grepTool struct{ r *Registry }

func (t *grepTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "grep",
		Description: "Search file contents under the workspace. Prefer a narrow path " +
			"(e.g. Engine/Source/Runtime/Engine) — full Engine trees are huge. " +
			"Uses ripgrep when available; skips Intermediate/Binaries/.git and similar.",
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
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if in.Pattern == "" {
		return Result{Content: "pattern is required", IsError: true}
	}
	root := t.r.cwd
	var err error
	if in.Path != "" {
		root, err = t.r.resolvePath(in.Path)
		if err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, searchBudget)
	defer cancel()

	if out, ok, res := t.grepRipgrep(ctx, root, in.Pattern, in.Glob); ok {
		if res.IsError {
			return res
		}
		if out == "" {
			return Result{Content: "no matches"}
		}
		return Result{Content: out}
	}
	return t.grepWalk(ctx, root, in.Pattern, in.Glob)
}

func (t *grepTool) grepRipgrep(ctx context.Context, root, pattern, glob string) (string, bool, Result) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		return "", false, Result{}
	}
	cwdAbs, err := filepath.Abs(t.r.cwd)
	if err != nil {
		cwdAbs = t.r.cwd
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		rootAbs = root
	}
	searchRoot := rootAbs
	if rel, err := filepath.Rel(cwdAbs, rootAbs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		searchRoot = rel
	}
	args := []string{"-n", "--no-heading", "--color", "never",
		"--max-count", "20", // per-file
		"-m", fmt.Sprintf("%d", grepMaxMatches),
		"-g", "!**/Intermediate/**",
		"-g", "!**/Binaries/**",
		"-g", "!**/.git/**",
		"-g", "!**/DerivedDataCache/**",
		"-g", "!**/Saved/**",
		"-g", "!**/node_modules/**",
	}
	if glob != "" {
		args = append(args, "-g", glob)
	}
	args = append(args, "--", pattern, searchRoot)
	cmd := exec.CommandContext(ctx, rg, args...)
	cmd.Dir = cwdAbs
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() != nil {
			return "", true, Result{Content: "grep timed out; narrow path/glob and retry", IsError: true}
		}
		// rg exit 1 = no matches
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "", true, Result{}
		}
		// Fall back to walk on other rg failures (bad regex dialect, etc.)
		return "", false, Result{}
	}
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		relLine, pathPart, ok := normalizeGrepLine(cwdAbs, line)
		if !ok {
			continue
		}
		if pathHasSkippedDir(pathPart) {
			continue
		}
		kept = append(kept, relLine)
	}
	return strings.Join(kept, "\n"), true, Result{}
}

func splitGrepLine(line string) (pathPart, rest string, ok bool) {
	// path:line:content — on Windows path may be C:\...
	start := 0
	if len(line) >= 3 && line[1] == ':' && (line[2] == '\\' || line[2] == '/') {
		start = 2
	}
	idx := strings.Index(line[start:], ":")
	if idx < 0 {
		return "", "", false
	}
	idx += start
	return line[:idx], line[idx:], true
}

func normalizeGrepLine(cwd, line string) (relLine, pathPart string, ok bool) {
	pathPart, rest, ok := splitGrepLine(line)
	if !ok {
		return "", "", false
	}
	if !filepath.IsAbs(pathPart) {
		return filepath.ToSlash(pathPart) + rest, pathPart, true
	}
	rel, err := filepath.Rel(cwd, pathPart)
	if err != nil || strings.HasPrefix(rel, "..") {
		// Best-effort prefix strip when short/long path forms differ.
		cwdSlash := strings.ToLower(filepath.ToSlash(cwd))
		pathSlash := filepath.ToSlash(pathPart)
		if strings.HasPrefix(strings.ToLower(pathSlash), cwdSlash+"/") {
			rel = pathSlash[len(cwdSlash)+1:]
		} else {
			return filepath.ToSlash(pathPart) + rest, pathPart, true
		}
	}
	return filepath.ToSlash(rel) + rest, pathPart, true
}

func pathHasSkippedDir(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if shouldSkipDir(part) {
			return true
		}
	}
	return false
}

func (t *grepTool) grepWalk(ctx context.Context, root, pattern, glob string) Result {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var matches []string
	timedOut := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			timedOut = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" {
			base := filepath.Base(path)
			ok, _ := filepath.Match(glob, base)
			if !ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil || info.Size() > grepMaxFileBytes {
			return nil
		}
		rel, err := filepath.Rel(t.r.cwd, path)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if isMostlyBinary(data) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), i+1, line))
				if len(matches) >= grepMaxMatches {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if timedOut && len(matches) == 0 {
		return Result{Content: "grep timed out; narrow path/glob and retry", IsError: true}
	}
	if len(matches) == 0 {
		return Result{Content: "no matches"}
	}
	out := strings.Join(matches, "\n")
	if timedOut {
		out += "\n…[grep stopped early due to time budget; narrow path]"
	}
	return Result{Content: out}
}

func isMostlyBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// --- glob ---

type globTool struct{ r *Registry }

func (t *globTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "glob",
		Description: "List files under the workspace matching a glob pattern (relative paths). " +
			"Skips Intermediate/Binaries/.git and similar generated trees.",
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
	var in struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	ctx, cancel := context.WithTimeout(ctx, searchBudget)
	defer cancel()

	var hits []string
	timedOut := false
	_ = filepath.WalkDir(t.r.cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			timedOut = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
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
		// Also support **/prefix style via basename match
		if !ok && strings.HasPrefix(in.Pattern, "**/") {
			ok, _ = filepath.Match(strings.TrimPrefix(in.Pattern, "**/"), filepath.Base(relSlash))
		}
		if ok {
			hits = append(hits, relSlash)
			if len(hits) >= globMaxHits {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if timedOut && len(hits) == 0 {
		return Result{Content: "glob timed out; use a tighter pattern", IsError: true}
	}
	if len(hits) == 0 {
		return Result{Content: "no files"}
	}
	out := strings.Join(hits, "\n")
	if timedOut {
		out += "\n…[glob stopped early due to time budget]"
	}
	return Result{Content: out}
}

// --- shell ---

type shellTool struct{ r *Registry }

const (
	defaultShellTimeout = 60 * time.Second
	minShellTimeout     = time.Second
	maxShellTimeout     = 120 * time.Second
	// A background job is unwaited, not unbounded; it may run for an hour.
	maxBackgroundShellTimeout = 3600 * time.Second
)

func (t *shellTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "shell",
		Description: "Run a shell command in the workspace cwd. Prefer targeted commands. " +
			"Set timeout_seconds from how long this command should need (1–120, default 60; up to 3600 in the background). " +
			"When that time elapses, or the turn is stopped, the command and the processes it started are killed and this call returns. " +
			"Set run_in_background for a long job: the call returns a task id at once and the job keeps running; " +
			"read it with task_output and stop it with task_cancel.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"command":{"type":"string"},
				"timeout_seconds":{"type":"integer","minimum":1,"description":"Kill the command and its child processes after this many seconds. Default 60; 1–120 in the foreground, up to 3600 in the background."},
				"run_in_background":{"type":"boolean","description":"Start the command as a background task and return its task id instead of waiting."}
			},
			"required":["command"]
		}`),
	}
}

func (t *shellTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		Command         string `json:"command"`
		TimeoutSeconds  int    `json:"timeout_seconds"`
		RunInBackground bool   `json:"run_in_background"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if in.Command == "" {
		return Result{Content: "command is required", IsError: true}
	}

	if in.RunInBackground {
		mgr := t.r.taskMgr
		if mgr == nil {
			return Result{Content: "background tasks are not available in this registry", IsError: true}
		}
		timeout := defaultShellTimeout
		if in.TimeoutSeconds != 0 {
			if in.TimeoutSeconds < 1 || in.TimeoutSeconds > int(maxBackgroundShellTimeout/time.Second) {
				return Result{Content: "background timeout_seconds must be between 1 and 3600", IsError: true}
			}
			timeout = time.Duration(in.TimeoutSeconds) * time.Second
		}
		info, err := mgr.StartShell(tasks.ShellSpec{
			Title:   in.Command,
			Command: in.Command,
			CWD:     t.r.cwd,
			AgentID: t.r.agentID,
			Timeout: timeout,
			Prepare: configureShellCommand,
		})
		if err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
		return Result{Content: fmt.Sprintf(
			"%s started in the background: %s\nRead it with task_output, stop it with task_cancel.",
			info.ID, in.Command)}
	}

	timeout := defaultShellTimeout
	if in.TimeoutSeconds != 0 {
		if in.TimeoutSeconds < 1 || in.TimeoutSeconds > int(maxShellTimeout/time.Second) {
			return Result{Content: "timeout_seconds must be between 1 and 120", IsError: true}
		}
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if isWindows() {
		cmd = exec.CommandContext(ctx, "cmd", "/C", in.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", in.Command)
	}
	cmd.Dir = t.r.cwd
	configureShellCommand(cmd)
	out, err := cmd.CombinedOutput()
	content := string(out)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			note := fmt.Sprintf("command killed after %s", timeout)
			if content == "" {
				content = note
			} else {
				content = content + "\n" + note
			}
		} else if content == "" {
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
