package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StartRemote connects to an MCP server over HTTP JSON-RPC or SSE.
//
// Transport "http": each JSON-RPC call is a POST; the response body is a JSON-RPC object.
// Transport "sse": GET base URL for an SSE stream that advertises a message endpoint
// (event "endpoint"), then POSTs JSON-RPC to that endpoint and reads replies from the SSE stream.
func StartRemote(ctx context.Context, cfg ServerConfig) (*Session, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp %s: URL is required for transport %q", cfg.Name, cfg.Transport)
	}
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transport == "" {
		transport = "http"
	}
	s := &Session{
		name:      cfg.Name,
		transport: transport,
		baseURL:   strings.TrimRight(cfg.URL, "/"),
		http:      &http.Client{},
		msgURL:    strings.TrimRight(cfg.URL, "/"),
		headers:   cfg.Headers,
	}
	switch transport {
	case "http":
		// msgURL already base
	case "sse":
		if err := s.openSSE(ctx); err != nil {
			_ = s.Close()
			return nil, err
		}
	default:
		return nil, fmt.Errorf("mcp %s: unsupported transport %q", cfg.Name, cfg.Transport)
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

func (s *Session) applyHeaders(req *http.Request) {
	for k, v := range s.headers {
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}
}

func (s *Session) openSSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	s.applyHeaders(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return fmt.Errorf("mcp sse: status %d", resp.StatusCode)
	}
	s.sseBody = resp.Body
	s.sseReader = bufio.NewReader(resp.Body)
	s.sseReplies = make(chan json.RawMessage, 8)
	s.sseErr = make(chan error, 1)

	endpointCh := make(chan string, 1)
	go s.readSSE(endpointCh)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-s.sseErr:
		if err != nil {
			return err
		}
	case ep := <-endpointCh:
		if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
			s.msgURL = ep
		} else if strings.HasPrefix(ep, "/") {
			// relative to host of baseURL
			u := s.baseURL
			if i := strings.Index(u, "://"); i >= 0 {
				rest := u[i+3:]
				if j := strings.Index(rest, "/"); j >= 0 {
					s.msgURL = u[:i+3] + rest[:j] + ep
				} else {
					s.msgURL = u + ep
				}
			} else {
				s.msgURL = ep
			}
		} else {
			s.msgURL = ep
		}
	}
	return nil
}

func (s *Session) readSSE(endpointCh chan<- string) {
	var event, data string
	flush := func() {
		ev := event
		d := strings.TrimSpace(data)
		event, data = "", ""
		if d == "" {
			return
		}
		switch ev {
		case "endpoint":
			select {
			case endpointCh <- d:
			default:
			}
		case "message", "":
			select {
			case s.sseReplies <- json.RawMessage(d):
			default:
			}
		}
	}
	for {
		line, err := s.sseReader.ReadString('\n')
		if err != nil {
			select {
			case s.sseErr <- err:
			default:
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				data = part
			} else {
				data += "\n" + part
			}
		}
	}
}

func (s *Session) requestHTTP(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	reqBody := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	if s.transport == "sse" {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.msgURL, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		s.applyHeaders(httpReq)
		resp, err := s.http.Do(httpReq)
		if err != nil {
			return nil, err
		}
		io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("mcp sse post: status %d", resp.StatusCode)
		}
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case err := <-s.sseErr:
				if err != nil && err != io.EOF {
					return nil, err
				}
			case msg := <-s.sseReplies:
				var probe map[string]json.RawMessage
				if err := json.Unmarshal(msg, &probe); err != nil {
					continue
				}
				if _, ok := probe["id"]; !ok {
					continue
				}
				var respObj rpcResponse
				if err := json.Unmarshal(msg, &respObj); err != nil {
					return nil, err
				}
				if respObj.ID != id {
					continue
				}
				if respObj.Error != nil {
					return nil, fmt.Errorf("mcp %s: %s", method, respObj.Error.Message)
				}
				return respObj.Result, nil
			}
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.msgURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	s.applyHeaders(httpReq)
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp http: status %d: %s", resp.StatusCode, body)
	}
	var respObj rpcResponse
	if err := json.Unmarshal(body, &respObj); err != nil {
		return nil, err
	}
	if respObj.Error != nil {
		return nil, fmt.Errorf("mcp %s: %s", method, respObj.Error.Message)
	}
	return respObj.Result, nil
}

func (s *Session) notifyHTTP(ctx context.Context, method string, params any) error {
	reqBody := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.msgURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	s.applyHeaders(httpReq)
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return nil
}
