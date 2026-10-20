// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anvil-dev/anvil/internal/mcp"
	"github.com/anvil-dev/anvil/internal/settings"
	"github.com/anvil-dev/anvil/internal/tools"
)

// MCPState is where one declared server stands (0.44+).
type MCPState string

const (
	// MCPStateDisabled means the entry is declared but turned off: it shadows a
	// lower layer and is never started.
	MCPStateDisabled MCPState = "disabled"
	// MCPStateConnecting means a start is in flight.
	MCPStateConnecting MCPState = "connecting"
	MCPStateConnected  MCPState = "connected"
	// MCPStateFailed carries the start or transport error.
	MCPStateFailed MCPState = "failed"
	// MCPStateAuthRequired means the server answered that it wants a sign-in
	// (p_0.44.3 runs the flow; until then it is a distinct failure).
	MCPStateAuthRequired MCPState = "auth_required"
)

// MCPTool is one tool a connected server offers.
type MCPTool struct {
	// Server is the configured server name.
	Server      string
	Name        string
	Description string
	InputSchema json.RawMessage
}

// MCPServerStatus is one server's configuration and connection state.
//
// The status is a snapshot: it is copied on the way out, so reading it never
// races a reconnect. Secrets are not part of it — the runtime holds the
// configuration, and the status names the transport and the file instead.
type MCPServerStatus struct {
	Name      string
	Transport string
	Source    string
	Enabled   bool
	State     MCPState
	Error     string
	ToolCount int
	Tools     []MCPTool
	// ConnectedAt is when the current session was established; zero unless
	// State is connected.
	ConnectedAt time.Time
}

// MCPRuntimeOptions configures an MCPRuntime.
type MCPRuntimeOptions struct {
	// CWD is the workspace whose project file is read.
	CWD string
	// SettingSources selects the file layers, exactly as LocalOptions does.
	SettingSources []SettingSource
	// Inline is merged last and wins over the files, like Options.MCPServers.
	Inline map[string]MCPServerConfig
	// TokenDir is where OAuth tokens are stored (p_0.44.3). Empty uses the
	// user data directory's mcp/oauth folder.
	TokenDir string
	// ConnectTimeout bounds one server's start (default 15s).
	ConnectTimeout time.Duration
	// OnChange is called on every state transition, from whatever goroutine
	// caused it. A host forwards these as notifications; it must tolerate
	// concurrent calls.
	OnChange func(MCPServerStatus)
}

// MCPRuntime owns a set of MCP connections for one workspace and one set of
// setting sources (0.44+).
//
// Agents do not own these sessions: they borrow the tools, and every tool
// handler resolves the live session by server name, so a reconnect replaces
// the process without replacing the registry. The host keeps one runtime per
// (cwd, source set) and closes it on shutdown.
type MCPRuntime struct {
	mu      sync.RWMutex
	opts    MCPRuntimeOptions
	cwd     string
	entries map[string]*mcpEntry
	version atomic.Int64
	closed  bool
	// lifetime bounds a pending sign-in: Close cancels it, so a listener and a
	// Wait goroutine never outlive the runtime.
	lifetime context.Context
	cancel   context.CancelFunc
}

// MCPAuthentication is what an editor shows as "open this URL to sign in".
type MCPAuthentication struct {
	Server    string
	URL       string
	ExpiresAt time.Time
}

// mcpEntry is one server. ioMu is held for reading by a call and for writing
// while the session is retired, so a close waits for in-flight work. mu guards
// the pointer and the status; a status read never waits for a dial.
type mcpEntry struct {
	ioMu      sync.RWMutex
	mu        sync.RWMutex
	connectMu sync.Mutex
	// authMu serialises Authenticate and SignOut for one server; it is not held
	// across a dial or a call.
	authMu    sync.Mutex
	cfg       runtimeMCPConfig
	status    MCPServerStatus
	session   *mcp.Session
	oauth     *mcp.OAuth
	challenge string
}

// runtimeMCPConfig is one entry as the runtime holds it, with a hash that
// decides whether a reload has to restart the process.
type runtimeMCPConfig struct {
	name      string
	source    string
	transport string
	command   string
	args      []string
	env       []string
	url       string
	headers   map[string]string
	enabled   bool
	// The manual client registration, when the file supplies one (0.44+).
	clientID     string
	clientSecret string
	scope        string
	hash         string
}

