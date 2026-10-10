// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Supervisor manages language servers for one agent cwd.
type Supervisor struct {
	cwd      string
	cfg      Config
	initTO   time.Duration
	reqTO    time.Duration
	mu       sync.Mutex
	sessions map[string]*Session // language -> session
	startErr map[string]error
}

// NewSupervisor builds a supervisor (does not start servers until needed).
func NewSupervisor(cwd string, cfg Config) *Supervisor {
	initTO := time.Duration(cfg.InitTimeoutMs) * time.Millisecond
	reqTO := time.Duration(cfg.RequestTimeoutMs) * time.Millisecond
	if initTO <= 0 {
		initTO = defaultInitTimeout
	}
	if reqTO <= 0 {
		reqTO = defaultRequestTimeout
	}
	return &Supervisor{
		cwd:      cwd,
		cfg:      cfg,
		initTO:   initTO,
		reqTO:    reqTO,
		sessions: map[string]*Session{},
		startErr: map[string]error{},
	}
}

// Close stops all sessions.
func (s *Supervisor) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		sess.Close()
	}
	s.sessions = map[string]*Session{}
}

// Warm starts all configured servers (used when Required=true).
func (s *Supervisor) Warm(ctx context.Context) error {
	for _, sc := range s.cfg.Servers {
		if _, err := s.sessionForLanguage(ctx, sc.Language); err != nil {
			return err
		}
	}
	return nil
}

func (s *Supervisor) sessionForLanguage(ctx context.Context, language string) (*Session, error) {
	s.mu.Lock()
	if sess, ok := s.sessions[language]; ok {
		s.mu.Unlock()
		return sess, nil
	}
	if err, ok := s.startErr[language]; ok {
		s.mu.Unlock()
		return nil, err
	}
	var sc *ServerConfig
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].Language == language {
			sc = &s.cfg.Servers[i]
			break
		}
	}
	s.mu.Unlock()
	if sc == nil {
		return nil, fmt.Errorf("no LSP server configured for language %q", language)
	}
	sess, err := startSession(ctx, s.cwd, *sc, s.initTO)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.startErr[language] = err
		return nil, err
	}
	s.sessions[language] = sess
	return sess, nil
}

func (s *Supervisor) sessionForPath(ctx context.Context, absPath string) (*Session, *ServerConfig, error) {
	rel, err := filepath.Rel(s.cwd, absPath)
	if err != nil {
		return nil, nil, err
	}
	rel = filepath.ToSlash(rel)
	var matched *ServerConfig
	for i := range s.cfg.Servers {
		sc := &s.cfg.Servers[i]
		if matchPattern(sc.Pattern, rel) {
			matched = sc
			break
		}
	}
	if matched == nil {
		return nil, nil, fmt.Errorf("no LSP server matches %s", rel)
	}
	sess, err := s.sessionForLanguage(ctx, matched.Language)
	return sess, matched, err
}

// NotifyFileChanged syncs a file Anvil just wrote (didOpen/didChange).
func (s *Supervisor) NotifyFileChanged(ctx context.Context, absPath string) {
	if s == nil {
		return
	}
	absPath = filepath.Clean(absPath)
	if !insideCWD(s.cwd, absPath) {
		return
	}
	sess, _, err := s.sessionForPath(ctx, absPath)
	if err != nil {
		return
	}
	_ = sess.SyncFile(ctx, absPath)
}

func (s *Supervisor) openPath(ctx context.Context, relOrAbs string) (*Session, string, error) {
	abs := relOrAbs
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.cwd, relOrAbs)
	}
	abs = filepath.Clean(abs)
	if !insideCWD(s.cwd, abs) {
		return nil, "", fmt.Errorf("path escapes cwd")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", err
	}
	sess, sc, err := s.sessionForPath(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	if err := sess.EnsureOpen(ctx, abs, sc.Language, string(data)); err != nil {
		return nil, "", err
	}
	return sess, abs, nil
}

// Definition resolves textDocument/definition.
func (s *Supervisor) Definition(ctx context.Context, path string, line, character int) ([]Location, error) {
	sess, abs, err := s.openPath(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, err := sess.request(ctx, s.reqTO, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     Position{Line: line, Character: character},
	})
	if err != nil {
		return nil, err
	}
	return parseLocations(raw, s.cwd)
}

// References resolves textDocument/references.
func (s *Supervisor) References(ctx context.Context, path string, line, character int, includeDecl bool) ([]Location, error) {
	sess, abs, err := s.openPath(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, err := sess.request(ctx, s.reqTO, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     Position{Line: line, Character: character},
		"context":      map[string]any{"includeDeclaration": includeDecl},
	})
	if err != nil {
		return nil, err
	}
	return parseLocations(raw, s.cwd)
}

