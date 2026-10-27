// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/hooks"
	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/index"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/lsp"
	"github.com/anvil-dev/anvil/internal/mcp"
	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/plugin"
	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/rules"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/skills"
	"github.com/anvil-dev/anvil/internal/store"
	"github.com/anvil-dev/anvil/internal/tasks"
	"github.com/anvil-dev/anvil/internal/tools"
	"github.com/anvil-dev/anvil/internal/verify"
	"github.com/anvil-dev/anvil/internal/vision"
	"github.com/anvil-dev/anvil/internal/websearch"
	plugmemory "github.com/anvil-dev/anvil/plugins/memory"
	"github.com/anvil-dev/anvil/plugins/sample"
)

// Agent is a durable local agent handle bound to a working directory.
type Agent struct {
	id     string
	opts   Options
	cwd    string
	closed bool

	// depth is 0 for the session's own agent and grows with each spawn; the
	// budget is shared by every agent in one root run's tree (0.42+).
	depth  int
	budget *subagentBudget
	// taskMgr is the tree's background table. A child shares its parent's, so
	// one tree has one table; closing the root cancels what is still running.
	taskMgr *tasks.Manager

	store store.Backend
	// lock is the exclusive claim on this agent id. Two live handles on one id
	// interleave their runs into a single transcript, which is what strands a
	// tool_call without its result. Nil when the backend does not lock (a
	// caller-supplied store) or when the handle never took one (Create).
	lock *store.SessionLock

	mu sync.Mutex
	// running is a turn in flight; compacting is a manual compaction, which is
	// also a model call and therefore must not overlap a turn. They are separate
	// flags because a refused Send and a refused Compact say different things.
	running    bool
	compacting bool
	activeRun  *Run
	// persistErr is the first failed write to the session file, if any.
	persistErr error
	history    []llm.Message
	complete   llm.Completer
	registry   *tools.Registry
	mcp        []*mcp.Session
	// mcpTools is the set of registry names the last refresh registered, and
	// mcpVersion the runtime version that produced it (0.44+).
	mcpTools     []string
	mcpVersion   int64
	plugins      *plugin.Manager
	lsp          *lsp.Supervisor
	systemPrompt string
	// rootAgentID and rootRunID are a child's tree root, set when its run starts (0.50+).
	rootAgentID string
	rootRunID   string
	// promptSpec is the layers systemPrompt was composed from; a run composes again from
	// it to add its matched skills and directive before the closing line.
	promptSpec prompt.Spec
	hookFile     *hooks.File
}

// Create validates options and returns a new local agent (no LLM call).
func Create(ctx context.Context, opts Options) (*Agent, error) {
	return createAgent(ctx, opts, subagentIdentity{})
}

// createAgent is Create with the identity a subagent has. A top-level caller
// passes the zero value and writes exactly the meta record it wrote before.
func createAgent(ctx context.Context, opts Options, ident subagentIdentity) (*Agent, error) {
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	// A runtime already resolved the file layers; reading them again would
	// start a second set of processes behind its back.
	if opts.MCPRuntime == nil {
		opts, err = mergeMCPFromSources(cwd, opts)
		if err != nil {
			return nil, err
		}
	}
	if opts.skipFileMCP {
		opts.MCPServers = nil
	}
	// A child reuses the parent's client; only a top-level agent builds one.
	completer := opts.completer
	if completer == nil {
		completer, err = newCompleter(opts)
		if err != nil {
			return nil, err
		}
	}
	// The id is chosen before the registry is built so the registry can name the
	// agent in every permission request it forwards (0.42+).
	agentID := "agent-" + id.New()
	opts.agentID = agentID
	opts.depth = ident.Depth
	if opts.budget == nil {
		opts.budget = newSubagentBudget(opts.Subagents.MaxAgents)
	}
	reg, sessions, plugMgr, hookFile, lspSup, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, err
	}
	// Keep the table on the options too: childOptions hands the same pointer to
	// every child, which is what makes the tree's table one table.
	opts.taskMgr = reg.TaskManager()
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("open store", false, err)
	}
	// An agent's history is trimmed when it opens, so the bound is honoured even
	// by embedders that never list checkpoints.
	pruneCheckpoints(cwd, agentID, opts.Checkpoints)
	allowShell := opts.Local.AllowShellEnabled()
	meta := store.Record{
		Type:       store.TypeMeta,
		AgentID:    agentID,
		CWD:        cwd,
		Provider:   string(opts.Provider),
		Model:      opts.Model.ID,
		MaxSteps:   opts.MaxSteps,
		AllowShell: &allowShell,

		ParentAgentID:    ident.ParentAgentID,
		ParentRunID:      ident.ParentRunID,
		ParentToolCallID: ident.ParentToolCallID,
		Depth:            ident.Depth,
		TaskTitle:        ident.TaskTitle,
	}
	if ident.Brief != nil {
		if raw, err := json.Marshal(ident.Brief); err == nil {
			meta.Brief = raw
		}
	}
	if ident.ParentAgentID != "" {
		meta.Kind = "subagent"
	}
	if err := st.Append(agentID, meta); err != nil {
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("persist agent meta", false, err)
	}
	spec, err := buildPromptSpec(cwd, opts)
	if err != nil {
		// The meta record is already on disk. A session that failed to start must
		// not stay behind as a meta-only entry that lists as a session with no runs
		// (audit #23), so the file is removed with the failed create.
		_ = st.Delete(agentID)
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("load project rules", false, err)
	}
	mcpTools, mcpVersion := runtimeMCPState(opts)
	return &Agent{
		id:           agentID,
		opts:         opts,
		cwd:          cwd,
		depth:        opts.depth,
		budget:       opts.budget,
		taskMgr:      opts.taskMgr,
		store:        st,
		complete:     completer,
		registry:     reg,
		mcp:          sessions,
		mcpTools:     mcpTools,
		mcpVersion:   mcpVersion,
		plugins:      plugMgr,
		lsp:          lspSup,
		history:      nil,
		systemPrompt: prompt.Compose(spec),
			promptSpec:   spec,
		hookFile:     hookFile,
	}, nil
}

