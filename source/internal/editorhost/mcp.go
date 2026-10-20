// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anvil-dev/anvil"
)

// This file is the host's MCP surface: the runtime registry that every session
// for one (cwd, setting_sources) pair shares, the listing that merges
// configuration with observed state, and the management handlers. The SDK owns
// connections, config writing and sign-in; the host routes calls to it and puts
// the answers on the wire. It imports nothing but the public package.

// hostMCPRuntime is one shared runtime plus what the host needs to route work to
// it. reloadMu serialises reloads, and background keeps a panel's polls from
// starting a second one.
type hostMCPRuntime struct {
	rt      *anvil.MCPRuntime
	sources []anvil.SettingSource
	key     string

	reloadMu   sync.Mutex
	background atomic.Bool
}

// key sources canonical form: sorted and de-duplicated, so ["user","project"]
// and ["project","user"] name one runtime rather than two.
func mcpRuntimeKey(cwd string, sources []anvil.SettingSource) string {
	names := make([]string, 0, len(sources))
	for _, src := range sources {
		names = append(names, string(src))
	}
	sort.Strings(names)
	compact := names[:0]
	for i, name := range names {
		if i == 0 || name != names[i-1] {
			compact = append(compact, name)
		}
	}
	return cwd + "\x00" + strings.Join(compact, ",")
}

// sourceNames is what a notification carries so a client shows two runtimes'
// servers apart.
func sourceNames(sources []anvil.SettingSource) []string {
	out := make([]string, 0, len(sources))
	for _, src := range sources {
		out = append(out, string(src))
	}
	return out
}

// mcpRuntimeFor returns the shared runtime for this workspace and source set,
// creating it on first use. It does not connect: callers choose between
// reload (a session's first use, a management call) and reloadInBackground (a
// listing that should not wait for a dial).
func (s *Server) mcpRuntimeFor(cwd string, sources []anvil.SettingSource) *hostMCPRuntime {
	key := mcpRuntimeKey(cwd, sources)
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if h, ok := s.mcp[key]; ok {
		return h
	}
	sources = canonicalSources(sources)
	h := &hostMCPRuntime{sources: sources, key: key}
	h.rt = anvil.NewMCPRuntime(anvil.MCPRuntimeOptions{
		CWD:            cwd,
		SettingSources: sources,
		TokenDir:       s.cfg.MCPTokenDir,
		OnChange: func(status anvil.MCPServerStatus) {
			s.notifyMCPStatus(h, status)
		},
	})
	s.mcp[key] = h
	return h
}

// reload reads the files and makes the connections match them, waiting for the
// starts it has to make.
func (h *hostMCPRuntime) reload(ctx context.Context) error {
	h.reloadMu.Lock()
	defer h.reloadMu.Unlock()
	return h.rt.Reload(ctx)
}

// reloadInBackground refreshes the runtime without making a caller wait. One
// reload at a time: a panel polling the listing must not stack dials.
func (h *hostMCPRuntime) reloadInBackground() {
	if !h.background.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer h.background.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = h.reload(ctx)
	}()
}

// closeMCPRuntimes closes every runtime. Called from closeAll after the sessions
// are gone: the agents borrow the runtime's sessions and must not outlive it.
func (s *Server) closeMCPRuntimes() {
	s.mcpMu.Lock()
	all := make([]*hostMCPRuntime, 0, len(s.mcp))
	for _, h := range s.mcp {
		all = append(all, h)
	}
	s.mcp = map[string]*hostMCPRuntime{}
	s.mcpMu.Unlock()
	for _, h := range all {
		if err := h.rt.Close(); err != nil {
			s.logf("close mcp runtime %s: %v", h.key, err)
		}
	}
}

// notifyMCPStatus is the runtime's OnChange. It runs on whatever goroutine
// caused the transition, so it only reads the status values it was handed.
func (s *Server) notifyMCPStatus(h *hostMCPRuntime, status anvil.MCPServerStatus) {
	s.notify(NotifyMCPStatus, MCPStatusParams{
		Server:         mcpServerFromStatus(status, false),
		SettingSources: sourceNames(h.sources),
	})
}

