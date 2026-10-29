// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/tasks"
	"github.com/anvil-dev/anvil/internal/tools"
)

// subagentIdentity is what Create stamps onto a child's meta record. A top-level
// session passes the zero value and writes exactly the record it wrote before.
type subagentIdentity struct {
	ParentAgentID    string
	ParentRunID      string
	ParentToolCallID string
	TaskTitle        string
	Depth            int
	// Brief is the validated delegate brief, written to the child's meta record so a
	// replay of the parent shows what the child was asked (0.50+). Nil for spawn_agent.
	Brief *Brief
}

// subagentBudget bounds how many agents one root run tree may create. The
// counter is shared by every agent in the tree and reset by the root's run.
type subagentBudget struct {
	mu   sync.Mutex
	max  int
	used int
}

func newSubagentBudget(max int) *subagentBudget {
	if max <= 0 {
		max = 8
	}
	return &subagentBudget{max: max}
}

// take claims one agent from the budget, or reports the limit in words.
func (b *subagentBudget) take() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used >= b.max {
		return fmt.Errorf("this run has already created %d agents; raise Subagents.MaxAgents to allow more", b.max)
	}
	b.used++
	return nil
}

// give returns one agent to the budget. A slot is only consumed by an agent that
// actually starts: when the start is refused, or the child cannot be created, the
// slot goes back, so the next spawn is not refused for an agent that never ran
// (audit #22).
func (b *subagentBudget) give() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used > 0 {
		b.used--
	}
}

// remaining is how many more agents the tree may create; negative means unbounded.
func (b *subagentBudget) remaining() int {
	if b == nil {
		return -1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.max - b.used
}

// reset starts a new root run's tree budget.
func (b *subagentBudget) reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.used = 0
	b.mu.Unlock()
}

// subagentMaxDepth resolves the configured nesting limit. 0 means the default
// of 2: a root (depth 0) and one level of children (depth 1).
func subagentMaxDepth(o SubagentOptions) int {
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return 2
}

// ---------------------------------------------------------------------------
// context: the agent executing a tool, and the call that created a child
// ---------------------------------------------------------------------------

type agentCtxKey struct{}

// withAgent tags a run's context with the agent it belongs to, so a tool that
// acts for the agent (spawn_agent) can copy its options without the registry
// holding a back-reference to the Agent.
func withAgent(ctx context.Context, a *Agent) context.Context {
	return context.WithValue(ctx, agentCtxKey{}, a)
}

func agentFromContext(ctx context.Context) *Agent {
	a, _ := ctx.Value(agentCtxKey{}).(*Agent)
	return a
}

type subagentOrigin struct {
	parentAgentID string
	parentRunID   string
	toolCallID    string
	// rootAgentID and rootRunID are the root of the tree this child belongs to: the
	// agent whose Send started it all and that Send's run. Checkpoints are filed there.
	rootAgentID string
	rootRunID   string
}

type subagentOriginKey struct{}

// withSubagentOrigin tags the context a child's run derives from with where it
// came from. The child's executeRun reads it to build SubagentEvent envelopes.
func withSubagentOrigin(ctx context.Context, parent *Agent, parentRunID, toolCallID string) context.Context {
	rootAgent, rootRun := parent.treeRoot()
	return context.WithValue(ctx, subagentOriginKey{}, subagentOrigin{
		parentAgentID: parent.id,
		parentRunID:   parentRunID,
		toolCallID:    toolCallID,
		rootAgentID:   rootAgent,
		rootRunID:     rootRun,
	})
}

