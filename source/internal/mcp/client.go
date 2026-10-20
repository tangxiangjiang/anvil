// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ServerConfig launches or connects to an MCP server.
type ServerConfig struct {
	Name      string
	Transport string // stdio | http | sse; empty defaults from Command/URL
	Command   string
	Args      []string
	Env       []string
	URL       string // http / sse base URL
	// Headers are applied to HTTP/SSE requests only (ignored for stdio).
	Headers map[string]string
	// AuthToken, when set, is called before every HTTP request and its result
	// sent as a bearer token. The runtime uses it to refresh an OAuth token
	// mid-session. The value is never logged or returned in an error.
	AuthToken func(ctx context.Context) (string, error)
}

// ToolInfo is one MCP tool descriptor.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Session is a live MCP connection (stdio, HTTP, or SSE).
type Session struct {
	name      string
	transport string // stdio | http | sse
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	mu        sync.Mutex
	nextID    atomic.Int64
	tools     []ToolInfo

	http    *http.Client
	baseURL string
	msgURL  string
	headers map[string]string
	// authToken, when set, supplies a bearer token per request. It is how OAuth
	// refresh happens without a reconnect; a nil source means the static
	// headers are the whole story.
	authToken func(context.Context) (string, error)
	// sessionID and protocolVersion are Streamable HTTP state: the id the
	// server issued at initialize, and the version it accepted.
	sessionID       string
	protocolVersion string
	sseBody         io.ReadCloser
	sseReader       *bufio.Reader
	sseReplies      chan json.RawMessage
	sseErr          chan error
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Start connects to the server and completes initialize + tools/list.
func Start(ctx context.Context, cfg ServerConfig) (*Session, error) {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	switch {
	case transport == "http" || transport == "sse" || (cfg.URL != "" && cfg.Command == ""):
		if transport == "" {
			transport = "http"
		}
		cfg.Transport = transport
		return StartRemote(ctx, cfg)
	case transport == "" || transport == "stdio":
		return startStdio(ctx, cfg)
	default:
		if cfg.URL != "" {
			cfg.Transport = transport
			return StartRemote(ctx, cfg)
		}
		return nil, fmt.Errorf("mcp %s: unknown transport %q", cfg.Name, cfg.Transport)
	}
}

func startStdio(ctx context.Context, cfg ServerConfig) (*Session, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp %s: command is required", cfg.Name)
	}
	// The process outlives the handshake. ctx bounds initialize and tools/list,
	// and a runtime cancels it as soon as Start returns; tying the child to it
	// would kill the server at that moment. The session's own cancel is what
	// stops the child, through Close.
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cmd := exec.CommandContext(lifetime, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	s := &Session{
		name:      cfg.Name,
		transport: "stdio",
		cmd:       cmd,
		cancel:    cancel,
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	s.stdin = stdin
	s.stdout = bufio.NewReader(stdout)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("mcp %s start: %w", cfg.Name, err)
	}
	if err := s.initialize(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	tools, err := s.listTools(ctx)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	s.tools = tools
	return s, nil
}

// Name returns the configured server name.
func (s *Session) Name() string { return s.name }

// Tools returns tools discovered at start.
func (s *Session) Tools() []ToolInfo { return append([]ToolInfo(nil), s.tools...) }

// Call invokes tools/call.
func (s *Session) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var arguments any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return "", err
		}
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	raw, err := s.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw), nil
	}
	var b strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" || c.Type == "" {
			b.WriteString(c.Text)
		}
	}
	out := b.String()
	if result.IsError {
		return out, fmt.Errorf("mcp tool error")
	}
	return out, nil
}

// Close stops the server process or remote connection.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.sseBody != nil {
		_ = s.sseBody.Close()
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	return nil
}

func (s *Session) initialize(ctx context.Context) error {
	// Streamable HTTP opens with the current version and falls back once if the
	// server refuses it. stdio and the legacy SSE transport keep the version
	// their servers have always been sent.
	version := "2024-11-05"
	if s.transport == "http" {
		version = "2025-06-18"
	}
	for {
		raw, err := s.request(ctx, "initialize", map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "anvil",
				"version": "0.3.0",
			},
		})
		if err != nil {
			if version == "2025-06-18" && rejectableVersion(err) {
				version = "2024-11-05"
				continue
			}
			return err
		}
		var result struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(raw, &result)
		s.protocolVersion = result.ProtocolVersion
		if s.protocolVersion == "" {
			s.protocolVersion = version
		}
		break
	}
	return s.notify(ctx, "notifications/initialized", map[string]any{})
}

func (s *Session) listTools(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	cursor := ""
	for page := 0; ; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := s.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		for _, t := range result.Tools {
			schema := t.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			out = append(out, ToolInfo{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			})
		}
		if result.NextCursor == "" {
			return out, nil
		}
		// A server that repeats a cursor is not paging; treating it as "more"
		// would loop forever while holding the session lock.
		if result.NextCursor == cursor || page >= 100 {
			return nil, fmt.Errorf("mcp %s: tools/list cursor loop", s.name)
		}
		cursor = result.NextCursor
	}
}

func (s *Session) notify(ctx context.Context, method string, params any) error {
	if s.transport == "http" || s.transport == "sse" {
		return s.notifyHTTP(ctx, method, params)
	}
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	return s.writeLocked(req)
}

func (s *Session) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s.transport == "http" || s.transport == "sse" {
		return s.requestHTTP(ctx, method, params)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID.Add(1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := s.writeLocked(req); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		msg, err := s.readLocked()
		if err != nil {
			return nil, err
		}
		// Skip notifications (no id).
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(msg, &probe); err != nil {
			return nil, err
		}
		if _, ok := probe["id"]; !ok {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			return nil, err
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (s *Session) writeLocked(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(s.stdin, header); err != nil {
		return err
	}
	_, err = s.stdin.Write(body)
	return err
}

func (s *Session) readLocked() (json.RawMessage, error) {
	var contentLength int
	for {
		line, err := s.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			n := strings.TrimSpace(line[strings.Index(lower, ":")+1:])
			contentLength, err = strconv.Atoi(n)
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("mcp: missing Content-Length")
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(s.stdout, buf); err != nil {
		return nil, err
	}
	return json.RawMessage(buf), nil
}
