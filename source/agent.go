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
	"github.com/anvil-dev/anvil/internal/plugin"
	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/rules"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/skills"
	"github.com/anvil-dev/anvil/internal/store"
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

	store store.Backend

	mu           sync.Mutex
	running      bool
	activeRun    *Run
	history      []llm.Message
	complete     llm.Completer
	registry     *tools.Registry
	mcp          []*mcp.Session
	plugins      *plugin.Manager
	lsp          *lsp.Supervisor
	systemPrompt string
	hookFile     *hooks.File
}

// Create validates options and returns a new local agent (no LLM call).
func Create(ctx context.Context, opts Options) (*Agent, error) {
	opts, cwd, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	opts, err = mergeMCPFromSources(cwd, opts)
	if err != nil {
		return nil, err
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	reg, sessions, plugMgr, hookFile, lspSup, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("open store", false, err)
	}
	agentID := "agent-" + id.New()
	allowShell := opts.Local.AllowShellEnabled()
	if err := st.Append(agentID, store.Record{
		Type:       store.TypeMeta,
		AgentID:    agentID,
		CWD:        cwd,
		Provider:   string(opts.Provider),
		Model:      opts.Model.ID,
		MaxSteps:   opts.MaxSteps,
		AllowShell: &allowShell,
	}); err != nil {
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("persist agent meta", false, err)
	}
	sysPrompt, err := buildSystemPrompt(cwd, opts)
	if err != nil {
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("load project rules", false, err)
	}
	return &Agent{
		id:           agentID,
		opts:         opts,
		cwd:          cwd,
		store:        st,
		complete:     completer,
		registry:     reg,
		mcp:          sessions,
		plugins:      plugMgr,
		lsp:          lspSup,
		history:      nil,
		systemPrompt: sysPrompt,
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
	opts, err = mergeMCPFromSources(cwd, opts)
	if err != nil {
		return nil, err
	}
	completer, err := newCompleter(opts)
	if err != nil {
		return nil, err
	}
	reg, sessions, plugMgr, hookFile, lspSup, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, err
	}
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
	sysPrompt, err := buildSystemPrompt(cwd, opts)
	if err != nil {
		_ = st.Close()
		cleanupRegistry(sessions, plugMgr, lspSup)
		return nil, newError("load project rules", false, err)
	}
	return &Agent{
		id:           agentID,
		opts:         opts,
		cwd:          cwd,
		store:        st,
		complete:     completer,
		registry:     reg,
		mcp:          sessions,
		plugins:      plugMgr,
		lsp:          lspSup,
		history:      historyFromRecords(records),
		systemPrompt: sysPrompt,
		hookFile:     hookFile,
	}, nil
}

