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

	"github.com/anvil-dev/anvil/internal/tools"
)

// subagentBrief is the system prompt every child agent gets instead of the main
// agent's. It is deliberately short: a child's job is the task it was handed,
// and its answer is read by the agent that called it, not by a person.
const subagentBrief = `You are a subagent. The agent that created you gave you one task and is waiting for \
your answer. Work with the tools you have. Do not ask a question and stop; if something is missing, \
say what you could not determine. Your final message is the entire result: write it for the agent \
that called you, not for a person, and do not repeat the task.`

// subagentIdentity is what Create stamps onto a child's meta record. A top-level
// session passes the zero value and writes exactly the record it wrote before.
type subagentIdentity struct {
	ParentAgentID    string
	ParentRunID      string
	ParentToolCallID string
	TaskTitle        string
	Depth            int
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
}

type subagentOriginKey struct{}

// withSubagentOrigin tags the context a child's run derives from with where it
// came from. The child's executeRun reads it to build SubagentEvent envelopes.
func withSubagentOrigin(ctx context.Context, parentAgentID, parentRunID, toolCallID string) context.Context {
	return context.WithValue(ctx, subagentOriginKey{}, subagentOrigin{
		parentAgentID: parentAgentID,
		parentRunID:   parentRunID,
		toolCallID:    toolCallID,
	})
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
	Tools       []string `json:"tools"`
	Model       string   `json:"model"`
	MaxSteps    int      `json:"max_steps"`
	Background  bool     `json:"background"`
}

// subagentTool builds the spawn_agent FuncTool. It is a closure over the
// parent's registry so the selector can be checked against the tools this
// session actually has, not against a list compiled in here.
func subagentTool(reg *tools.Registry) *tools.FuncTool {
	return &tools.FuncTool{
		Name:    "spawn_agent",
		Builtin: true,
		Description: "Hand one task to a subagent and read its final answer. " +
			"The subagent runs in this workspace with the tools you list (an omitted list is read-only), " +
			"and its conversation does not enter yours: you learn only what it reports. " +
			"Use it for work that would otherwise fill your context with searching, or for a second opinion.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"prompt":{"type":"string","description":"The whole task. The subagent sees nothing else from this conversation."},
				"description":{"type":"string","description":"A short name for the task, shown on its card. Not sent to the subagent."},
				"tools":{"type":"array","items":{"type":"string"},"description":"Group or tool names the subagent may use: read, search, write, edit, shell, verify, web, lsp, git, vision, mcp. Default: read, search, skills, git, vision."},
				"model":{"type":"string","description":"Model id. Empty uses this session's model."},
				"max_steps":{"type":"integer","minimum":0,"description":"Optional step ceiling for the subagent; it can never exceed this session's own."},
				"background":{"type":"boolean","description":"Return a task id immediately instead of waiting."}
			},
			"required":["prompt","description"]
		}`),
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
	if in.Background {
		// Implemented by p_0.42.2; refusing with a sentence is better than a card
		// that says it started something it did not.
		return "", errors.New("background subagents are not available in this build")
	}
	selectors, err := resolveSubagentSelectors(reg, in.Tools)
	if err != nil {
		return "", err
	}
	if err := parent.budget.take(); err != nil {
		return "", err
	}

	depth := parent.depth + 1
	opts := parent.childOptions(in, selectors, depth)
	ident := subagentIdentity{
		ParentAgentID:    parent.id,
		ParentRunID:      parent.currentRunID(),
		ParentToolCallID: tools.CallID(ctx),
		TaskTitle:        strings.TrimSpace(in.Description),
		Depth:            depth,
	}
	callCtx := withSubagentOrigin(ctx, parent.id, ident.ParentRunID, ident.ParentToolCallID)
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

// resolveSubagentSelectors validates the caller's tool list against the session
// and returns the selectors to hand the child. Empty means the read-only
// default. A selector that names no group and no tool this session has is an
// error rather than a silently narrower child.
func resolveSubagentSelectors(reg *tools.Registry, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string(nil), defaultSubagentTools...), nil
	}
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
	out := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, raw := range requested {
		sel := strings.ToLower(strings.TrimSpace(raw))
		if sel == "" {
			continue
		}
		if !groups[sel] {
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
		return append([]string(nil), defaultSubagentTools...), nil
	}
	return out, nil
}

// childOptions builds the child's Options from a whitelist of the parent's. See
// doc/design/v1.0/41-subagents-and-tasks.md — the exclusions are deliberate.
func (a *Agent) childOptions(in spawnParams, selectors []string, depth int) Options {
	sel := tools.ExpandToolSelectors(selectors)
	opts := Options{
		Provider:         a.opts.Provider,
		APIKey:           a.opts.APIKey,
		BaseURL:          a.opts.BaseURL,
		Model:            a.opts.Model,
		MaxSteps:         a.opts.MaxSteps,
		MaxWallTime:      a.opts.MaxWallTime,
		Permission:       a.opts.Permission,
		PermissionGroups: append([]string(nil), a.opts.PermissionGroups...),
		PermissionRules:  append([]PermissionRule(nil), a.opts.PermissionRules...),
		ParallelTools:    a.opts.ParallelTools,
		ParallelToolsMax: a.opts.ParallelToolsMax,
		Checkpoints:      a.opts.Checkpoints,
		SystemPrompt:     subagentBrief,
		BuiltinTools:     selectors,
		DisallowedTools:  append([]string(nil), a.opts.DisallowedTools...),
		Subagents:        a.opts.Subagents,
		completer:        a.complete,
		budget:           a.budget,
		depth:            depth,
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
