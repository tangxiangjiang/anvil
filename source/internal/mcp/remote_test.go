// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/mcp"
)

func TestHTTPHeaders(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "fake"}}
		case "notifications/initialized":
			w.WriteHeader(204)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		default:
			result = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()

	sess, err := mcp.Start(context.Background(), mcp.ServerConfig{
		Name:      "remote",
		Transport: "http",
		URL:       srv.URL,
		Headers:   map[string]string{"Authorization": "Bearer test-token-xyz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if sawAuth != "Bearer test-token-xyz" {
		t.Fatalf("Authorization=%q", sawAuth)
	}
}

func TestHTTPTransportEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "fake"}}
		case "notifications/initialized":
			w.WriteHeader(204)
			return
		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "echo text",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
				}},
			}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			text, _ := p.Arguments["text"].(string)
			result = map[string]any{
				"content": []map[string]string{{"type": "text", "text": "echo:" + text}},
			}
		default:
			http.Error(w, "unknown method "+req.Method, 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
	defer srv.Close()

	sess, err := mcp.Start(context.Background(), mcp.ServerConfig{
		Name:      "remote",
		Transport: "http",
		URL:       srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	tools := sess.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("%+v", tools)
	}
	out, err := sess.Call(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || out != "echo:hi" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestSSETransportEcho(t *testing.T) {
	var mu sync.Mutex
	subscribers := map[chan []byte]struct{}{}

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		ch := make(chan []byte, 8)
		mu.Lock()
		subscribers[ch] = struct{}{}
		mu.Unlock()
		defer func() {
			mu.Lock()
			delete(subscribers, ch)
			mu.Unlock()
		}()

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: endpoint\ndata: /message\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-ch:
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.Method == "notifications/initialized" || req.ID == 0 {
			w.WriteHeader(202)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "sse-fake"}}
		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "echo",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				}},
			}
		case "tools/call":
			var p struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			text, _ := p.Arguments["text"].(string)
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": "echo:" + text}}}
		default:
			http.Error(w, "bad method", 400)
			return
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		mu.Lock()
		for ch := range subscribers {
			select {
			case ch <- raw:
			default:
			}
		}
		mu.Unlock()
		w.WriteHeader(202)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	sess, err := mcp.Start(context.Background(), mcp.ServerConfig{
		Name:      "sse",
		Transport: "sse",
		URL:       srv.URL + "/sse",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	out, err := sess.Call(context.Background(), "echo", json.RawMessage(`{"text":"sse"}`))
	if err != nil || out != "echo:sse" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