// treeRoot is the root of the tree this agent belongs to. A top-level agent is its own
// root, and the run in progress is the root run; a child carries what its own run was
// started with.
func (a *Agent) treeRoot() (agentID, runID string) {
	if a.depth == 0 {
		return a.id, a.currentRunID()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rootAgentID, a.rootRunID
}

// adoptTreeRoot is called when a run starts: a child files its captures under the root's
// run (as its origin says), a top-level agent under its own.
func (a *Agent) adoptTreeRoot(ctx context.Context) {
	if origin, ok := subagentOriginFromContext(ctx); ok && a.depth > 0 && origin.rootAgentID != "" && origin.rootRunID != "" {
		a.mu.Lock()
		a.rootAgentID, a.rootRunID = origin.rootAgentID, origin.rootRunID
		a.mu.Unlock()
		a.registry.SetCheckpointRoot(origin.rootAgentID, origin.rootRunID)
		return
	}
	a.registry.SetCheckpointRoot("", "")
}

func subagentOriginFromContext(ctx context.Context) (subagentOrigin, bool) {
	o, ok := ctx.Value(subagentOriginKey{}).(subagentOrigin)
	return o, ok
}

// ---------------------------------------------------------------------------
// the tool
// ---------------------------------------------------------------------------

// defaultSubagentTools is what a child gets when the caller names no tools: it
// can look, and it can report, but it cannot change the workspace.
var defaultSubagentTools = []string{"read", "search", "skills", "git", "vision"}

type spawnParams struct {
	Prompt      string   `json:"prompt"`
	Description string   `json:"description"`
	Profile     string   `json:"profile"`
	Tools       []string `json:"tools"`
	Model       string   `json:"model"`
	MaxSteps    int      `json:"max_steps"`
	Background  bool     `json:"background"`
	toolsSet    bool
	modelSet    bool
	maxStepsSet bool
}

func (p *spawnParams) UnmarshalJSON(data []byte) error {
	type plain spawnParams
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = spawnParams(value)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	_, p.toolsSet = fields["tools"]
	_, p.modelSet = fields["model"]
	_, p.maxStepsSet = fields["max_steps"]
	return nil
}

// subagentTool builds the spawn_agent FuncTool. It is a closure over the
// parent's registry so the selector can be checked against the tools this
// session actually has, not against a list compiled in here.
func subagentTool(reg *tools.Registry, profiles []SubagentProfile) *tools.FuncTool {
	description := "Hand one task to a subagent and read its final answer. " +
		"The subagent runs in this workspace with the tools you list (an omitted list is read-only), " +
		"and its conversation does not enter yours: you learn only what it reports. " +
		"Use it for work that would otherwise fill your context with searching, or for a second opinion."
	profileProperty := map[string]any{
		"type":        "string",
		"description": "Optional configured specialization. Omit it for the normal inherited subagent.",
	}
	if len(profiles) > 0 {
		ids := make([]string, 0, len(profiles))
		details := make([]string, 0, len(profiles))
		for _, profile := range profiles {
			ids = append(ids, profile.ID)
			details = append(details, profile.ID+": "+profile.Description)
		}
		profileProperty["enum"] = ids
		profileProperty["description"] = "Optional configured specialization. Choose it by task: " + strings.Join(details, "; ")
	}
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt":      map[string]any{"type": "string", "description": "The whole task. The subagent sees nothing else from this conversation."},
			"description": map[string]any{"type": "string", "description": "A short name for the task, shown on its card. Not sent to the subagent."},
			"profile":     profileProperty,
			"tools":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Group or tool names the subagent may use: read, search, write, edit, shell, verify, web, lsp, git, vision, mcp. Default: read, search, skills, git, vision. An explicit value overrides the profile."},
			"model":       map[string]any{"type": "string", "description": "Model id. An explicit value overrides the profile; empty inherits this session's model."},
			"max_steps":   map[string]any{"type": "integer", "minimum": 0, "description": "Optional step ceiling. An explicit value overrides the profile; zero inherits this session's limit."},
			"background":  map[string]any{"type": "boolean", "description": "Return a task id immediately instead of waiting."},
		},
		"required": []string{"prompt", "description"},
	})
	return &tools.FuncTool{
		Name:        "spawn_agent",
		Builtin:     true,
		Description: description,
		Parameters:  schema,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			return spawnAgent(ctx, reg, raw)
		},
	}
}