// Prompt creates an agent, sends one message, waits, and closes.
func Prompt(ctx context.Context, message string, opts Options) (*RunResult, error) {
	agent, err := Create(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer agent.Close()

	var sendOpts []SendOption
	if len(opts.Attachments) > 0 {
		sendOpts = append(sendOpts, WithAttachments(opts.Attachments...))
	}
	run, err := agent.Send(ctx, message, sendOpts...)
	if err != nil {
		return nil, err
	}
	return run.Wait(ctx)
}

// Resume loads a persisted agent by ID from the local store (jsonl or sqlite).
func Resume(ctx context.Context, agentID string, opts Options) (*Agent, error) {
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	if opts.MCPRuntime == nil {
		opts, err = mergeMCPFromSources(cwd, opts)
		if err != nil {
			return nil, err
		}
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	// Resume does not know a depth until it has read the file, and the registry is
	// built first; a resumed session is a top-level conversation (a child is
	// resumed through its parent's transcript or not at all).
	opts.agentID = agentID
	if opts.budget == nil {
		opts.budget = newSubagentBudget(opts.Subagents.MaxAgents)
	}
	reg, sessions, plugMgr, hookFile, lspSup, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, err
	}
	opts.taskMgr = reg.TaskManager()
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("open store", false, err)
	}
	records, err := st.Load(agentID)
	if err != nil {
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		if errors.Is(err, store.ErrNotFound) {
			return nil, newError("agent not found: "+agentID, false, nil)
		}
		return nil, newError("load agent store", false, err)
	}
	// One writer per transcript. Resuming an id that a live handle already owns
	// used to succeed, and the two then wrote their runs into one file — the
	// editor makes that easy to reach, because closing a chat tab does not stop
	// the turn it was showing. The claim is taken before the history is loaded
	// and released on Close, so the second caller is refused instead of
	// corrupting the first one's conversation.
	lock, err := st.TryLock(agentID)
	if err != nil {
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		if errors.Is(err, store.ErrSessionBusy) {
			return nil, newError("session is already open in another window or session; "+
				"close it there before resuming "+agentID, false, err)
		}
		return nil, newError("lock agent store", false, err)
	}
	spec, err := buildPromptSpec(cwd, opts)
	if err != nil {
		_ = lock.Release()
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("load project rules", false, err)
	}
	pruneCheckpoints(cwd, agentID, opts.Checkpoints)
	mcpTools, mcpVersion := runtimeMCPState(opts)
	return &Agent{
		id:           agentID,
		opts:         opts,
		cwd:          cwd,
		depth:        opts.depth,
		budget:       opts.budget,
		taskMgr:      opts.taskMgr,
		store:        st,
		lock:         lock,
		complete:     completer,
		registry:     reg,
		mcp:          sessions,
		mcpTools:     mcpTools,
		mcpVersion:   mcpVersion,
		plugins:      plugMgr,
		lsp:          lspSup,
		history:      historyFromRecords(records),
		systemPrompt: prompt.Compose(spec),
			promptSpec:   spec,
		hookFile:     hookFile,
	}, nil
}

// buildSystemPrompt composes the agent's system prompt. It is the one place a prompt
// is built: the safety layer is prepended here for every agent, whatever Role and
// SystemPrompt say (chapter 49).
func buildSystemPrompt(cwd string, opts Options) (string, error) {
	spec, err := buildPromptSpec(cwd, opts)
	if err != nil {
		return "", err
	}
	return prompt.Compose(spec), nil
}

// buildPromptSpec assembles the layers that last as long as the agent: role, project
// rules, session notes, plugin hints and always-on skills. A run adds its matched
// skills and directive to a copy of it.
func buildPromptSpec(cwd string, opts Options) (prompt.Spec, error) {
	loadProject := opts.Local.LoadRules
	loadCursor := opts.Local.LoadCursorRules || opts.Local.HasSettingSource(SettingSourceProject)
	rulesText := ""
	if loadProject || loadCursor {
		var err error
		rulesText, err = rules.Load(cwd, rules.Options{
			LoadProject: loadProject,
			LoadCursor:  loadCursor,
		})
		if err != nil {
			return prompt.Spec{}, err
		}
	}
	// L2 in today's order. SystemPrompt replaces the role text (L1) only; the safety
	// layer is prepended by Compose.
	var project []string
	if rulesText != "" {
		project = append(project, "# Project rules\n"+rulesText)
	}
	if notes := sessionStartNotes(cwd, opts); notes != "" {
		project = append(project, "# Session notes\n"+notes)
	}
	if pluginNotes := pluginSessionHints(opts); pluginNotes != "" {
		project = append(project, "# Plugins\n"+pluginNotes)
	}
	if always := alwaysSkillsText(cwd, opts); always != "" {
		project = append(project, "# Skills (always)\n"+always)
	}
	return prompt.Spec{
		CWD:        cwd,
		AllowShell: opts.Local.AllowShellEnabled(),
		Role:       prompt.Role(opts.Role),
		RoleText:   opts.SystemPrompt,
		Multi:      opts.Mode != ModeSingle,
		Brief:      opts.brief,
		Project:    strings.Join(project, "\n\n"),
	}, nil
}

func pluginSessionHints(opts Options) string {
	var parts []string
	for _, id := range opts.Plugins {
		if strings.TrimSpace(id) == "memory" {
			parts = append(parts, plugmemory.SessionHint)
		}
	}
	return strings.Join(parts, "\n")
}

func sessionStartNotes(cwd string, opts Options) string {
	if !opts.Local.HasSettingSource(SettingSourceProject) {
		return ""
	}
	hf, _, err := hooks.Load(cwd)
	if err != nil || hf == nil {
		return ""
	}
	return hf.SessionStartNotes()
}

func skillDiscoverOpts(opts Options) skills.DiscoverOpts {
	return skills.DiscoverOpts{
		SkillDirs:   opts.SkillDirs,
		ScanProject: opts.Local.HasSettingSource(SettingSourceProject) || len(opts.SkillDirs) > 0 || len(opts.Skills) > 0,
		ScanUser:    opts.Local.HasSettingSource(SettingSourceUser),
	}
}

func alwaysSkillsText(cwd string, opts Options) string {
	ids := append([]string{}, opts.Skills...)
	ids = append(ids, skills.LoadAlwaysIDs(cwd)...)
	if len(ids) == 0 && !skillDiscoverOpts(opts).ScanProject {
		return ""
	}
	all, err := skills.Discover(cwd, skillDiscoverOpts(opts))
	if err != nil || len(all) == 0 {
		return ""
	}
	selected := skills.SelectByID(all, ids)
	return skills.FormatAlways(selected, 0)
}

func routerSkillsText(cwd string, opts Options, userMsg string) string {
	dopts := skillDiscoverOpts(opts)
	if !dopts.ScanProject && !dopts.ScanUser && len(dopts.SkillDirs) == 0 {
		return ""
	}
	all, err := skills.Discover(cwd, dopts)
	if err != nil || len(all) == 0 {
		return ""
	}
	exclude := append([]string{}, opts.Skills...)
	exclude = append(exclude, skills.LoadAlwaysIDs(cwd)...)
	picked := skills.Router(all, exclude, userMsg, opts.SkillRouterTopK, 0)
	return skills.FormatRouter(picked, 0)
}

func cleanupRegistry(sessions []*mcp.Session, plugMgr *plugin.Manager, lspSup *lsp.Supervisor) {
	for _, s := range sessions {
		_ = s.Close()
	}
	if plugMgr != nil {
		_ = plugMgr.Close(context.Background())
	}
	if lspSup != nil {
		lspSup.Close()
	}
}

func builtinPluginFactories() map[string]plugin.Factory {
	return map[string]plugin.Factory{
		"sample": sample.New,
		"memory": plugmemory.New,
	}
}

func toPluginPolicy(p *PluginPolicy) *plugin.Policy {
	if p == nil {
		return nil
	}
	out := &plugin.Policy{
		Available: append([]string(nil), p.Available...),
		Deny:      append([]string(nil), p.Deny...),
	}
	for _, s := range p.MaxPermissions {
		out.MaxPermissions = append(out.MaxPermissions, plugin.Permission(s))
	}
	return out
}