// NewMCPRuntime records what to read and how to connect. It does not connect:
// Reload is the step that reads the files, so a constructor cannot fail on a
// workspace the caller is about to inspect.
func NewMCPRuntime(opts MCPRuntimeOptions) *MCPRuntime {
	cwd, err := resolveListCWD(opts.CWD)
	if err != nil {
		// resolveListCWD only fails when the process cannot resolve an absolute
		// path at all; keeping the raw value makes Reload report it in context.
		cwd = opts.CWD
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &MCPRuntime{opts: opts, cwd: cwd, entries: map[string]*mcpEntry{}, lifetime: lifetime, cancel: cancel}
}

// Version is a counter that moves whenever the tool snapshot or any status
// changed. An agent compares its recorded value before a turn.
func (r *MCPRuntime) Version() int64 {
	if r == nil {
		return 0
	}
	return r.version.Load()
}

// Reload reads the configured layers and makes the connections match them.
//
// An unchanged entry keeps its session. A changed one is retired and started
// again; removed entries are retired after their in-flight calls finish.
// Start failures become statuses, not errors: an unreadable configuration is
// the only thing that fails a reload, and it leaves the last good connections
// alone.
func (r *MCPRuntime) Reload(ctx context.Context) error {
	if r == nil {
		return newError("nil MCP runtime", false, nil)
	}
	configs, err := r.readConfig()
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return newError("MCP runtime is closed", false, nil)
	}
	var connect []*mcpEntry
	var retire []*mcpEntry
	for name, cfg := range configs {
		entry, ok := r.entries[name]
		if ok && entry.hash() == cfg.hash {
			continue
		}
		if ok {
			// The name stays, the process changes.
			entry.mu.Lock()
			entry.cfg = cfg
			entry.mu.Unlock()
			if !cfg.enabled {
				// Kept in the map: the file still declares it, and a manager
				// has to show what happened to it.
				retire = append(retire, entry)
				continue
			}
			connect = append(connect, entry)
			continue
		}
		entry = &mcpEntry{cfg: cfg}
		entry.status = MCPServerStatus{
			Name:      name,
			Transport: cfg.transport,
			Source:    cfg.source,
			Enabled:   cfg.enabled,
			State:     MCPStateDisabled,
		}
		r.entries[name] = entry
		if cfg.enabled {
			connect = append(connect, entry)
		}
	}
	var removed []*mcpEntry
	for name, entry := range r.entries {
		if _, ok := configs[name]; ok {
			continue
		}
		delete(r.entries, name)
		removed = append(removed, entry)
	}
	r.mu.Unlock()

	for _, entry := range removed {
		// A removed server has no status to report; the listing omits it.
		r.retire(entry, nil)
	}
	if len(removed) > 0 {
		// The agent's tool set changed even though nothing announced it.
		r.version.Add(1)
	}

	// Changed and new entries connect together: one slow server does not make
	// the next one wait.
	var wg sync.WaitGroup
	for _, entry := range connect {
		wg.Add(1)
		go func(e *mcpEntry) {
			defer wg.Done()
			_ = r.connect(ctx, e)
		}(entry)
	}
	wg.Wait()
	r.resolveCollisions()
	return nil
}

// Reconnect retires one server's session and starts a new one. It works while
// an agent is running: the handler resolves the session by name at call time.
func (r *MCPRuntime) Reconnect(ctx context.Context, name string) error {
	if r == nil {
		return newError("nil MCP runtime", false, nil)
	}
	r.mu.RLock()
	entry := r.entries[name]
	r.mu.RUnlock()
	if entry == nil {
		return newError("mcp server "+name+" is not configured", false, nil)
	}
	if !entry.cfg.enabled {
		return newError("mcp server "+name+" is disabled", false, nil)
	}
	err := r.connect(ctx, entry)
	r.resolveCollisions()
	return err
}

