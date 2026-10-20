// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Fake MCP stdio server for runtime tests. Unlike internal/mcp/fakecmd it
// reports its own pid (so a test can tell one process from the next) and
// offers a tool that takes time (so a test can retire a server under a call).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if pidFile := os.Getenv("FAKE_MCP_PIDFILE"); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	in := bufio.NewReader(os.Stdin)
	for {
		msg, err := readMsg(in)
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintln(os.Stderr, err)
			return
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int64          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(msg, &req); err != nil || req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			writeResult(*req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fakeruntime", "version": "0.0.1"},
			})
		case "tools/list":
			writeResult(*req.ID, map[string]any{
				"tools": []any{
					map[string]any{
						"name":        "echo",
						"description": "echo text",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
					},
					map[string]any{
						"name":        "pid",
						"description": "the server process id",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					},
					map[string]any{
						"name":        "slow",
						"description": "sleep, then echo",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
							"ms":   map[string]any{"type": "number"},
							"text": map[string]any{"type": "string"},
						}},
					},
				},
			})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			switch p.Name {
			case "pid":
				writeResult(*req.ID, textResult(strconv.Itoa(os.Getpid())))
			case "slow":
				ms, _ := p.Arguments["ms"].(float64)
				text, _ := p.Arguments["text"].(string)
				time.Sleep(time.Duration(ms) * time.Millisecond)
				writeResult(*req.ID, textResult(text))
			default:
				text, _ := p.Arguments["text"].(string)
				writeResult(*req.ID, textResult("echo:"+text))
			}
		default:
			if req.ID != nil {
				writeErr(*req.ID, "method not found")
			}
		}
	}
}

func textResult(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}

func readMsg(r *bufio.Reader) (json.RawMessage, error) {
	var n int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			n, err = strconv.Atoi(strings.TrimSpace(line[strings.Index(lower, ":")+1:]))
			if err != nil {
				return nil, err
			}
		}
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeResult(id int64, result any) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func writeErr(id int64, msg string) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": msg}})
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