func buildRegistry(ctx context.Context, cwd string, opts Options) (*tools.Registry, []*mcp.Session, *plugin.Manager, *hooks.File, *lsp.Supervisor, error) {
	reg := tools.NewRegistry(cwd, opts.Local.AllowShellEnabled())
	// Every call this registry executes belongs to one agent; saying which lets a
	// permission prompt point at a child instead of the session (0.42+).
	reg.SetIdentity(opts.agentID, opts.depth)
	// One table per agent tree. The root creates it; a child inherits it through
	// opts.taskMgr, so a child's shell job and a grandchild's subagent are rows
	// in the same list. The limit is written only where the table is created.
	mgr := opts.taskMgr
	if mgr == nil {
		mgr = tasks.NewManager()
		if n := opts.Subagents.MaxBackground; n > 0 {
			mgr.Limit = n
		}
	}
	if on := opts.Subagents.OnTask; on != nil {
		mgr.SetOnChange(func(info tasks.Info) { on(toTaskInfo(info)) })
	}
	reg.SetTaskManager(mgr)
	if opts.Checkpoints != nil {
		// Capture is named but not yet scoped to a run: SetRunContext in
		// executeRun is what turns a call from "no pre-image" into one.
		reg.SetCheckpoints(checkpointRoot(cwd, *opts.Checkpoints))
	}
	embedKey := opts.EmbedAPIKey
	if embedKey == "" {
		embedKey = opts.APIKey
	}
	embedURL := opts.EmbedBaseURL
	if embedURL == "" {
		embedURL = opts.BaseURL
	}
	reg.SetIndexEmbed(tools.IndexEmbed{
		BaseURL: embedURL,
		APIKey:  embedKey,
		Model:   opts.EmbedModel,
	})
	if opts.Local.EnsureIndex && !index.Exists(cwd) {
		// A requested index that cannot be built is an error, not a silent
		// absence: the caller asked for search and would otherwise get none (audit #29).
		if _, err := index.Build(ctx, cwd, index.BuildOptions{}); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("build the project index: %w", err)
		}
	}

	reg.SetSkillsConfig(tools.SkillsConfig{
		SkillDirs:   opts.SkillDirs,
		ScanProject: skillDiscoverOpts(opts).ScanProject,
		ScanUser:    skillDiscoverOpts(opts).ScanUser,
	})
	if err := configureWebTools(reg, opts); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := configureVerifyTools(reg, cwd, opts); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	configureVisionTools(reg, opts)
	lspSup, err := configureLSP(ctx, reg, cwd, opts)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// LoadHooks is a switch of its own (0.36+). The file used to be reachable only
	// through SettingSources, which neither the editor host nor the CLI ever set,
	// so every deny written in .anvil/hooks.json was inert in both.
	var hookFile *hooks.File
	if opts.Local.LoadHooks || opts.Local.HasSettingSource(SettingSourceProject) {
		hf, _, err := hooks.Load(cwd)
		if err != nil {
			if lspSup != nil {
				lspSup.Close()
			}
			return nil, nil, nil, nil, nil, newError("load hooks", false, err)
		}
		hookFile = hf
	}

	hookOpts := hooks.CheckOpts{
		AllowCommands: opts.AllowHookCommands,
		CWD:           cwd,
	}
	reg.SetPermissionGroups(opts.PermissionGroups)
	permRules, err := buildPermissionRules(cwd, opts)
	if err != nil {
		if lspSup != nil {
			lspSup.Close()
		}
		return nil, nil, nil, nil, nil, err
	}
	reg.SetPermissionRules(permRules)
	if opts.Local.LoadPermissions {
		// The files are re-read for each call when a file owns the policy: they
		// can be edited, or appended to by an approval card, while a session is
		// open. A file that stops parsing keeps the policy it last read cleanly.
		userPath, _ := permissions.UserPath()
		anvilHome, _ := permissions.AnvilHome()
		reg.SetPermissionRulesSource(permissions.NewSource(
			cwd, userPath, permissions.HomeDir(), anvilHome,
			codeRules(opts), permRules,
		))
	}
	// What is left here is the caller's own callback: the file hooks are checked in
	// preExec now, so that a rule that allows a call cannot skip them.
	if perm := opts.Permission; perm != nil {
		reg.SetPermission(func(ctx context.Context, call tools.GatedCall) (json.RawMessage, error) {
			agentID, depth := reg.Identity()
			dec, err := perm(ctx, PermissionRequest{
				ToolName: call.Name,
				Args:     call.Args,
				Preview:  toToolPreview(call.Preview),
				// Which rule asked, when one did. A client shows it, so a prompt
				// never appears without a reason the user can act on.
				Rule:      permissionRuleMatch(call.Rule),
				Protected: call.Protected,
				// Who is asking: a subagent's call is decided by the same callback
				// as its parent's, and the card must be able to say whose it is.
				AgentID: agentID,
				RunID:   reg.RunID(),
				Depth:   depth,
			})
			if err != nil {
				return nil, err
			}
			if !dec.Allow {
				msg := dec.Message
				if msg == "" {
					msg = "permission denied for tool " + call.Name
				}
				return nil, fmt.Errorf("%s", msg)
			}
			if len(dec.Args) > 0 {
				return dec.Args, nil
			}
			return call.Args, nil
		})
	}
	if hookFile != nil {
		hf := hookFile
		ho := hookOpts
		reg.SetPreExec(func(ctx context.Context, name string, args json.RawMessage) error {
			o := ho
			o.Ctx = ctx
			// The checks that decide whether a call may run at all live here, where
			// they run whether or not anyone installed a permission callback. They
			// used to sit inside the callback, which meant a caller with no callback
			// — the CLI, a library embedder — never saw them, and a rule that allowed
			// a call skipped them even when one was installed.
			switch name {
			case "shell":
				var in struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(args, &in)
				if deny, msg := hf.CheckShell(in.Command, o); deny {
					return fmt.Errorf("%s", msg)
				}
			case "write_file", "edit_file", "apply_patch":
				if deny, msg := hf.CheckMutate(name, string(args), o); deny {
					return fmt.Errorf("%s", msg)
				}
			case "read_file":
				if deny, msg := hf.CheckRead(string(args), o); deny {
					return fmt.Errorf("%s", msg)
				}
			}
			if strings.HasPrefix(name, "mcp_") {
				short := name
				if i := strings.LastIndex(name, "_"); i >= 0 && i+1 < len(name) {
					short = name[i+1:]
				}
				if deny, msg := hf.CheckMCP(name, short, string(args), o); deny {
					return fmt.Errorf("%s", msg)
				}
			}
			return nil
		})
		reg.SetPostExec(func(ctx context.Context, name string, args json.RawMessage, res tools.Result) tools.Result {
			_ = ctx
			res.Content = hf.ApplyAfter(name, string(args), res.Content)
			return res
		})
	}
	for _, spec := range opts.Tools {
		if spec.Handler == nil {
			if lspSup != nil {
				lspSup.Close()
			}
			return nil, nil, nil, nil, nil, newError("tool "+spec.Name+": handler is required", false, nil)
		}
		if err := reg.Register(&tools.FuncTool{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.Parameters,
			Handler:     spec.Handler,
		}); err != nil {
			if lspSup != nil {
				lspSup.Close()
			}
			return nil, nil, nil, nil, nil, newError(err.Error(), false, nil)
		}
	}
	// spawn_agent is a tool the runtime ships but implements as a function,
	// because it needs the agent executing it. A child at the last depth has no
	// such tool: the limit is enforced by absence, not by a runtime check.
	if opts.Subagents.Enabled && opts.depth+1 < subagentMaxDepth(opts.Subagents) {
		// run_workflow orchestrates the same children spawn_agent creates, so it
		// shares the depth rule: a workflow cannot be used to get around it.
		agentTools := []*tools.FuncTool{subagentTool(reg), workflowTool(reg)}
		if opts.Mode != ModeSingle {
			agentTools = append(agentTools, delegateTool(reg))
		}
		for _, tool := range agentTools {
			if err := reg.Register(tool); err != nil {
				if lspSup != nil {
					lspSup.Close()
				}
				return nil, nil, nil, nil, nil, newError(err.Error(), false, nil)
			}
		}
	}
	// A read-only child has no shell, but it can still read the tree's
	// background work; the task tools are how the model polls it.
	if opts.Subagents.Enabled {
		reg.EnsureTaskTools()
	}
	var sessions []*mcp.Session
	if opts.MCPRuntime != nil {
		// The runtime owns the processes. The handlers proxy by server and tool
		// name, so a reconnect replaces the session under a live tool.
		if _, err := registerMCPRuntimeTools(reg, opts.MCPRuntime); err != nil {
			cleanupRegistry(nil, nil, lspSup)
			return nil, nil, nil, nil, nil, err
		}
	} else {
		for name, cfg := range opts.MCPServers {
			sess, err := mcp.Start(ctx, mcp.ServerConfig{
				Name:      name,
				Transport: cfg.Transport,
				Command:   cfg.Command,
				Args:      cfg.Args,
				Env:       cfg.Env,
				URL:       cfg.URL,
				Headers:   cfg.Headers,
			})
			if err != nil {
				cleanupRegistry(sessions, nil, lspSup)
				return nil, nil, nil, nil, nil, newError("mcp server "+name, false, err)
			}
			sessions = append(sessions, sess)
			for _, t := range sess.Tools() {
				toolName := "mcp_" + sanitizeName(name) + "_" + sanitizeName(t.Name)
				mcpName := t.Name
				session := sess
				if err := reg.Register(&tools.FuncTool{
					Name:        toolName,
					Description: fmt.Sprintf("[mcp:%s] %s", name, t.Description),
					Parameters:  t.InputSchema,
					Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
						return session.Call(ctx, mcpName, args)
					},
				}); err != nil {
					cleanupRegistry(sessions, nil, lspSup)
					return nil, nil, nil, nil, nil, newError(err.Error(), false, nil)
				}
			}
		}
	}
	// Filter builtins/MCP before plugins so Options.Plugins tools are never stripped.
	applyBuiltinToolFilters(reg, opts)
	plugMgr, err := plugin.Load(ctx, cwd, opts.Plugins, reg, plugin.LoadOptions{
		Factories:   builtinPluginFactories(),
		SearchPaths: opts.PluginDirs,
		Policy:      toPluginPolicy(opts.PluginPolicy),
		Embed: plugin.EmbedConfig{
			BaseURL: embedURL,
			APIKey:  embedKey,
			Model:   opts.EmbedModel,
		},
	})
	if err != nil {
		cleanupRegistry(sessions, nil, lspSup)
		return nil, nil, nil, nil, nil, newError("load plugins", false, err)
	}
	return reg, sessions, plugMgr, hookFile, lspSup, nil
}