// Hover resolves textDocument/hover (optional).
func (s *Supervisor) Hover(ctx context.Context, path string, line, character int) (string, error) {
	sess, abs, err := s.openPath(ctx, path)
	if err != nil {
		return "", err
	}
	raw, err := sess.request(ctx, s.reqTO, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     Position{Line: line, Character: character},
	})
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "(no hover)", nil
	}
	var h struct {
		Contents any `json:"contents"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return string(raw), nil
	}
	return formatHover(h.Contents), nil
}

// Diagnostics returns cached publishDiagnostics, optionally filtered by path prefix/glob.
func (s *Supervisor) Diagnostics(ctx context.Context, pathFilter string) ([]DiagHit, error) {
	_ = ctx
	// Ensure at least one session if path given.
	if pathFilter != "" {
		if _, _, err := s.openPath(ctx, pathFilter); err != nil {
			// still return whatever we have
			_ = err
		}
	} else {
		// open nothing; return accumulated
	}
	s.mu.Lock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

	var out []DiagHit
	for _, sess := range sessions {
		for path, diags := range sess.diagnosticsSnapshot() {
			rel, _ := filepath.Rel(s.cwd, path)
			rel = filepath.ToSlash(rel)
			if pathFilter != "" {
				pf := filepath.ToSlash(pathFilter)
				if !strings.Contains(rel, strings.TrimPrefix(pf, "./")) && rel != pf && pathFilter != rel {
					if ok, _ := filepath.Match(pf, rel); !ok {
						if ok2, _ := filepath.Match(filepath.Base(pf), filepath.Base(rel)); !ok2 {
							continue
						}
					}
				}
			}
			for _, d := range diags {
				out = append(out, DiagHit{
					Path:     rel,
					Line:     d.Range.Start.Line + 1,
					Character: d.Range.Start.Character,
					Severity: d.Severity,
					Message:  d.Message,
					Source:   d.Source,
				})
			}
		}
	}
	const maxDiags = 50
	if len(out) > maxDiags {
		out = out[:maxDiags]
	}
	return out, nil
}

// DiagHit is a tool-facing diagnostic.
type DiagHit struct {
	Path      string
	Line      int
	Character int
	Severity  int
	Message   string
	Source    string
}

// Rename requests textDocument/rename and returns a WorkspaceEdit.
func (s *Supervisor) Rename(ctx context.Context, path string, line, character int, newName string) (*WorkspaceEdit, error) {
	sess, abs, err := s.openPath(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, err := sess.request(ctx, s.reqTO, "textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     Position{Line: line, Character: character},
		"newName":      newName,
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("rename returned no edits")
	}
	var we WorkspaceEdit
	if err := json.Unmarshal(raw, &we); err != nil {
		return nil, err
	}
	return &we, nil
}

func parseLocations(raw json.RawMessage, cwd string) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var locs []Location
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &locs); err != nil {
			// single Location vs LocationLink — try one
			var one Location
			if err2 := json.Unmarshal(raw, &one); err2 == nil && one.URI != "" {
				locs = []Location{one}
			} else {
				return nil, err
			}
		}
	} else {
		var one Location
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		if one.URI != "" {
			locs = []Location{one}
		}
	}
	var out []Location
	for _, loc := range locs {
		p, err := uriToPath(loc.URI)
		if err != nil || !insideCWD(cwd, p) {
			continue
		}
		rel, _ := filepath.Rel(cwd, p)
		loc.URI = filepath.ToSlash(rel) // rewrite to relative path for tools
		out = append(out, loc)
	}
	return out, nil
}

func formatHover(contents any) string {
	switch v := contents.(type) {
	case string:
		return v
	case map[string]any:
		if val, ok := v["value"].(string); ok {
			return val
		}
	case []any:
		var parts []string
		for _, item := range v {
			parts = append(parts, formatHover(item))
		}
		return strings.Join(parts, "\n")
	}
	b, _ := json.Marshal(contents)
	return string(b)
}

// LoadProjectConfig reads .anvil/lsp.json if present.
func LoadProjectConfig(cwd string) (Config, error) {
	path := filepath.Join(cwd, ".anvil", "lsp.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// MergeConfig overlays file config under Options (Options wins on Enabled/Required/timeouts; servers append).
func MergeConfig(fileCfg, optsCfg Config) Config {
	out := fileCfg
	if optsCfg.Enabled {
		out.Enabled = true
	}
	if optsCfg.Required {
		out.Required = true
	}
	if optsCfg.InitTimeoutMs > 0 {
		out.InitTimeoutMs = optsCfg.InitTimeoutMs
	}
	if optsCfg.RequestTimeoutMs > 0 {
		out.RequestTimeoutMs = optsCfg.RequestTimeoutMs
	}
	if len(optsCfg.Servers) > 0 {
		out.Servers = append(out.Servers, optsCfg.Servers...)
	}
	// Deduplicate by language (opts last wins).
	byLang := map[string]ServerConfig{}
	for _, sc := range out.Servers {
		byLang[sc.Language] = sc
	}
	out.Servers = out.Servers[:0]
	for _, sc := range byLang {
		out.Servers = append(out.Servers, sc)
	}
	return out
}