// Authenticate starts (or repeats) a sign-in for one remote server and returns
// the URL a browser should open. The flow completes in the background; on
// success the server is reconnected and its new state announced.
func (r *MCPRuntime) Authenticate(ctx context.Context, name string) (MCPAuthentication, error) {
	if r == nil {
		return MCPAuthentication{}, newError("nil MCP runtime", false, nil)
	}
	r.mu.RLock()
	entry := r.entries[name]
	r.mu.RUnlock()
	if entry == nil {
		return MCPAuthentication{}, newError("mcp server "+name+" is not configured", false, nil)
	}
	// One sign-in at a time per server: a second call during a pending flow
	// returns the same URL from Begin rather than opening a second listener.
	entry.authMu.Lock()
	defer entry.authMu.Unlock()

	entry.mu.RLock()
	cfg := entry.cfg
	auth := entry.oauth
	challenge := entry.challenge
	entry.mu.RUnlock()
	if !cfg.enabled {
		return MCPAuthentication{}, newError("mcp server "+name+" is disabled", false, nil)
	}
	if auth == nil {
		auth = r.oauthFor(cfg)
		if auth == nil {
			return MCPAuthentication{}, newError("mcp server "+name+" is not a remote server", false, nil)
		}
		entry.mu.Lock()
		entry.oauth = auth
		entry.mu.Unlock()
	}
	authorization, err := auth.Begin(ctx, challenge)
	if err != nil {
		r.setState(entry, MCPServerStatus{State: MCPStateFailed, Error: err.Error()})
		return MCPAuthentication{}, newError(err.Error(), false, nil)
	}
	go r.completeSignIn(entry, auth)
	return MCPAuthentication{Server: name, URL: authorization.URL, ExpiresAt: authorization.ExpiresAt}, nil
}

// completeSignIn is the background half: wait for the callback, then reconnect
// so the new token is what the next handshake uses.
func (r *MCPRuntime) completeSignIn(entry *mcpEntry, auth *mcp.OAuth) {
	if err := auth.Wait(r.lifetime); err != nil {
		if r.lifetime.Err() != nil {
			return // the runtime is closing; nobody waits for this status
		}
		r.setState(entry, MCPServerStatus{State: MCPStateFailed, Error: err.Error()})
		return
	}
	if r.lifetime.Err() != nil {
		return
	}
	_ = r.connect(r.lifetime, entry)
}

// SignOut deletes the stored tokens and puts the server back in auth_required.
func (r *MCPRuntime) SignOut(name string) error {
	if r == nil {
		return newError("nil MCP runtime", false, nil)
	}
	r.mu.RLock()
	entry := r.entries[name]
	r.mu.RUnlock()
	if entry == nil {
		return newError("mcp server "+name+" is not configured", false, nil)
	}
	entry.authMu.Lock()
	defer entry.authMu.Unlock()

	entry.mu.RLock()
	cfg := entry.cfg
	auth := entry.oauth
	entry.mu.RUnlock()
	if auth == nil {
		auth = r.oauthFor(cfg)
		if auth == nil {
			return newError("mcp server "+name+" is not a remote server", false, nil)
		}
		entry.mu.Lock()
		entry.oauth = auth
		entry.mu.Unlock()
	}
	if err := auth.SignOut(); err != nil {
		return newError(err.Error(), false, nil)
	}
	r.retire(entry, &MCPServerStatus{State: MCPStateAuthRequired, Error: "sign-in required"})
	return nil
}

// oauthFor builds the sign-in helper for a remote entry. stdio and the legacy
// SSE transport have no flow: they authenticate with env or headers.
func (r *MCPRuntime) oauthFor(cfg runtimeMCPConfig) *mcp.OAuth {
	if cfg.transport != "http" || cfg.url == "" {
		return nil
	}
	return mcp.NewOAuth(mcp.OAuthConfig{
		Server:       cfg.name,
		ServerURL:    cfg.url,
		TokenDir:     r.tokenDir(),
		ClientID:     cfg.clientID,
		ClientSecret: cfg.clientSecret,
		Scope:        cfg.scope,
	})
}

// tokenDir is ~/.anvil/mcp/oauth unless the caller names another directory.
func (r *MCPRuntime) tokenDir() string {
	if r.opts.TokenDir != "" {
		return r.opts.TokenDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".anvil", "mcp", "oauth")
}

// Statuses returns every known server, sorted by name.
func (r *MCPRuntime) Statuses() []MCPServerStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]*mcpEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, r.entries[name])
	}
	r.mu.RUnlock()

	out := make([]MCPServerStatus, 0, len(entries))
	for _, entry := range entries {
		out = append(out, r.statusOf(entry))
	}
	return out
}

// Status reports one server.
func (r *MCPRuntime) Status(name string) (MCPServerStatus, bool) {
	if r == nil {
		return MCPServerStatus{}, false
	}
	r.mu.RLock()
	entry := r.entries[name]
	r.mu.RUnlock()
	if entry == nil {
		return MCPServerStatus{}, false
	}
	return r.statusOf(entry), true
}