func configureLSP(ctx context.Context, reg *tools.Registry, cwd string, opts Options) (*lsp.Supervisor, error) {
	fileCfg := lsp.Config{}
	if opts.Local.HasSettingSource(SettingSourceProject) {
		var err error
		fileCfg, err = lsp.LoadProjectConfig(cwd)
		if err != nil {
			return nil, newError("load lsp.json", false, err)
		}
	}
	optsCfg := lsp.Config{
		Enabled:  opts.LSP.Enabled,
		Required: opts.LSP.Required,
	}
	if opts.LSP.InitTimeout > 0 {
		optsCfg.InitTimeoutMs = int(opts.LSP.InitTimeout / time.Millisecond)
	}
	if opts.LSP.RequestTimeout > 0 {
		optsCfg.RequestTimeoutMs = int(opts.LSP.RequestTimeout / time.Millisecond)
	}
	for _, s := range opts.LSP.Servers {
		optsCfg.Servers = append(optsCfg.Servers, lsp.ServerConfig{
			Language: s.Language,
			Pattern:  s.Pattern,
			Command:  s.Command,
			Args:     s.Args,
		})
	}
	cfg := lsp.MergeConfig(fileCfg, optsCfg)
	if len(opts.LSP.Servers) > 0 {
		cfg.Enabled = true
	}
	if !cfg.Enabled || len(cfg.Servers) == 0 {
		if cfg.Required && len(cfg.Servers) == 0 {
			return nil, newError("LSP.Required but no servers configured", false, nil)
		}
		return nil, nil
	}
	sup := lsp.NewSupervisor(cwd, cfg)
	if cfg.Required {
		if err := sup.Warm(ctx); err != nil {
			sup.Close()
			return nil, newError("LSP Required start failed", false, err)
		}
	}
	reg.SetLSP(sup)
	return sup, nil
}

func configureVerifyTools(reg *tools.Registry, cwd string, opts Options) error {
	pf, err := verify.LoadProject(cwd)
	if err != nil {
		return newError("load project.json", false, err)
	}
	testSpec := verify.MergeSpec(verify.CommandSpec{
		Command: opts.Test.Command,
		Timeout: opts.Test.Timeout,
		Dir:     opts.Test.Dir,
	}, pf.TestSpec())
	buildSpec := verify.MergeSpec(verify.CommandSpec{
		Command: opts.Build.Command,
		Timeout: opts.Build.Timeout,
		Dir:     opts.Build.Dir,
	}, pf.BuildSpec())
	reg.SetVerifyConfig(tools.VerifyConfig{Test: testSpec, Build: buildSpec})
	return nil
}

func configureVisionTools(reg *tools.Registry, opts Options) {
	reg.SetVisionConfig(tools.VisionConfig{
		Enabled:  opts.Vision.Enabled,
		MaxSide:  opts.Vision.MaxSide,
		MaxBytes: opts.Vision.MaxBytes,
	})
}