func spawnAgent(ctx context.Context, reg *tools.Registry, raw json.RawMessage) (string, error) {
	parent := agentFromContext(ctx)
	if parent == nil {
		return "", errors.New("spawn_agent is only available inside a run")
	}
	var in spawnParams
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return "", errors.New("prompt is required")
	}
	if strings.TrimSpace(in.Description) == "" {
		return "", errors.New("description is required")
	}
	resolved, profileInstructions, err := resolveSubagentProfile(parent.opts.Subagents.Profiles, in)
	if err != nil {
		return "", err
	}
	in = resolved
	if in.Background {
		return spawnBackground(ctx, reg, parent, in, profileInstructions)
	}
	selectors, err := resolveSubagentSelectors(reg, in.Tools)
	if err != nil {
		return "", err
	}
	if err := parent.budget.take(); err != nil {
		return "", err
	}

	depth := parent.depth + 1
	opts := parent.childOptions(in, selectors, depth, profileInstructions)
	ident := subagentIdentity{
		ParentAgentID:    parent.id,
		ParentRunID:      parent.currentRunID(),
		ParentToolCallID: tools.CallID(ctx),
		TaskTitle:        strings.TrimSpace(in.Description),
		Depth:            depth,
	}
	callCtx := withSubagentOrigin(ctx, parent, ident.ParentRunID, ident.ParentToolCallID)
	child, err := createSubagent(callCtx, opts, ident)
	if err != nil {
		return "", fmt.Errorf("start subagent: %w", err)
	}
	defer child.Close()

	run, err := child.Send(callCtx, in.Prompt)
	if err != nil {
		return "", fmt.Errorf("start subagent: %w", err)
	}
	res, waitErr := run.Wait(callCtx)
	if waitErr != nil && res == nil {
		return "", fmt.Errorf("%s did not finish: %w", child.AgentID(), waitErr)
	}
	if res == nil {
		return "", fmt.Errorf("%s returned no result", child.AgentID())
	}
	switch res.Status {
	case StatusFinished:
		clampNote := ""
		if in.MaxSteps > 0 && parent.opts.MaxSteps > 0 && in.MaxSteps > parent.opts.MaxSteps {
			clampNote = fmt.Sprintf("\n(max_steps %d clamped to this session's %d)", in.MaxSteps, parent.opts.MaxSteps)
		}
		return fmt.Sprintf("%s finished in %d steps.%s\n%s", child.AgentID(), res.Steps, clampNote, res.Result), nil
	case StatusCancelled:
		return "", fmt.Errorf("%s was cancelled: %s", child.AgentID(), firstNonEmpty(res.Err, "the run stopped"))
	default:
		return "", fmt.Errorf("%s failed: %s", child.AgentID(), firstNonEmpty(res.Err, res.Result))
	}
}

// spawnBackground starts the same child the foreground path builds, but hands
// it to the tree's task table and returns at once. The child is not tied to the
// parent run's context: stopping the turn leaves it working, and only
// task_cancel (or closing the session) stops it.
func spawnBackground(ctx context.Context, reg *tools.Registry, parent *Agent, in spawnParams, profileInstructions string) (string, error) {
	mgr := reg.TaskManager()
	if mgr == nil {
		return "", errors.New("background tasks are not available in this session")
	}
	selectors, err := resolveSubagentSelectors(reg, in.Tools)
	if err != nil {
		return "", err
	}
	if err := parent.budget.take(); err != nil {
		return "", err
	}
	depth := parent.depth + 1
	opts := parent.childOptions(in, selectors, depth, profileInstructions)
	ident := subagentIdentity{
		ParentAgentID:    parent.id,
		ParentRunID:      parent.currentRunID(),
		ParentToolCallID: tools.CallID(ctx),
		TaskTitle:        strings.TrimSpace(in.Description),
		Depth:            depth,
	}
	prompt := in.Prompt
	info, err := mgr.StartAgent(tasks.AgentSpec{
		Title:            "subagent " + ident.TaskTitle,
		AgentID:          parent.id,
		ParentRunID:      ident.ParentRunID,
		ParentToolCallID: ident.ParentToolCallID,
		Run: func(taskCtx context.Context) (string, string, error) {
			callCtx := withSubagentOrigin(taskCtx, parent, ident.ParentRunID, ident.ParentToolCallID)
			child, err := createSubagent(callCtx, opts, ident)
			if err != nil {
				parent.budget.give()
				return "error", "", fmt.Errorf("start subagent: %w", err)
			}
			defer child.Close()
			_ = mgr.SetChildAgent(tasks.IDFromContext(taskCtx), child.AgentID())
			run, err := child.Send(callCtx, prompt)
			if err != nil {
				return "error", "", fmt.Errorf("start subagent: %w", err)
			}
			res, waitErr := run.Wait(callCtx)
			if waitErr != nil && res == nil {
				return "error", "", fmt.Errorf("%s did not finish: %w", child.AgentID(), waitErr)
			}
			if res == nil {
				return "error", "", fmt.Errorf("%s returned no result", child.AgentID())
			}
			switch res.Status {
			case StatusFinished:
				if res.Result == "" {
					return string(res.Status), fmt.Sprintf("%s finished in %d steps without a message.", child.AgentID(), res.Steps), nil
				}
				return string(res.Status), res.Result, nil
			case StatusCancelled:
				return string(res.Status), firstNonEmpty(res.Err, "the run stopped"), nil
			default:
				return string(res.Status), firstNonEmpty(res.Err, res.Result, "the run failed"), nil
			}
		},
	})
	if err != nil {
		parent.budget.give()
		return "", err
	}
	return fmt.Sprintf("%s started in the background: subagent %s\nRead it with task_output, stop it with task_cancel.",
		info.ID, ident.TaskTitle), nil
}