// Tools returns the tools of every connected server, ordered by server name
// and then the order the server listed them.
func (r *MCPRuntime) Tools() []MCPTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	statuses := make([]MCPServerStatus, 0, len(r.entries))
	for _, entry := range r.entries {
		statuses = append(statuses, r.statusOf(entry))
	}
	r.mu.RUnlock()
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })

	var out []MCPTool
	for _, status := range statuses {
		if status.State != MCPStateConnected {
			continue
		}
		out = append(out, status.Tools...)
	}
	return out
}

// Call invokes one tool on one server. The session is resolved when the call
// happens, so a handler captured before a reconnect still reaches the new
// process. The read lock keeps a retirement from closing the session under an
// in-flight call.
func (r *MCPRuntime) Call(ctx context.Context, server, tool string, args json.RawMessage) (string, error) {
	if r == nil {
		return "", newError("nil MCP runtime", false, nil)
	}
	r.mu.RLock()
	entry := r.entries[server]
	r.mu.RUnlock()
	if entry == nil {
		return "", newError("mcp server "+server+" is not configured", false, nil)
	}
	entry.ioMu.RLock()
	defer entry.ioMu.RUnlock()
	entry.mu.RLock()
	session := entry.session
	state := entry.status.State
	entry.mu.RUnlock()
	if session == nil {
		return "", newError(fmt.Sprintf("mcp server %s is %s", server, state), false, nil)
	}
	out, err := session.Call(ctx, tool, args)
	if err != nil {
		var authErr *mcp.AuthRequiredError
		if errors.As(err, &authErr) {
			entry.mu.Lock()
			entry.challenge = authErr.Challenge
			entry.mu.Unlock()
			r.setState(entry, MCPServerStatus{State: MCPStateAuthRequired, Error: "sign-in required"})
		}
	}
	return out, err
}

// Close retires every session and makes the runtime unusable. It is safe to
// call more than once.
func (r *MCPRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	entries := make([]*mcpEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.entries = map[string]*mcpEntry{}
	r.mu.Unlock()
	r.cancel()

	for _, entry := range entries {
		r.retire(entry, nil)
	}
	r.version.Add(1)
	return nil
}

// readConfig resolves the same precedence the non-runtime merge uses — user,
// then project, then inline — but keeps disabled entries, because a manager has
// to show them and a disabled entry still shadows the layer below it.
func (r *MCPRuntime) readConfig() (map[string]runtimeMCPConfig, error) {
	out := map[string]runtimeMCPConfig{}
	read := func(load func() (map[string]settings.MCPServer, string, error)) error {
		servers, source, err := load()
		if err != nil {
			return err
		}
		for name, s := range servers {
			out[name] = configFromServer(name, mcpFromSettings(s), source)
		}
		return nil
	}
	if hasSettingSource(r.opts.SettingSources, SettingSourceUser) {
		if err := read(func() (map[string]settings.MCPServer, string, error) {
			path, err := settings.UserMCPPath()
			if err != nil {
				return nil, "", newError("resolve user mcp path", false, err)
			}
			servers, err := settings.LoadMCPJSON(path)
			if err != nil {
				return nil, "", newError("load user mcp.json", false, err)
			}
			return servers, path, nil
		}); err != nil {
			return nil, err
		}
	}
	if hasSettingSource(r.opts.SettingSources, SettingSourceProject) {
		path := settings.ProjectMCPPath(r.cwd)
		servers, err := settings.LoadMCPJSON(path)
		if err != nil {
			return nil, newError("load project mcp.json", false, err)
		}
		for name, s := range servers {
			out[name] = configFromServer(name, mcpFromSettings(s), path)
		}
	}
	for name, cfg := range r.opts.Inline {
		out[name] = configFromServer(name, cfg, "inline")
	}
	return out, nil
}

func hasSettingSource(sources []SettingSource, want SettingSource) bool {
	for _, s := range sources {
		if s == want {
			return true
		}
	}
	return false
}

func configFromServer(name string, cfg MCPServerConfig, source string) runtimeMCPConfig {
	enabled := cfg.Enabled == nil || *cfg.Enabled
	transport := settings.MCPServer{Transport: cfg.Transport}.TransportName()
	if transport == "" {
		transport = mcpTransport(cfg)
	}
	out := runtimeMCPConfig{
		name:      name,
		source:    source,
		transport: transport,
		command:   cfg.Command,
		args:      append([]string(nil), cfg.Args...),
		env:       append([]string(nil), cfg.Env...),
		url:       cfg.URL,
		headers:   cfg.Headers,
		enabled:   enabled,
	}
	if cfg.OAuth != nil {
		out.clientID = cfg.OAuth.ClientID
		out.clientSecret = cfg.OAuth.ClientSecret
		out.scope = cfg.OAuth.Scope
	}
	out.hash = out.fingerprint()
	return out
}

// fingerprint is what "the same server" means for a reload: a change to any
// field that reaches the process, and nothing else (the source file's path is
// not part of it — moving a declaration between layers does not restart it).
func (c runtimeMCPConfig) fingerprint() string {
	h := sha256.New()
	fmt.Fprintf(h, "enabled=%t\n", c.enabled)
	fmt.Fprintf(h, "transport=%s\n", c.transport)
	fmt.Fprintf(h, "command=%s\n", c.command)
	fmt.Fprintf(h, "args=%q\n", c.args)
	fmt.Fprintf(h, "url=%s\n", c.url)
	for _, kv := range sortedStrings(c.env) {
		fmt.Fprintf(h, "env=%s\n", kv)
	}
	keys := make([]string, 0, len(c.headers))
	for k := range c.headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "header=%s=%s\n", k, c.headers[k])
	}
	// Only the digest leaves this function; a secret in it is not readable.
	fmt.Fprintf(h, "client=%s\n", c.clientID)
	fmt.Fprintf(h, "secret=%s\n", c.clientSecret)
	fmt.Fprintf(h, "scope=%s\n", c.scope)
	return hex.EncodeToString(h.Sum(nil))
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func (r *MCPRuntime) serverConfig(cfg runtimeMCPConfig) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:      cfg.name,
		Transport: cfg.transport,
		Command:   cfg.command,
		Args:      cfg.args,
		Env:       cfg.env,
		URL:       cfg.url,
		Headers:   cfg.headers,
	}
}