func (a *Agent) prepareAttachments(atts []ImageAttachment) ([]llm.ContentPart, error) {
	if len(atts) == 0 {
		return nil, nil
	}
	if !a.opts.Vision.Enabled {
		return nil, newError("Vision.Enabled is required for image attachments", false, nil)
	}
	vopts := vision.Options{MaxSide: a.opts.Vision.MaxSide, MaxBytes: a.opts.Vision.MaxBytes}
	parts := make([]llm.ContentPart, 0, len(atts))
	for i, att := range atts {
		var img *vision.Image
		var err error
		switch {
		case len(att.Bytes) > 0:
			img, err = vision.Process(att.Bytes, att.MIME, vopts)
		case strings.TrimSpace(att.Path) != "":
			abs, rerr := a.registry.ResolvePath(att.Path)
			if rerr != nil {
				return nil, newError(rerr.Error(), false, nil)
			}
			img, err = vision.LoadFile(abs, vopts)
		default:
			return nil, newError(fmt.Sprintf("attachment[%d]: path or bytes required", i), false, nil)
		}
		if err != nil {
			return nil, newError(fmt.Sprintf("attachment[%d]: %v", i, err), false, nil)
		}
		parts = append(parts, llm.ContentPart{
			Type:   "image",
			MIME:   img.MIME,
			Data:   img.Data,
			Detail: att.Detail,
		})
	}
	return parts, nil
}

func configureWebTools(reg *tools.Registry, opts Options) error {
	ws := opts.WebSearch
	if !ws.Enabled {
		return nil
	}
	providerName := strings.ToLower(strings.TrimSpace(ws.Provider))
	if providerName == "" {
		providerName = "http"
	}
	if providerName == "mcp" {
		// Host supplies web_search via MCPServers; no builtin registration.
		return nil
	}
	if providerName != "http" {
		return newError("unsupported WebSearch.Provider: "+ws.Provider+" (use http or mcp)", false, nil)
	}
	keyEnv := ws.APIKeyEnv
	if keyEnv == "" && ws.APIKey == "" {
		keyEnv = "ANVIL_WEB_SEARCH_KEY"
	}
	p, err := websearch.NewHTTP(websearch.HTTPConfig{
		BaseURL:    ws.BaseURL,
		APIKey:     ws.APIKey,
		APIKeyEnv:  keyEnv,
		Method:     ws.Method,
		Timeout:    ws.Timeout,
		MaxResults: ws.MaxResults,
	})
	if err != nil {
		return newError("web search config", false, err)
	}
	reg.SetWebConfig(tools.WebConfig{
		Provider:         p,
		AllowFetchURL:    ws.AllowFetchURL,
		AllowPrivateURLs: ws.AllowPrivateURLs,
	})
	return nil
}

// maxPreviewBody is the shared size cap for ToolPreview.Before and .After.
//
// The two bodies are one budget, not two: past it a client gets neither, and
// Truncated says so. The alternative — keep whichever body fits — would hand a
// diff editor a clipped post-image and let it render that as the whole file,
// which is a worse failure than showing the unified diff text the preview
// already carries. apply_patch carries no bodies at all, so it never trips this.
const maxPreviewBody = 512 << 10

// toToolPreview lifts the runtime's preview into the public type.
func toToolPreview(p tools.Preview) ToolPreview {
	out := ToolPreview{
		Kind:    p.Kind,
		Path:    p.Path,
		Paths:   p.Paths,
		Diff:    p.Diff,
		Command: p.Command,
		Summary: p.Summary,
	}
	if p.Before != "" || p.After != "" {
		if len(p.Before)+len(p.After) <= maxPreviewBody {
			out.Before, out.After = p.Before, p.After
		} else {
			out.Truncated = true
		}
	}
	return out
}

// buildPermissionRules compiles the user rule file, the workspace rule file and
// the rules given in code into one set, in that order.
//
// The order is the point: within one effect the first matching rule decides, so
// the user's rules are the ones a reader of the user file sees at the top, then
// the workspace's, then the caller's. Severity still decides across all three, so
// a workspace allow cannot weaken a user deny.
//
// The user file is read under the same switch as the workspace file: a session
// created with LoadPermissions off has read no file policy, and a machine-wide
// file is a file policy. A malformed file, in either scope, fails Create with the
// path and the place rather than being silently ignored.
func buildPermissionRules(cwd string, opts Options) (*permissions.Set, error) {
	// Validated on its own first, so an error names a rule the caller can count
	// rather than a position in the merged list.
	code, err := permissions.New(codeRules(opts))
	if err != nil {
		return nil, newError("permission rules", false, err)
	}
	var workspace, user *permissions.Set
	var extra []string
	if opts.Local.LoadPermissions {
		workspace, err = permissions.Load(cwd)
		if err != nil {
			return nil, newError("load permissions", false, err)
		}
		// A home directory that cannot be resolved is not a policy to refuse: it
		// means this machine has no user file to read.
		userPath, pathErr := permissions.UserPath()
		if pathErr == nil {
			user, extra, err = permissions.LoadUser(userPath)
			if err != nil {
				return nil, newError("load user permissions", false, err)
			}
		}
	}
	set := permissions.Merge(user, workspace, code)
	if set != nil {
		// Protected paths are a fact about where the session runs, not about which
		// file supplied a rule, so they are installed on every set that has rules.
		anvilHome, _ := permissions.AnvilHome()
		set.Protect(permissions.Protection{
			Root:      cwd,
			Home:      permissions.HomeDir(),
			AnvilHome: anvilHome,
			Extra:     extra,
		})
	}
	return set, nil
}

// codeRules are the rules a program passes in: the caller's own, then the generated
// deny a delegated child carries. Order matters for the citation only, since a deny
// beats an allow wherever it sits; the generated rules come after the caller's so the
// caller's rules are always the ones a reader meets first.
func codeRules(opts Options) []permissions.Rule {
	return append(permissionRules(opts.PermissionRules), opts.confine...)
}

func permissionRules(rules []PermissionRule) []permissions.Rule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]permissions.Rule, 0, len(rules))
	for _, r := range rules {
		out = append(out, permissions.Rule{
			Effect:  r.Effect,
			Tool:    r.Tool,
			Pattern: r.Pattern,
			Reason:  r.Reason,
			Source:  r.Source,
			Index:   r.Index,
		})
	}
	return out
}

func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "tool"
	}
	return string(out)
}

func historyFromRecords(records []store.Record) []llm.Message {
	lastSummary := -1
	for i, rec := range records {
		if rec.Type == store.TypeSummary {
			lastSummary = i
		}
	}
	var history []llm.Message
	if lastSummary >= 0 {
		history = append(history, llm.Message{
			Role:    llm.RoleUser,
			Name:    compress.SummaryMarker,
			Content: records[lastSummary].Content,
		})
	}
	start := 0
	if lastSummary >= 0 {
		start = lastSummary + 1
	}
	for _, rec := range records[start:] {
		if rec.Type != store.TypeMessage {
			continue
		}
		msg := llm.Message{
			Role:       llm.Role(rec.Role),
			Content:    rec.Content,
			ToolCallID: rec.ToolCallID,
			Name:       rec.Name,
		}
		for _, tc := range rec.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
		history = append(history, msg)
	}
	return history
}

