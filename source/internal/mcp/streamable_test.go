// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/mcp"
)

type rpcIn struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func decodeRPC(t *testing.T, r *http.Request) rpcIn {
	t.Helper()
	var req rpcIn
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return req
}

// A Streamable HTTP server: the session id is issued at initialize and must
// come back on every later request, and an answer may arrive as an SSE body.
func TestStreamableHTTPSendsTheSessionAndParsesSSE(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	const sessionID = "session-abc"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRPC(t, r)
		mu.Lock()
		seen = append(seen, req.Method+"|session="+r.Header.Get("Mcp-Session-Id")+"|version="+r.Header.Get("MCP-Protocol-Version")+"|accept="+r.Header.Get("Accept"))
		mu.Unlock()

		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			http.Error(w, "Accept must offer event-stream", 400)
			return
		}
		switch req.Method {
		case "initialize":
			if got := r.Header.Get("Mcp-Session-Id"); got != "" {
				t.Errorf("initialize carried a session id %q", got)
			}
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "sse-http"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != sessionID {
				http.Error(w, "missing session id", 400)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			// A notification first: the parser has to skip it and keep looking
			// for the reply with this id.
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
			body, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": []map[string]any{{
					"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"},
				}}},
			})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
		case "tools/call":
			var p struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			text, _ := p.Arguments["text"].(string)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"content": []map[string]string{{"type": "text", "text": "echo:" + text}}},
			})
		default:
			http.Error(w, "unknown method", 400)
		}
	}))
	defer srv.Close()

	sess, err := mcp.Start(context.Background(), mcp.ServerConfig{Name: "remote", Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()
	if tools := sess.Tools(); len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%+v", tools)
	}
	out, err := sess.Call(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || out != "echo:hi" {
		t.Fatalf("out=%q err=%v", out, err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, line := range seen {
		parts := strings.Split(line, "|")
		method := parts[0]
		switch method {
		case "initialize":
			if !strings.Contains(line, "session=") || parts[1] != "session=" {
				t.Fatalf("initialize should carry no session id: %s", line)
			}
			if parts[2] != "version=" {
				t.Fatalf("initialize should carry no version header: %s", line)
			}
		default:
			if parts[1] != "session="+sessionID {
				t.Fatalf("%s did not carry the session id: %s", method, line)
			}
			if parts[2] != "version=2025-06-18" {
				t.Fatalf("%s did not carry the negotiated version: %s", method, line)
			}
		}
	}
}

// A server that refuses the current protocol version gets exactly one retry
// with the older one, and the older one is what later requests announce.
func TestStreamableHTTPFallsBackOneProtocolVersion(t *testing.T) {
	var versions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRPC(t, r)
		if req.Method == "initialize" {
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			versions = append(versions, p.ProtocolVersion)
			if p.ProtocolVersion != "2024-11-05" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32600, "message": "unsupported protocol version"},
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}},
			})
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if v := r.Header.Get("MCP-Protocol-Version"); v != "2024-11-05" {
			http.Error(w, "wrong version header "+v, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"tools": []any{}},
		})
	}))
	defer srv.Close()

	sess, err := mcp.Start(context.Background(), mcp.ServerConfig{Name: "remote", Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()
	if len(versions) != 2 || versions[0] != "2025-06-18" || versions[1] != "2024-11-05" {
		t.Fatalf("initialize versions=%v want one retry with 2024-11-05", versions)
	}
}

// 401 is a typed fact with the challenge's metadata URL, not a string that
// only looks like an error.
func TestStreamableHTTP401IsAuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://auth.example.test/.well-known/oauth-protected-resource/mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := mcp.Start(context.Background(), mcp.ServerConfig{Name: "remote", Transport: "http", URL: srv.URL})
	var authErr *mcp.AuthRequiredError
	if !errors.As(err, &authErr) {
		t.Fatalf("err=%v want *AuthRequiredError", err)
	}
	if authErr.ResourceMetadataURL != "https://auth.example.test/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("metadata=%q", authErr.ResourceMetadataURL)
	}
	if strings.Contains(err.Error(), "https://auth.example.test/.well-known/oauth-protected-resource/mcp?") {
		t.Fatalf("an unredacted metadata URL leaked into the error: %v", err)
	}
}

// A 404 on a request that carried a session id is an expired session, not an
// ordinary transport error.
func TestStreamableHTTP404WithSessionIsExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRPC(t, r)
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "gone")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}},
			})
		default:
			http.Error(w, "no such session", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	_, err := mcp.Start(context.Background(), mcp.ServerConfig{Name: "remote", Transport: "http", URL: srv.URL})
	var expired *mcp.SessionExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("err=%v want *SessionExpiredError", err)
	}
}

// The token source is asked per request, so a refreshed token reaches the next
// call without a reconnect.
func TestStreamableHTTPBearerTokenSource(t *testing.T) {
	var mu sync.Mutex
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.Header.Get("Authorization"))
		mu.Unlock()
		req := decodeRPC(t, r)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{"tools": []any{}}
		if req.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()

	calls := 0
	_, err := mcp.Start(context.Background(), mcp.ServerConfig{
		Name: "remote", Transport: "http", URL: srv.URL,
		AuthToken: func(context.Context) (string, error) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			return fmt.Sprintf("token-%d", n), nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// A notification writes nothing; the read loop consumed three requests:
	// initialize, notifications/initialized and tools/list.
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) == 0 || tokens[0] != "Bearer token-1" {
		t.Fatalf("tokens=%v want a bearer on the first request", tokens)
	}
	for _, token := range tokens {
		if !strings.HasPrefix(token, "Bearer token-") {
			t.Fatalf("request without a bearer token: %q", token)
		}
	}
}