// refreshMCPForRuntime re-registers the tools of every idle session that shares
// this runtime. A session with a run in flight is left alone; its next Send
// refreshes, which is the SDK's own rule. Errors are swallowed here because a
// refusal can only mean the session started a run between the check and the
// call.
func (s *Server) refreshMCPForRuntime(key string) {
	s.mu.Lock()
	sessions := make([]*session, 0, len(s.sessions))
	for _, id := range s.sessionID {
		if sess, ok := s.sessions[id]; ok {
			sessions = append(sessions, sess)
		}
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		if sess.mcpKey != key {
			continue
		}
		if run, _ := sess.activeRun(); run != nil {
			continue
		}
		if agent := sess.sdk(); agent != nil {
			_ = agent.RefreshMCP()
		}
	}
}

// ---------------------------------------------------------------------------
// listing
// ---------------------------------------------------------------------------

// mcpListing merges what the files declare with what the runtime observed.
//
// A malformed enabled file is a result with a problem, not an error: the panel
// shows the parser error beside the last good statuses and makes the document
// read-only. Only a failure that leaves nothing to describe at all is an RPC
// error.
func (s *Server) mcpListing(ctx context.Context, sources []anvil.SettingSource, includeTools bool) (MCPListResult, *RPCError) {
	cwd := s.cwdPath()
	sources = canonicalSources(sources)
	out := MCPListResult{
		Runtime: true,
		Servers: []MCPServerInfo{},
		Sources: []MCPSourceInfo{},
	}
	if len(sources) == 0 {
		// No layer is read, so there is no runtime to ask: the setting is off.
		return out, nil
	}
	for _, src := range sources {
		path, err := anvil.MCPConfigPath(cwd, src)
		if err != nil {
			return MCPListResult{}, errHost(err)
		}
		_, statErr := os.Stat(path)
		out.Sources = append(out.Sources, MCPSourceInfo{
			Source: string(src),
			Path:   path,
			Exists: statErr == nil,
		})
	}

	h := s.mcpRuntimeFor(cwd, sources)
	h.reloadInBackground()
	statuses := map[string]anvil.MCPServerStatus{}
	for _, status := range h.rt.Statuses() {
		statuses[status.Name] = status
	}

	listing, listErr := anvil.ListMCPServers(cwd, sources)
	if listErr != nil {
		// The files cannot be read. The runtime may still hold the last good
		// connections, so they are what the panel draws beside the problem.
		out.Problem = mcpProblem(listErr)
		for _, status := range h.rt.Statuses() {
			out.Servers = append(out.Servers, mcpServerFromStatus(status, includeTools))
		}
		return out, nil
	}

	for _, declared := range listing.Servers {
		info := mcpServerFromListing(declared)
		if status, ok := statuses[declared.Name]; ok {
			mergeMCPStatus(&info, status, includeTools)
		} else if declared.Enabled {
			// Declared, enabled, and not yet reported: the reload that will
			// connect it has just been queued. Saying "connecting" is what is
			// happening; leaving the field empty would read as "no runtime".
			info.State = string(anvil.MCPStateConnecting)
		} else {
			info.State = string(anvil.MCPStateDisabled)
		}
		out.Servers = append(out.Servers, info)
	}
	return out, nil
}

func mcpServerFromListing(declared anvil.MCPServerInfo) MCPServerInfo {
	return MCPServerInfo{
		Name:        declared.Name,
		Transport:   declared.Transport,
		Source:      declared.Source,
		Enabled:     declared.Enabled,
		Command:     declared.Command,
		Args:        declared.Args,
		URL:         declared.URL,
		EnvNames:    declared.EnvNames,
		HeaderNames: declared.HeaderNames,
	}
}

func mcpServerFromStatus(status anvil.MCPServerStatus, includeTools bool) MCPServerInfo {
	info := MCPServerInfo{
		Name:      status.Name,
		Transport: status.Transport,
		Source:    status.Source,
		Enabled:   status.Enabled,
	}
	mergeMCPStatus(&info, status, includeTools)
	return info
}

// mergeMCPStatus copies the observed half onto a server object. The tools list
// is dropped unless the request asked for it, which keeps the chat card's reply
// small.
func mergeMCPStatus(info *MCPServerInfo, status anvil.MCPServerStatus, includeTools bool) {
	info.State = string(status.State)
	info.ToolCount = status.ToolCount
	info.Error = status.Error
	if !status.ConnectedAt.IsZero() {
		info.ConnectedAt = status.ConnectedAt.UTC().Format(time.RFC3339)
	}
	if includeTools && status.State == anvil.MCPStateConnected {
		tools := make([]MCPToolInfo, 0, len(status.Tools))
		for _, tool := range status.Tools {
			tools = append(tools, MCPToolInfo{Name: tool.Name, Description: tool.Description})
		}
		info.Tools = tools
	}
}