func resolveSubagentProfile(profiles []SubagentProfile, in spawnParams) (spawnParams, string, error) {
	if strings.TrimSpace(in.Profile) == "" {
		return in, "", nil
	}
	var selected *SubagentProfile
	for i := range profiles {
		if profiles[i].ID == in.Profile {
			selected = &profiles[i]
			break
		}
	}
	if selected == nil {
		return in, "", fmt.Errorf("unknown subagent profile %q", in.Profile)
	}
	if err := ValidateSubagentProfiles([]SubagentProfile{*selected}); err != nil {
		return in, "", err
	}
	if !in.modelSet {
		in.Model = selected.Model
	}
	if !in.toolsSet {
		in.Tools = append([]string(nil), selected.Tools...)
	}
	if !in.maxStepsSet {
		in.MaxSteps = selected.MaxSteps
	}
	return in, selected.Instructions, nil
}

// subagentProfileBrief is the brief layer (L3) a profiled child carries: the profile's
// instructions, under the role text and the safety layer rather than in place of them.
// A child spawned without a profile has no brief.
func subagentProfileBrief(profileInstructions []string) string {
	if len(profileInstructions) == 0 || strings.TrimSpace(profileInstructions[0]) == "" {
		return ""
	}
	return "Profile instructions:\n" + strings.TrimSpace(profileInstructions[0])
}

