// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/anvil-dev/anvil/internal/tools"
)

// ExecConfig launches an external plugin binary (stdio JSON-RPC).
type ExecConfig struct {
	ID       string
	Command  string
	Args     []string
	CWD      string
	Env      []string
	Manifest Manifest // optional pre-parsed; re-validated on initialize
}

// ExecSession is a live exec plugin process.
type ExecSession struct {
	id   string
	man  Manifest
	cmd  *exec.Cmd
	stdin io.WriteCloser
	stdout *bufio.Reader
	mu    sync.Mutex
	nextID atomic.Int64
}

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// StartExec spawns the plugin, handshakes, and registers remote tools on host.
func StartExec(ctx context.Context, cfg ExecConfig, host Host) (*ExecSession, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("plugin %q: command is required", cfg.ID)
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if cfg.CWD != "" {
		cmd.Dir = cfg.CWD
	}
	// Scrubbed env: inherit PATH/SYSTEMROOT essentials + optional extras (no secrets dump).
	cmd.Env = scrubEnv(cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("plugin %q start: %w", cfg.ID, err)
	}
	s := &ExecSession{
		id:     cfg.ID,
		man:    cfg.Manifest,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}
	man, err := s.initialize(ctx, cfg.CWD)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	s.man = man
	toolsList, err := s.listTools(ctx)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	for _, t := range toolsList {
		name := t.Name
		sess := s
		if err := host.RegisterTool(&tools.FuncTool{
			Name:        name,
			Description: t.Description,
			Parameters:  t.Parameters,
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				return sess.CallTool(ctx, name, args)
			},
		}); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}

type remoteTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func (s *ExecSession) Manifest() Manifest { return s.man }

func (s *ExecSession) initialize(ctx context.Context, cwd string) (Manifest, error) {
	_ = ctx
	raw, err := s.call("initialize", map[string]any{"cwd": cwd})
	if err != nil {
		return Manifest{}, err
	}
	var out struct {
		Manifest Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Manifest{}, fmt.Errorf("plugin %q initialize decode: %w", s.id, err)
	}
	if err := out.Manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	if s.id != "" && out.Manifest.ID != s.id {
		return Manifest{}, fmt.Errorf("plugin id mismatch: want %q got %q", s.id, out.Manifest.ID)
	}
	return out.Manifest, nil
}

func (s *ExecSession) listTools(ctx context.Context) ([]remoteTool, error) {
	_ = ctx
	raw, err := s.call("list_tools", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []remoteTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("plugin %q list_tools: %w", s.id, err)
	}
	return out.Tools, nil
}

// CallTool invokes a remote tool.
func (s *ExecSession) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	_ = ctx
	if args == nil {
		args = json.RawMessage(`{}`)
	}
	raw, err := s.call("call_tool", map[string]any{
		"name":      name,
		"arguments": json.RawMessage(args),
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.IsError {
		return out.Content, fmt.Errorf("%s", out.Content)
	}
	return out.Content, nil
}

func (s *ExecSession) call(method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return nil, fmt.Errorf("plugin %q: process not running", s.id)
	}
	id := s.nextID.Add(1)
	req := rpcReq{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	line = append(line, '\n')
	if _, err := s.stdin.Write(line); err != nil {
		return nil, fmt.Errorf("plugin %q write: %w", s.id, err)
	}
	respLine, err := s.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("plugin %q read: %w", s.id, err)
	}
	var resp rpcResp
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return nil, fmt.Errorf("plugin %q response: %w", s.id, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("plugin %q %s: %s", s.id, method, resp.Error.Message)
	}
	return resp.Result, nil
}

// Close sends shutdown and kills the process.
func (s *ExecSession) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return nil
	}
	_, _ = s.callLocked("shutdown", nil)
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	s.cmd = nil
	return nil
}

func (s *ExecSession) callLocked(method string, params any) (json.RawMessage, error) {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil, fmt.Errorf("plugin %q: process not running", s.id)
	}
	id := s.nextID.Add(1)
	req := rpcReq{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	line = append(line, '\n')
	if _, err := s.stdin.Write(line); err != nil {
		return nil, err
	}
	respLine, err := s.stdout.ReadBytes('\n')
	if err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, err
	}
	var resp rpcResp
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return resp.Result, nil
}

func scrubEnv(extra []string) []string {
	base := []string{}
	for _, k := range []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE", "ComSpec"} {
		if v, ok := os.LookupEnv(k); ok {
			base = append(base, k+"="+v)
		}
	}
	return append(base, extra...)
}