// connect retires whatever session the entry had and starts a new one. Failure
// is recorded, not returned as a reload error: a broken server must not take
// the rest of the workspace with it.
func (r *MCPRuntime) connect(ctx context.Context, entry *mcpEntry) error {
	entry.connectMu.Lock()
	defer entry.connectMu.Unlock()

	r.retire(entry, &MCPServerStatus{State: MCPStateConnecting})

	entry.mu.RLock()
	cfg := entry.cfg
	entry.mu.RUnlock()

	auth := r.oauthFor(cfg)
	entry.mu.Lock()
	entry.oauth = auth
	entry.mu.Unlock()

	timeout := r.opts.ConnectTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	serverCfg := r.serverConfig(cfg)
	if auth != nil {
		serverCfg.AuthToken = auth.Token
	}
	session, err := mcp.Start(startCtx, serverCfg)
	if err != nil {
		var authErr *mcp.AuthRequiredError
		if errors.As(err, &authErr) {
			entry.mu.Lock()
			entry.challenge = authErr.Challenge
			entry.mu.Unlock()
			r.setState(entry, MCPServerStatus{State: MCPStateAuthRequired, Error: "sign-in required"})
			return err
		}
		r.setState(entry, MCPServerStatus{State: MCPStateFailed, Error: err.Error()})
		return err
	}

	entry.mu.Lock()
	entry.session = session
	entry.status = MCPServerStatus{
		Name:        cfg.name,
		Transport:   cfg.transport,
		Source:      cfg.source,
		Enabled:     true,
		State:       MCPStateConnected,
		ToolCount:   len(session.Tools()),
		Tools:       toolsOf(cfg.name, session.Tools()),
		ConnectedAt: time.Now().UTC(),
	}
	status := entry.status
	entry.mu.Unlock()

	r.version.Add(1)
	r.notify(status)
	return nil
}

// retire closes the entry's session after in-flight calls finish. next, when
// non-nil, becomes the entry's new status (with Name/Transport/Source/Enabled
// filled from the entry); nil is for an entry that is leaving the runtime and
// has no status to report.
func (r *MCPRuntime) retire(entry *mcpEntry, next *MCPServerStatus) {
	entry.ioMu.Lock()
	entry.mu.Lock()
	cfg := entry.cfg
	session := entry.session
	auth := entry.oauth
	entry.session = nil
	var status MCPServerStatus
	if next != nil {
		status = *next
		if status.Name == "" {
			status.Name = cfg.name
		}
		if status.Transport == "" {
			status.Transport = cfg.transport
		}
		if status.Source == "" {
			status.Source = cfg.source
		}
		status.Enabled = cfg.enabled
		entry.status = status
	}
	entry.mu.Unlock()
	entry.ioMu.Unlock()

	if auth != nil {
		// A pending sign-in belongs to the session being retired; its loopback
		// listener should not outlive the connection it was opened for.
		auth.Cancel()
	}
	if session != nil {
		_ = session.Close()
	}
	if next != nil {
		r.version.Add(1)
		r.notify(status)
	}
}