// resolveSubagentSelectors validates the caller's tool list against the session
// and returns the selectors to hand the child. Empty means the read-only
// default. A selector that names no group and no tool this session has is an
// error rather than a silently narrower child.
func resolveSubagentSelectors(reg *tools.Registry, requested []string) ([]string, error) {
	groups := map[string]bool{}
	for _, g := range tools.GroupNames() {
		groups[g] = true
	}
	available := map[string]bool{}
	if reg != nil {
		for _, name := range reg.Names() {
			available[name] = true
		}
	}
	if len(requested) == 0 {
		defaults := make([]string, 0, len(defaultSubagentTools))
		for _, selector := range defaultSubagentTools {
			for name := range tools.ExpandToolSelectors([]string{selector}) {
				if available[name] {
					defaults = append(defaults, selector)
					break
				}
			}
		}
		if len(defaults) == 0 {
			return nil, errors.New("no safe default tools are available to the parent agent")
		}
		return defaults, nil
	}
	out := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, raw := range requested {
		sel := strings.ToLower(strings.TrimSpace(raw))
		if sel == "" {
			continue
		}
		if groups[sel] {
			found := false
			for name := range tools.ExpandToolSelectors([]string{sel}) {
				if available[name] || (sel == "mcp" && strings.HasPrefix(name, "mcp_")) {
					found = true
					break
				}
			}
			if sel == "mcp" {
				for name := range available {
					if strings.HasPrefix(name, "mcp_") {
						found = true
						break
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("tools: %q is not available to the parent agent", raw)
			}
		} else {
			expanded := tools.ExpandToolSelectors([]string{sel})
			found := false
			for name := range expanded {
				if available[name] {
					found = true
					break
				}
			}
			if !found {
				names := tools.GroupNames()
				sort.Strings(names)
				return nil, fmt.Errorf("tools: %q is not a tool group or a tool this session has (groups: %s)",
					raw, strings.Join(names, ", "))
			}
		}
		if seen[sel] {
			continue
		}
		seen[sel] = true
		out = append(out, sel)
	}
	if len(out) == 0 {
		return resolveSubagentSelectors(reg, nil)
	}
	return out, nil
}

// childOptions builds the child's Options from a whitelist of the parent's. See
// doc/design/v0.x/41-subagents-and-tasks.md — the exclusions are deliberate.
func (a *Agent) childOptions(in spawnParams, selectors []string, depth int, profileInstructions ...string) Options {
	sel := tools.ExpandToolSelectors(selectors)
	opts := Options{
		Provider:         a.opts.Provider,
		APIKey:           a.opts.APIKey,
		BaseURL:          a.opts.BaseURL,
		Model:            a.opts.Model,
		MaxSteps:         a.opts.MaxSteps,
		MaxWallTime:      a.opts.MaxWallTime,
		Effort:           a.opts.Effort,
		Retry:            a.opts.Retry,
		MaxOutputTokens:  a.opts.MaxOutputTokens,
		Permission:       a.opts.Permission,
		PermissionGroups: append([]string(nil), a.opts.PermissionGroups...),
		PermissionRules:  append([]PermissionRule(nil), a.opts.PermissionRules...),
		ParallelTools:    a.opts.ParallelTools,
		ParallelToolsMax: a.opts.ParallelToolsMax,
		Checkpoints:      a.opts.Checkpoints,
		// A command hook that denies in the parent denies in the child too, so the child
		// must carry the same switch that lets command hooks run (audit #25).
		AllowHookCommands: a.opts.AllowHookCommands,
		Role:              RoleSubagent,
		brief:             subagentProfileBrief(profileInstructions),
		RequireFinishTask: a.opts.RequireFinishTask,
		BuiltinTools:      selectors,
		DisallowedTools:   append([]string(nil), a.opts.DisallowedTools...),
		Subagents:         a.opts.Subagents,
		completer:         a.complete,
		budget:            a.budget,
		taskMgr:           a.taskMgr,
		// A child of a confined worker is confined to the same files: spawn_agent must not be
		// a way around the allowlist a brief set.
		confine: append([]permissions.Rule(nil), a.opts.confine...),
		depth:             depth,
		Local: LocalOptions{
			CWD:             a.cwd,
			AllowShell:      a.opts.Local.AllowShell,
			LoadRules:       a.opts.Local.LoadRules,
			LoadCursorRules: a.opts.Local.LoadCursorRules,
			LoadHooks:       a.opts.Local.LoadHooks,
			LoadPermissions: a.opts.Local.LoadPermissions,
			Store:           a.opts.Local.Store,
			SettingSources:  append([]SettingSource(nil), a.opts.Local.SettingSources...),
		},
	}
	if in.Model != "" {
		opts.Model = Model{ID: in.Model}
	}
	if in.MaxSteps > 0 {
		if a.opts.MaxSteps > 0 && in.MaxSteps > a.opts.MaxSteps {
			opts.MaxSteps = a.opts.MaxSteps
		} else {
			opts.MaxSteps = in.MaxSteps
		}
	}
	if sel["web"] || sel["web_search"] || sel["fetch_url"] {
		opts.WebSearch = a.opts.WebSearch
	}
	if sel["lsp"] {
		opts.LSP = a.opts.LSP
	}
	if sel["vision"] || sel["read_image"] {
		opts.Vision = a.opts.Vision
	}
	if sel["verify"] || sel["run_tests"] || sel["build"] || sel["test"] {
		opts.Test = a.opts.Test
		opts.Build = a.opts.Build
	}
	if hasMCPSelector(selectors) {
		opts.MCPServers = a.opts.MCPServers
		// The child borrows the parent's connections; it must not open a
		// second set of processes or close the ones it borrowed (0.44+).
		opts.MCPRuntime = a.opts.MCPRuntime
	} else {
		// SettingSources are inherited (see the table in chapter 41), and they are
		// also what loads .cursor/mcp.json. The merge runs inside Create, so the
		// child names the exclusion here: without the mcp selector it must start no
		// MCP process, including the file-configured ones.
		opts.skipFileMCP = true
	}
	return opts
}

// createSubagent builds a child through the same Create path as any agent,
// carrying its identity into the store record. It exists as a named step so the
// spawn handler reads as: decide, create, send, read the answer.
func createSubagent(ctx context.Context, opts Options, ident subagentIdentity) (*Agent, error) {
	return createAgent(ctx, opts, ident)
}

// hasMCPSelector reports whether the caller asked for the mcp group. The child
// inherits MCP servers only then; starting them for a child that cannot call
// them would be processes with no purpose.
func hasMCPSelector(selectors []string) bool {
	for _, s := range selectors {
		if strings.EqualFold(strings.TrimSpace(s), "mcp") {
			return true
		}
	}
	return false
}

// currentRunID reports the run this agent is executing, or "" when idle.
func (a *Agent) currentRunID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeRun == nil {
		return ""
	}
	return a.activeRun.id
}

// firstNonEmpty is a local convenience; the host has its own.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