// AgentID returns the agent identifier.
func (a *Agent) AgentID() string {
	if a == nil {
		return ""
	}
	return a.id
}

// Send starts a run for the user message.
func (a *Agent) Send(ctx context.Context, message string, opts ...SendOption) (*Run, error) {
	if a == nil {
		return nil, newError("nil agent", false, nil)
	}
	if a.closed {
		return nil, newError("agent is closed", false, nil)
	}
	cfg := sendConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if a.opts.Mode == ModeComplex {
		// Refused before anything is created or sent: no stage starts for a run that
		// cannot finish.
		if why := a.complexRefusal(); why != "" {
			return nil, newError(why, false, nil)
		}
		if len(cfg.Attachments) > 0 {
			return nil, newError("mode complex does not take image attachments: the stages work from the message and the workspace", false, nil)
		}
	}

	if cfg.Force {
		a.mu.Lock()
		prev := a.activeRun
		a.mu.Unlock()
		if prev != nil {
			// The caller's context may already be done. It must not make the forced
			// send give up while the previous run is still active (audit #21): the
			// previous run is cancelled and waited for on a context of its own.
			_ = prev.Cancel(context.Background())
			wait, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = prev.Wait(wait)
			cancel()
		}
	}

	parts, err := a.prepareAttachments(cfg.Attachments)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, newError("another run is already in progress", false, nil)
	}
	if a.compacting {
		a.mu.Unlock()
		return nil, newError("a compaction is in progress", false, nil)
	}
	// A runtime may have reconnected, added or removed a server since the last
	// turn. The swap happens here — idle, under a.mu, before running is set — so
	// no turn ever sees the registry change under it.
	if a.opts.MCPRuntime != nil && a.opts.MCPRuntime.Version() != a.mcpVersion {
		if err := a.refreshMCPLocked(); err != nil {
			a.mu.Unlock()
			return nil, err
		}
	}
	a.running = true
	historyCopy := append([]llm.Message(nil), a.history...)
	wall := a.opts.MaxWallTime
	runCtx, cancelCause := context.WithCancelCause(ctx)
	run := newRun("run-"+id.New(), cancelCause)
	a.activeRun = run
	a.mu.Unlock()

	if a.opts.Mode == ModeComplex {
		go a.executeComplex(runCtx, cancelCause, wall, run, message)
		return run, nil
	}
	go a.executeRun(runCtx, cancelCause, wall, run, message, cfg.Directive, historyCopy, parts)
	return run, nil
}

// Reload re-reads file-based MCP, rules, and hooks without disposing the agent (0.10+).
// Inline MCPServers / Tools / SystemPrompt from the original Options are kept;
// file layers are refreshed. Fails if a run is in progress.
func (a *Agent) Reload(ctx context.Context) error {
	if a == nil {
		return newError("nil agent", false, nil)
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return newError("cannot reload while a run is in progress", false, nil)
	}
	opts := a.opts
	cwd := a.cwd
	oldSessions := a.mcp
	oldPlugins := a.plugins
	oldLSP := a.lsp
	a.mu.Unlock()

	if opts.MCPRuntime == nil {
		merged, err := mergeMCPFromSources(cwd, opts)
		if err != nil {
			return err
		}
		opts = merged
	}
	reg, sessions, plugMgr, hookFile, lspSup, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return err
	}
	spec, err := buildPromptSpec(cwd, opts)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return newError("load project rules", false, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return newError("cannot reload while a run is in progress", false, nil)
	}
	for _, s := range oldSessions {
		_ = s.Close()
	}
	if oldPlugins != nil {
		_ = oldPlugins.Close(context.Background())
	}
	if oldLSP != nil {
		oldLSP.Close()
	}
	a.opts = opts
	a.registry = reg
	a.mcp = sessions
	if opts.MCPRuntime != nil {
		a.mcpTools, a.mcpVersion = runtimeMCPState(opts)
	} else {
		a.mcpTools, a.mcpVersion = nil, 0
	}
	a.plugins = plugMgr
	a.lsp = lspSup
	a.systemPrompt = prompt.Compose(spec)
	a.promptSpec = spec
	a.hookFile = hookFile
	return nil
}

// RefreshMCP re-registers the runtime's tools without disposing the agent
// (0.44+). It refuses while a run or a compaction is in progress: a running
// turn keeps the registry it started with. The next Send picks the change up
// on its own, so a refusal is a deferral, not a loss.
func (a *Agent) RefreshMCP() error {
	if a == nil {
		return newError("nil agent", false, nil)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return newError("agent is closed", false, nil)
	}
	if a.opts.MCPRuntime == nil {
		return newError("agent has no MCP runtime", false, nil)
	}
	if a.running || a.compacting {
		return newError("cannot refresh MCP tools while a run is in progress", false, nil)
	}
	return a.refreshMCPLocked()
}

// refreshMCPLocked swaps only the mcp_* names. Built-in, plugin and caller
// tools are untouched; the allow/deny lists are re-applied so a filtered tool
// does not come back through a refresh.
func (a *Agent) refreshMCPLocked() error {
	rt := a.opts.MCPRuntime
	if rt == nil || a.registry == nil {
		return nil
	}
	for _, name := range a.mcpTools {
		a.registry.Unregister(name)
	}
	names, err := registerMCPRuntimeTools(a.registry, rt)
	if err != nil {
		a.mcpTools = nil
		return err
	}
	applyBuiltinToolFilters(a.registry, a.opts)
	a.mcpTools = names
	a.mcpVersion = rt.Version()
	return nil
}

// promptCacheEnabled reports whether the adapter should place cache
// breakpoints. normalizeOptions has resolved nil already; this keeps the
// runtime call site free of a pointer.
func promptCacheEnabled(opts Options) bool {
	return opts.Context.PromptCache != nil && *opts.Context.PromptCache
}

// usageInfo projects the runtime's usage onto the public type. It is nil in,
// nil out: a provider that reported no counts must not become a zero row that
// a client draws as a run that cost nothing.
func usageInfo(u *llm.Usage) *Usage {
	if u == nil {
		return nil
	}
	return &Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.CachedTokens,
		CacheWriteTokens: u.CacheWriteTokens,
	}
}

// todoItemsInfo projects the model's task list onto the public type. A nil list
// stays nil — no call wrote one — while an empty non-nil list stays empty, which
// is how a call clears the panel.
func todoItemsInfo(items []tools.TodoItem) []TodoItem {
	if items == nil {
		return nil
	}
	out := make([]TodoItem, len(items))
	for i, it := range items {
		out[i] = TodoItem{Text: it.Text, Status: it.Status}
	}
	return out
}

// compactInfo projects a runtime compaction onto the public event.
func compactInfo(in *runtime.CompactInfo) *CompactInfo {
	if in == nil {
		return nil
	}
	return &CompactInfo{
		Reason:     in.Reason,
		Before:     in.Before,
		After:      in.After,
		Summarized: in.Summarized,
		Window:     in.Window,
	}
}