func (r *MCPRuntime) setState(entry *mcpEntry, state MCPServerStatus) {
	entry.mu.Lock()
	if state.Name == "" {
		state.Name = entry.cfg.name
	}
	if state.Transport == "" {
		state.Transport = entry.cfg.transport
	}
	if state.Source == "" {
		state.Source = entry.cfg.source
	}
	state.Enabled = entry.cfg.enabled
	entry.status = state
	entry.mu.Unlock()
	r.version.Add(1)
	r.notify(state)
}

func (entry *mcpEntry) hash() string {
	entry.mu.RLock()
	defer entry.mu.RUnlock()
	return entry.cfg.hash
}

func (r *MCPRuntime) statusOf(entry *mcpEntry) MCPServerStatus {
	entry.mu.RLock()
	status := entry.status
	entry.mu.RUnlock()
	status.Tools = append([]MCPTool(nil), status.Tools...)
	status.ToolCount = len(status.Tools)
	return status
}

func (r *MCPRuntime) notify(status MCPServerStatus) {
	if cb := r.opts.OnChange; cb != nil {
		status.Tools = append([]MCPTool(nil), status.Tools...)
		cb(status)
	}
}

// resolveCollisions finds two servers whose tools register under the same
// name. The registry would refuse the second one and leave the first usable by
// accident; saying which server lost, and marking it failed, is the honest
// answer.
func (r *MCPRuntime) resolveCollisions() {
	r.mu.RLock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]*mcpEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, r.entries[name])
	}
	r.mu.RUnlock()

	owner := map[string]string{}
	for _, entry := range entries {
		status := r.statusOf(entry)
		if status.State != MCPStateConnected {
			continue
		}
		var loser string
		for _, tool := range status.Tools {
			registered := runtimeToolName(status.Name, tool.Name)
			if first, ok := owner[registered]; ok {
				loser = fmt.Sprintf("tool %s collides with server %s", registered, first)
				break
			}
			owner[registered] = status.Name
		}
		if loser == "" {
			continue
		}
		r.retire(entry, &MCPServerStatus{
			Name:      status.Name,
			Transport: status.Transport,
			Source:    status.Source,
			Enabled:   true,
			State:     MCPStateFailed,
			Error:     loser,
		})
	}
}

// runtimeToolName is the registry name for one MCP tool. It is the same shape
// the non-runtime path registers, so a filter or a policy rule written against
// `mcp_<server>_<tool>` keeps working.
func runtimeToolName(server, tool string) string {
	return "mcp_" + sanitizeName(server) + "_" + sanitizeName(tool)
}

// registerMCPRuntimeTools registers one proxy tool per connected server tool.
// On a name collision it unregisters what it added, so a refused refresh leaves
// the registry as it found it.
func registerMCPRuntimeTools(reg *tools.Registry, rt *MCPRuntime) ([]string, error) {
	var registered []string
	undo := func() {
		for _, name := range registered {
			reg.Unregister(name)
		}
	}
	for _, tool := range rt.Tools() {
		name := runtimeToolName(tool.Server, tool.Name)
		server, toolName := tool.Server, tool.Name
		err := reg.Register(&tools.FuncTool{
			Name:        name,
			Description: fmt.Sprintf("[mcp:%s] %s", server, tool.Description),
			Parameters:  tool.InputSchema,
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				return rt.Call(ctx, server, toolName, args)
			},
		})
		if err != nil {
			undo()
			return nil, newError(err.Error(), false, nil)
		}
		registered = append(registered, name)
	}
	return registered, nil
}

// runtimeMCPState is what an agent records when it opens: the tool names the
// registry now holds and the runtime version they came from.
func runtimeMCPState(opts Options) ([]string, int64) {
	if opts.MCPRuntime == nil {
		return nil, 0
	}
	tools := opts.MCPRuntime.Tools()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, runtimeToolName(tool.Server, tool.Name))
	}
	return names, opts.MCPRuntime.Version()
}

func toolsOf(server string, tools []mcp.ToolInfo) []MCPTool {
	out := make([]MCPTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, MCPTool{
			Server:      server,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: append(json.RawMessage(nil), t.InputSchema...),
		})
	}
	return out
}
