// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultInitTimeout    = 15 * time.Second
	defaultRequestTimeout = 20 * time.Second
)

// Session is one language server process + connection.
type Session struct {
	cfg     ServerConfig
	cwd     string
	cmd     *exec.Cmd
	conn    *Conn
	cancel  context.CancelFunc
	mu      sync.Mutex
	opened  map[string]int // abs path -> version
	diags   map[string][]Diagnostic
	ready   bool
	startErr error
}

func startSession(ctx context.Context, cwd string, cfg ServerConfig, initTimeout time.Duration) (*Session, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("lsp server command is empty")
	}
	if initTimeout <= 0 {
		initTimeout = defaultInitTimeout
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = cwd
	cmd.Env = scrubLSPEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	conn := NewConn(stdout, stdin)
	s := &Session{
		cfg:    cfg,
		cwd:    cwd,
		cmd:    cmd,
		conn:   conn,
		cancel: cancel,
		opened: map[string]int{},
		diags:  map[string][]Diagnostic{},
	}
	conn.OnNotification(func(method string, params json.RawMessage) {
		s.handleNotify(method, params)
	})
	go func() { _ = conn.Serve(serveCtx) }()

	initCtx, initCancel := context.WithTimeout(ctx, initTimeout)
	defer initCancel()
	rootURI := pathToURI(cwd)
	_, err = conn.Request(initCtx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"didSave":   true,
					"didChange": true,
				},
				"definition": map[string]any{},
				"references": map[string]any{},
				"rename":     map[string]any{"prepareSupport": false},
				"publishDiagnostics": map[string]any{},
				"hover":      map[string]any{},
			},
			"workspace": map[string]any{
				"workspaceEdit": map[string]any{
					"documentChanges": true,
				},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  rootURI,
			"name": filepath.Base(cwd),
		}},
	})
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := conn.Notify("initialized", map[string]any{}); err != nil {
		s.Close()
		return nil, err
	}
	s.ready = true
	return s, nil
}

func scrubLSPEnv() []string {
	keep := []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "COMSPEC", "TMP", "TEMP", "HOME", "USERPROFILE", "GOROOT", "GOPATH", "GOMODCACHE"}
	var env []string
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (s *Session) handleNotify(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	path, err := uriToPath(p.URI)
	if err != nil || !insideCWD(s.cwd, path) {
		return
	}
	s.mu.Lock()
	s.diags[path] = p.Diagnostics
	s.mu.Unlock()
}

// EnsureOpen sends didOpen if needed (or didChange when content provided).
func (s *Session) EnsureOpen(ctx context.Context, absPath, languageID, text string) error {
	s.mu.Lock()
	ver, opened := s.opened[absPath]
	s.mu.Unlock()
	uri := pathToURI(absPath)
	if !opened {
		if err := s.conn.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": languageID,
				"version":    1,
				"text":       text,
			},
		}); err != nil {
			return err
		}
		s.mu.Lock()
		s.opened[absPath] = 1
		s.mu.Unlock()
		return nil
	}
	ver++
	if err := s.conn.Notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": ver,
		},
		"contentChanges": []map[string]any{{"text": text}},
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.opened[absPath] = ver
	s.mu.Unlock()
	_ = ctx
	return nil
}

// SyncFile forces didOpen/didChange with on-disk content.
func (s *Session) SyncFile(ctx context.Context, absPath string) error {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	return s.EnsureOpen(ctx, absPath, s.cfg.Language, string(data))
}

func (s *Session) request(ctx context.Context, timeout time.Duration, method string, params any) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.conn.Request(rctx, method, params)
}

// Close shuts down the language server.
func (s *Session) Close() {
	if s == nil {
		return
	}
	if s.conn != nil && s.ready {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = s.conn.Request(ctx, "shutdown", nil)
		_ = s.conn.Notify("exit", nil)
		cancel()
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		s.conn.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
}

func (s *Session) diagnosticsSnapshot() map[string][]Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]Diagnostic, len(s.diags))
	for k, v := range s.diags {
		cp := append([]Diagnostic(nil), v...)
		out[k] = cp
	}
	return out
}