func (a *Agent) executeRun(ctx context.Context, cancelCause context.CancelCauseFunc, wall time.Duration, run *Run, message, directive string, history []llm.Message, userParts []llm.ContentPart) {
	defer func() {
		// The run is over, so its pre-images stop being written: a tool call made
		// outside a run — a resumed tab replaying, a direct Execute — belongs to no
		// checkpoint and must not be filed under this run.
		a.registry.SetRunContext("", "")
		cancelCause(nil)
		a.clearRunState(run)
	}()

	loopCtx := ctx
	if wall > 0 {
		var wallCancel context.CancelFunc
		loopCtx, wallCancel = context.WithTimeoutCause(ctx, wall, runtime.ErrMaxWallTime)
		defer wallCancel()
	}
	// The tools that act for the agent find it here; the registry is shared by
	// the agent's runs, and a back-reference would be a cycle for no gain.
	loopCtx = withAgent(loopCtx, a)
	if a.depth == 0 {
		// One root run is one tree: the agent budget begins again with it.
		a.budget.reset()
	}

	a.persist(store.Record{Type: store.TypeRunStart, RunID: run.id})

	sysPrompt := a.systemPrompt
	routed := routerSkillsText(a.cwd, a.opts, message)
	if routed != "" || directive != "" {
		// Matched skills and the directive belong before the closing line, so the run's
		// prompt is composed again from the agent's layers rather than appended to.
		spec := a.promptSpec
		if routed != "" {
			if spec.Project != "" {
				spec.Project += "\n\n"
			}
			spec.Project += "# Skills (matched)\n" + routed
		}
		spec.Directive = directive
		sysPrompt = prompt.Compose(spec)
	}

	a.registry.SetRunContext(a.id, run.id)
	a.adoptTreeRoot(loopCtx)
	result := runtime.Loop(loopCtx, a.complete, a.registry, runtime.Config{
		CWD:               a.cwd,
		Model:             a.opts.Model.ID,
		MaxSteps:          a.opts.MaxSteps,
		History:           history,
		UserMsg:           message,
		UserParts:         userParts,
		RunID:             run.id,
		HistoryMaxChars:   a.opts.HistoryMaxChars,
		HistoryKeepRecent: a.opts.HistoryKeepRecent,
		Context: runtime.ContextBudget{
			WindowTokens:  a.opts.Context.WindowTokens,
			AutoCompactAt: a.opts.Context.AutoCompactAt,
			KeepRecent:    a.opts.Context.KeepRecent,
		},
		PromptCache:      promptCacheEnabled(a.opts),
		Effort:           llm.Effort(a.opts.Effort),
		MaxOutputTokens:  a.opts.MaxOutputTokens,
		SystemPrompt:     sysPrompt,
		ParallelTools:    a.opts.ParallelTools,
		ParallelToolsMax: a.opts.ParallelToolsMax,
		Sink:             func(m llm.Message) { a.persistMessage(run.id, m) },
	}, func(ev runtime.Event) {
		out := Event{
			Type:          EventType(ev.Type),
			Text:          ev.Text,
			ToolCallID:    ev.ToolCallID,
			Name:          ev.Name,
			Args:          json.RawMessage(ev.Args),
			Content:       ev.Content,
			IsError:       ev.IsError,
			Step:          ev.Step,
			MaxSteps:      ev.MaxSteps,
			Code:          Code(ev.Code),
			Attempt:       ev.Attempt,
			Delay:         ev.Delay,
			Rule:          permissionRuleMatch(ev.Rule),
			Protected:     ev.Protected,
			Usage:         usageInfo(ev.Usage),
			Items:         todoItemsInfo(ev.Items),
			Compact:       compactInfo(ev.Compact),
			LastRequestAt: ev.LastRequestAt,
		}
		run.emit(out)
		// A child's events travel to whoever asked for the child, tagged with the
		// call that started it. They do not travel through the parent's stream:
		// the parent's conversation never sees them, and neither should its card.
		if origin, ok := subagentOriginFromContext(loopCtx); ok && out.Type != "done" {
			// The child's done is not a stream event: the parent learns the outcome from
			// the spawn tool's result, so done is not forwarded (audit #26, design 42).
			if on := a.opts.Subagents.OnEvent; on != nil {
				on(SubagentEvent{
					ParentAgentID: origin.parentAgentID,
					ParentRunID:   origin.parentRunID,
					ToolCallID:    origin.toolCallID,
					AgentID:       a.id,
					RunID:         run.id,
					Depth:         a.depth,
					Event:         out,
				})
			}
		}
	})

	a.mu.Lock()
	a.history = nonSystemMessages(result.Messages)
	a.mu.Unlock()

	rr := &RunResult{
		ID:            result.ID,
		Status:        RunStatus(result.Status),
		Result:        result.Result,
		Steps:         result.Steps,
		Usage:         usageInfo(result.Usage),
		Err:           result.Err,
		Code:          Code(result.Code),
		Truncated:     result.Truncated,
		LastRequestAt: result.LastRequestAt,
	}
	if rr.ID == "" {
		rr.ID = run.id
	} else {
		run.id = rr.ID
	}

	var usage *store.Usage
	if result.Usage != nil {
		usage = &store.Usage{
			InputTokens:      result.Usage.InputTokens,
			OutputTokens:     result.Usage.OutputTokens,
			TotalTokens:      result.Usage.TotalTokens,
			CachedTokens:     result.Usage.CachedTokens,
			CacheWriteTokens: result.Usage.CacheWriteTokens,
		}
	}
	a.persist(store.Record{
		Type:   store.TypeRunEnd,
		RunID:  rr.ID,
		Status: string(rr.Status),
		Steps:  rr.Steps,
		Usage:  usage,
	})

	var err error
	if rr.Status == StatusError {
		err = newError(rr.Err, false, nil)
	}
	// A run whose records did not reach the session file must not report success:
	// the caller would believe a transcript that is not stored (audit #19).
	a.mu.Lock()
	persistErr := a.persistErr
	a.mu.Unlock()
	if err == nil && persistErr != nil {
		err = newError("session file write failed; the run is not fully recorded", false, persistErr)
	}
	// Release the slot before publishing the result. Wait wakes on finish, so a
	// caller that waits and immediately sends again would otherwise race this
	// goroutine's deferred cleanup and be told a run is already in progress.
	// The defer clears it again, which is harmless: activeRun is already nil.
	a.clearRunState(run)
	run.finish(rr, err)
}

// clearRunState releases an agent's run slot. The deferred call is the normal
// path; executeRun also calls it before finishing a Run so the state is true
// by the time a waiter observes the result.
func (a *Agent) clearRunState(run *Run) {
	a.mu.Lock()
	a.running = false
	if a.activeRun == run {
		a.activeRun = nil
	}
	a.mu.Unlock()
}