func mcpProblem(err error) *MCPProblem {
	var cfgErr *anvil.MCPConfigError
	if errors.As(err, &cfgErr) {
		return &MCPProblem{
			Path:    cfgErr.Path,
			Message: cfgErr.Message,
			Line:    cfgErr.Line,
			Column:  cfgErr.Column,
		}
	}
	return &MCPProblem{Message: err.Error()}
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

func (s *Server) handleMCPList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPListParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.list: " + err.Error())
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	return s.mcpListing(ctx, sources, p.IncludeTools)
}

func (s *Server) handleMCPAdd(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPAddParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.add: " + err.Error())
	}
	scope, rpcErr := mcpScope(p.Scope)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.add")
	if rpcErr != nil {
		return nil, rpcErr
	}
	draft := p.Server.draft()
	// A client that creates a server means it to run: the file's own default is
	// "enabled", and the draft is what the form showed.
	if draft.Enabled == nil {
		enabled := true
		draft.Enabled = &enabled
	}
	if _, err := anvil.AddMCPServer(s.cwdPath(), scope, draft, sources); err != nil {
		return nil, errHost(err)
	}
	return s.mcpAfterWrite(ctx, sources, p.IncludeTools)
}

func (s *Server) handleMCPUpdate(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPUpdateParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.update: " + err.Error())
	}
	scope, rpcErr := mcpScope(p.Scope)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.update")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if _, err := anvil.UpdateMCPServer(s.cwdPath(), scope, s.mcpUpdateDraft(scope, p.Server), sources); err != nil {
		return nil, errHost(err)
	}
	return s.mcpAfterWrite(ctx, sources, p.IncludeTools)
}

// mcpUpdateDraft is the draft an update writes. An omitted `enabled` keeps the
// file's value: the SDK's draft treats nil as "on", which would turn a disabled
// server back on whenever a form did not send the checkbox.
func (s *Server) mcpUpdateDraft(scope anvil.SettingSource, wire MCPServerDraftWire) anvil.MCPServerDraft {
	draft := wire.draft()
	if draft.Enabled == nil {
		draft.Enabled = s.mcpEnabledInScope(scope, draft.Name)
	}
	return draft
}

// mcpEnabledInScope reads one layer's own value for the entry. Nil means "not
// found or the file does not parse", which leaves the SDK's behaviour alone.
func (s *Server) mcpEnabledInScope(scope anvil.SettingSource, name string) *bool {
	listing, err := anvil.ListMCPServers(s.cwdPath(), []anvil.SettingSource{scope})
	if err != nil {
		return nil
	}
	for _, server := range listing.Servers {
		if server.Name == name {
			enabled := server.Enabled
			return &enabled
		}
	}
	return nil
}

func (s *Server) handleMCPRemove(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPRemoveParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.remove: " + err.Error())
	}
	scope, rpcErr := mcpScope(p.Scope)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.remove")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errInvalidParams("mcp.remove: name is required")
	}
	// Tokens belong to a server, not to the name: they are deleted only when no
	// other enabled layer still declares it. A malformed file cannot be read, so
	// in that case the tokens are left alone rather than guessed at.
	if !s.mcpDeclaredOutside(sources, scope, p.Name) {
		if h := s.mcpRuntimeFor(s.cwdPath(), sources); h != nil {
			// A stdio server has no tokens, and SignOut says so; the removal is
			// still what the caller asked for.
			_ = h.rt.SignOut(p.Name)
		}
	}
	if _, err := anvil.RemoveMCPServer(s.cwdPath(), scope, p.Name, sources); err != nil {
		return nil, errHost(err)
	}
	return s.mcpAfterWrite(ctx, sources, p.IncludeTools)
}

func (s *Server) handleMCPSetEnabled(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPSetEnabledParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.set_enabled: " + err.Error())
	}
	scope, rpcErr := mcpScope(p.Scope)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.set_enabled")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errInvalidParams("mcp.set_enabled: name is required")
	}
	if _, err := anvil.SetMCPServerEnabled(s.cwdPath(), scope, p.Name, p.Enabled, sources); err != nil {
		return nil, errHost(err)
	}
	return s.mcpAfterWrite(ctx, sources, p.IncludeTools)
}