func buildSystemPrompt(cwd string, opts Options) (string, error) {
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
			return "", err
		}
	}
	var base string
	if opts.SystemPrompt != "" {
		if rulesText == "" {
			base = opts.SystemPrompt
		} else {
			base = opts.SystemPrompt + "\n\n# Project rules\n" + rulesText
		}
	} else {
		base = prompt.SystemWithOptions(cwd, rulesText, prompt.Options{
			AllowShell: opts.Local.AllowShellEnabled(),
		})
	}
	if notes := sessionStartNotes(cwd, opts); notes != "" {
		base += "\n\n# Session notes\n" + notes
	}
	if pluginNotes := pluginSessionHints(opts); pluginNotes != "" {
		base += "\n\n# Plugins\n" + pluginNotes
	}
	if always := alwaysSkillsText(cwd, opts); always != "" {
		base += "\n\n# Skills (always)\n" + always
	}
	return base, nil
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
		_, _ = index.Build(ctx, cwd, index.BuildOptions{})
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

	var hookFile *hooks.File
	if opts.Local.HasSettingSource(SettingSourceProject) {
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
	perm := composePermission(opts.Permission, hookFile, hookOpts)
	if perm != nil {
		reg.SetPermission(func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
			dec, err := perm(ctx, PermissionRequest{ToolName: name, Args: args})
			if err != nil {
				return nil, err
			}
			if !dec.Allow {
				msg := dec.Message
				if msg == "" {
					msg = "permission denied for tool " + name
				}
				return nil, fmt.Errorf("%s", msg)
			}
			if len(dec.Args) > 0 {
				return dec.Args, nil
			}
			return args, nil
		})
	}
	if hookFile != nil {
		hf := hookFile
		ho := hookOpts
		reg.SetPreExec(func(ctx context.Context, name string, args json.RawMessage) error {
			o := ho
			o.Ctx = ctx
			if name == "read_file" {
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
	var sessions []*mcp.Session
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

func composePermission(user PermissionFunc, hookFile *hooks.File, hookOpts hooks.CheckOpts) PermissionFunc {
	if user == nil && hookFile == nil {
		return nil
	}
	return func(ctx context.Context, req PermissionRequest) (PermissionDecision, error) {
		if hookFile != nil {
			o := hookOpts
			o.Ctx = ctx
			switch req.ToolName {
			case "shell":
				var in struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(req.Args, &in)
				if deny, msg := hookFile.CheckShell(in.Command, o); deny {
					return PermissionDecision{Allow: false, Message: msg}, nil
				}
			case "write_file", "edit_file", "apply_patch":
				if deny, msg := hookFile.CheckMutate(req.ToolName, string(req.Args), o); deny {
					return PermissionDecision{Allow: false, Message: msg}, nil
				}
			}
		}
		if user != nil {
			return user(ctx, req)
		}
		return PermissionDecision{Allow: true}, nil
	}
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

	if cfg.Force {
		a.mu.Lock()
		prev := a.activeRun
		a.mu.Unlock()
		if prev != nil {
			_ = prev.Cancel(ctx)
			_, _ = prev.Wait(ctx)
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
	a.running = true
	historyCopy := append([]llm.Message(nil), a.history...)
	wall := a.opts.MaxWallTime
	runCtx, cancelCause := context.WithCancelCause(ctx)
	run := newRun("run-"+id.New(), cancelCause)
	a.activeRun = run
	a.mu.Unlock()

	go a.executeRun(runCtx, cancelCause, wall, run, message, historyCopy, parts)
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

	opts, err := mergeMCPFromSources(cwd, opts)
	if err != nil {
		return err
	}
	reg, sessions, plugMgr, hookFile, lspSup, err := buildRegistry(ctx, cwd, opts)
	if err != nil {
		cleanupRegistry(sessions, plugMgr, lspSup)
		return err
	}
	sysPrompt, err := buildSystemPrompt(cwd, opts)
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
	a.plugins = plugMgr
	a.lsp = lspSup
	a.systemPrompt = sysPrompt
	a.hookFile = hookFile
	return nil
}

func (a *Agent) executeRun(ctx context.Context, cancelCause context.CancelCauseFunc, wall time.Duration, run *Run, message string, history []llm.Message, userParts []llm.ContentPart) {
	defer func() {
		cancelCause(nil)
		a.mu.Lock()
		a.running = false
		if a.activeRun == run {
			a.activeRun = nil
		}
		a.mu.Unlock()
	}()

	loopCtx := ctx
	if wall > 0 {
		var wallCancel context.CancelFunc
		loopCtx, wallCancel = context.WithTimeoutCause(ctx, wall, runtime.ErrMaxWallTime)
		defer wallCancel()
	}

	a.persist(store.Record{Type: store.TypeRunStart, RunID: run.id})

	sysPrompt := a.systemPrompt
	if routed := routerSkillsText(a.cwd, a.opts, message); routed != "" {
		sysPrompt += "\n\n# Skills (matched)\n" + routed
	}

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
		SystemPrompt:      sysPrompt,
		ParallelTools:     a.opts.ParallelTools,
		ParallelToolsMax:  a.opts.ParallelToolsMax,
		Sink:              func(m llm.Message) { a.persistMessage(run.id, m) },
	}, func(ev runtime.Event) {
		run.emit(Event{
			Type:       EventType(ev.Type),
			Text:       ev.Text,
			ToolCallID: ev.ToolCallID,
			Name:       ev.Name,
			Args:       json.RawMessage(ev.Args),
			Content:    ev.Content,
			IsError:    ev.IsError,
			Step:       ev.Step,
			MaxSteps:   ev.MaxSteps,
			Code:       Code(ev.Code),
		})
	})

	a.mu.Lock()
	a.history = nonSystemMessages(result.Messages)
	a.mu.Unlock()

	rr := &RunResult{
		ID:     result.ID,
		Status: RunStatus(result.Status),
		Result: result.Result,
		Steps:  result.Steps,
		Err:    result.Err,
		Code:   Code(result.Code),
	}
	if result.Usage != nil {
		rr.Usage = &Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens,
		}
	}
	if rr.ID == "" {
		rr.ID = run.id
	} else {
		run.id = rr.ID
	}

	var usage *store.Usage
	if result.Usage != nil {
		usage = &store.Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens,
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
	run.finish(rr, err)
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

func (a *Agent) persist(rec store.Record) {
	if a.store == nil {
		return
	}
	_ = a.store.Append(a.id, rec)
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
	sessions := a.mcp
	a.mcp = nil
	plugMgr := a.plugins
	a.plugins = nil
	lspSup := a.lsp
	a.lsp = nil
	a.mu.Unlock()

	if st != nil {
		_ = st.Close()
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
	return opts, abs, nil
}

func newCompleter(opts Options) (llm.Completer, error) {
	cfg := llm.Config{
		APIKey:  opts.APIKey,
		BaseURL: opts.BaseURL,
		Model:   opts.Model.ID,
	}
	switch opts.Provider {
	case ProviderAnthropic:
		return llm.NewAnthropic(cfg), nil
	default:
		return llm.NewOpenAI(cfg), nil
	}
}