func (a *Agent) persistMessage(runID string, m llm.Message) {
	if m.Name == compress.SummaryMarker {
		a.persist(store.Record{
			Type:    store.TypeSummary,
			RunID:   runID,
			Role:    string(m.Role),
			Content: m.Content,
			Name:    m.Name,
		})
		return
	}
	rec := store.Record{
		Type:       store.TypeMessage,
		RunID:      runID,
		Role:       string(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	for _, tc := range m.ToolCalls {
		rec.ToolCalls = append(rec.ToolCalls, store.ToolCall{
			ID:   tc.ID,
			Name: tc.Name,
			Args: tc.Args,
		})
	}
	a.persist(rec)
}

// persist writes one record to the session. The store is read under the lock,
// because Close clears it under the same lock. A failed write is recorded on
// the agent rather than dropped, so a run whose transcript did not reach the
// file is not reported as complete (audit #19).
func (a *Agent) persist(rec store.Record) {
	a.mu.Lock()
	st := a.store
	id := a.id
	a.mu.Unlock()
	if st == nil {
		return
	}
	if err := st.Append(id, rec); err != nil {
		a.recordPersistError(err)
	}
}

// recordPersistError keeps the first write failure, so Wait can report it.
func (a *Agent) recordPersistError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.persistErr == nil {
		a.persistErr = err
	}
}

// Close releases agent resources including MCP servers and the store backend.
// Concurrent Close calls are safe: only the first performs cleanup and sessionEnd.
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	hf := a.hookFile
	allowCmds := a.opts.AllowHookCommands
	cwd := a.cwd
	st := a.store
	a.store = nil
	lk := a.lock
	a.lock = nil
	sessions := a.mcp
	a.mcp = nil
	plugMgr := a.plugins
	a.plugins = nil
	lspSup := a.lsp
	a.lsp = nil
	// The table stays readable after Close: it is the record of what ran, and Close
	// marks the running rows stopped below. Only the reference to it is kept (audit #24).
	taskMgr := a.taskMgr
	// A run still in flight must not write after the store and the claim are
	// released (audit #18): it is cancelled here and waited for below.
	active := a.activeRun
	a.mu.Unlock()

	if active != nil {
		_ = active.Cancel(context.Background())
		// A run that ignores cancellation is not worth holding Close open for ever.
		wait, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = active.Wait(wait)
		cancel()
	}

	// Background work ends first: a closed conversation must not leave a process
	// or a child agent writing into the workspace. The grace is short because a
	// task that ignores it is not worth holding the close open for; the rows
	// stay in the table as cancelled. Only the root owns the tree's table: a
	// child closing when its turn ends must not cancel its siblings' work.
	if taskMgr != nil && a.depth == 0 {
		taskMgr.Close(2 * time.Second)
	}
	if st != nil {
		_ = st.Close()
	}
	// Released after the store so no record can be written between the two:
	// the claim covers the whole life of the handle, not just its reads.
	if lk != nil {
		_ = lk.Release()
	}
	for _, s := range sessions {
		_ = s.Close()
	}
	if plugMgr != nil {
		_ = plugMgr.Close(context.Background())
	}
	if lspSup != nil {
		lspSup.Close()
	}
	if hf != nil {
		hf.FireSessionEnd(hooks.CheckOpts{
			AllowCommands: allowCmds,
			CWD:           cwd,
			Ctx:           context.Background(),
		})
	}
	return nil
}

func nonSystemMessages(messages []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

func normalizeOptions(opts Options) (Options, string, error) {
	if opts.Provider == "" {
		opts.Provider = ProviderOpenAI
	}
	switch opts.Provider {
	case ProviderOpenAI, ProviderAnthropic:
	default:
		return opts, "", newError("unsupported provider: "+string(opts.Provider), false, nil)
	}
	if !llm.Effort(opts.Effort).Valid() {
		return opts, "", newError("unsupported effort: "+string(opts.Effort)+" (use low, medium or high, or leave it empty)", false, nil)
	}
	switch opts.Mode {
	case ModeSingle:
	case ModeMulti:
		if !opts.Subagents.Enabled {
			return opts, "", newError("mode multi needs Subagents.Enabled: delegate creates a child agent", false, nil)
		}
	case ModeComplex:
		if !opts.Subagents.Enabled {
			return opts, "", newError("mode complex needs Subagents.Enabled: its stages are child agents", false, nil)
		}
		c := opts.Complex
		if !llm.Effort(c.Decision.Effort).Valid() || !llm.Effort(c.Analyze.Effort).Valid() || !llm.Effort(c.Worker.Effort).Valid() {
			return opts, "", newError("complex stage effort must be low, medium or high, or empty", false, nil)
		}
	default:
		return opts, "", newError("unsupported mode: "+string(opts.Mode)+" (use multi or complex, or leave it empty)", false, nil)
	}
	if !prompt.Role(opts.Role).Valid() {
		return opts, "", newError("unsupported role: "+string(opts.Role), false, nil)
	}
	if opts.APIKey == "" {
		opts.APIKey = os.Getenv("ANVIL_API_KEY")
	}
	if opts.Model.ID == "" {
		return opts, "", newError("model id is required", false, nil)
	}
	cwd := opts.Local.CWD
	if cwd == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return opts, "", newError("resolve cwd", false, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return opts, "", newError("cwd not accessible", false, err)
	}
	if !info.IsDir() {
		return opts, "", newError("cwd is not a directory", false, nil)
	}
	opts.Local.CWD = abs
	// MaxSteps 0 = unlimited; loop guard stops runaway tool repetition.
	if opts.HistoryKeepRecent < 0 {
		opts.HistoryKeepRecent = 0
	}
	// A declared budget normalizes here so every reader — the runtime, the
	// transcript, a client's meter — agrees on the same threshold.
	if opts.Context.WindowTokens < 0 {
		opts.Context.WindowTokens = 0
	}
	if opts.Context.AutoCompactAt <= 0 || opts.Context.AutoCompactAt > 1 {
		opts.Context.AutoCompactAt = 0.8
	}
	if opts.Context.KeepRecent < 2 {
		opts.Context.KeepRecent = 8
	}
	// Prompt caching is on by default for Anthropic: it costs no extra input
	// (a cache read is cheaper, a write is the same tokens) and it is what the
	// client's countdown reads. An OpenAI-compatible endpoint caches on its own
	// and ignores the flag, so turning it on there changes nothing.
	if opts.Context.PromptCache == nil {
		on := opts.Provider == ProviderAnthropic
		opts.Context.PromptCache = &on
	}
	return opts, abs, nil
}

func newCompleter(opts Options) (llm.Completer, error) {
	cfg := llm.Config{
		APIKey:  opts.APIKey,
		BaseURL: opts.BaseURL,
		Model:   opts.Model.ID,
	}
	var inner llm.Completer
	switch opts.Provider {
	case ProviderAnthropic:
		inner = llm.NewAnthropic(cfg)
	default:
		inner = llm.NewOpenAI(cfg)
	}
	// Wrapped once, here: a child agent reuses its parent's completer (Options.completer),
	// so a retry under a subagent is not multiplied.
	return llm.WithRetry(inner, llm.RetryPolicy{
		MaxAttempts: opts.Retry.MaxAttempts,
		BaseDelay:   opts.Retry.BaseDelay,
		MaxDelay:    opts.Retry.MaxDelay,
	}), nil
}