func (s *Server) handleMCPReconnect(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPReconnectParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.reconnect: " + err.Error())
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.reconnect")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errInvalidParams("mcp.reconnect: name is required")
	}
	h := s.mcpRuntimeFor(s.cwdPath(), sources)
	_ = h.reload(ctx)
	// A failed restart is a state, not an RPC error: the listing carries the
	// reason and the panel keeps rendering the other servers.
	_ = h.rt.Reconnect(ctx, p.Name)
	s.refreshMCPForRuntime(h.key)
	return s.mcpListing(ctx, sources, p.IncludeTools)
}

func (s *Server) handleMCPAuthenticate(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPAuthenticateParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.authenticate: " + err.Error())
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.authenticate")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errInvalidParams("mcp.authenticate: name is required")
	}
	h := s.mcpRuntimeFor(s.cwdPath(), sources)
	_ = h.reload(ctx)
	authorization, err := h.rt.Authenticate(ctx, p.Name)
	if err != nil {
		return nil, errHost(err)
	}
	out := MCPAuthenticateResult{Server: authorization.Server, AuthURL: authorization.URL}
	if !authorization.ExpiresAt.IsZero() {
		out.ExpiresAt = authorization.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}

func (s *Server) handleMCPSignOut(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	p, err := decode[MCPSignOutParams](params)
	if err != nil {
		return nil, errInvalidParams("mcp.sign_out: " + err.Error())
	}
	sources, rpcErr := s.mcpSources(p.SettingSources, "mcp.sign_out")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errInvalidParams("mcp.sign_out: name is required")
	}
	h := s.mcpRuntimeFor(s.cwdPath(), sources)
	_ = h.reload(ctx)
	if err := h.rt.SignOut(p.Name); err != nil {
		return nil, errHost(err)
	}
	s.refreshMCPForRuntime(h.key)
	return s.mcpListing(ctx, sources, p.IncludeTools)
}

// mcpAfterWrite reloads the runtime a write changed, refreshes the idle
// sessions that share it, and answers the listing a new session would read.
func (s *Server) mcpAfterWrite(ctx context.Context, sources []anvil.SettingSource, includeTools bool) (any, *RPCError) {
	h := s.mcpRuntimeFor(s.cwdPath(), sources)
	_ = h.reload(ctx)
	s.refreshMCPForRuntime(h.key)
	return s.mcpListing(ctx, sources, includeTools)
}

// mcpDeclaredOutside reports whether any enabled layer other than the one being
// written still declares the name. The merged listing cannot answer this — it
// reports one entry per name — so each layer is read on its own. An unreadable
// layer answers true: do not delete a token on a guess.
func (s *Server) mcpDeclaredOutside(sources []anvil.SettingSource, scope anvil.SettingSource, name string) bool {
	for _, src := range sources {
		if src == scope {
			continue
		}
		listing, err := anvil.ListMCPServers(s.cwdPath(), []anvil.SettingSource{src})
		if err != nil {
			return true
		}
		for _, server := range listing.Servers {
			if server.Name == name {
				return true
			}
		}
	}
	return false
}

// mcpSources validates the wire's source list and canonicalises it, so the
// runtime key and the listing agree on what this call is about.
func (s *Server) mcpSources(raw []string, method string) ([]anvil.SettingSource, *RPCError) {
	sources, rpcErr := settingSourcesFrom(raw)
	if rpcErr != nil {
		return nil, errInvalidParams(method + ": " + rpcErr.Message)
	}
	return canonicalSources(sources), nil
}

// canonicalSources sorts and de-duplicates. Two callers sending the same set in
// a different order must reach one runtime.
func canonicalSources(sources []anvil.SettingSource) []anvil.SettingSource {
	out := make([]anvil.SettingSource, 0, len(sources))
	seen := map[anvil.SettingSource]bool{}
	for _, src := range sources {
		if seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func mcpScope(raw string) (anvil.SettingSource, *RPCError) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(anvil.SettingSourceProject):
		return anvil.SettingSourceProject, nil
	case string(anvil.SettingSourceUser):
		return anvil.SettingSourceUser, nil
	}
	return "", errInvalidParams(`scope must be "project" or "user"`)
}

// draft converts the wire shape into the SDK writer's draft. The pointer on
// Enabled is preserved so "not sent" stays distinct from false.
func (d MCPServerDraftWire) draft() anvil.MCPServerDraft {
	return anvil.MCPServerDraft{
		Name:      strings.TrimSpace(d.Name),
		Transport: d.Transport,
		Command:   d.Command,
		Args:      d.Args,
		Env:       d.Env,
		Headers:   d.Headers,
		URL:       d.URL,
		Enabled:   d.Enabled,
	}
}
