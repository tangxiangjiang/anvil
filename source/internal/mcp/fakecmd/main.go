// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package main

// Fake MCP stdio server for tests: exposes tool "echo".
import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
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
		if err := json.Unmarshal(msg, &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			writeResult(*req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "0.0.1"},
			})
		case "notifications/initialized":
			// no response
		case "tools/list":
			writeResult(*req.ID, map[string]any{
				"tools": []any{
					map[string]any{
						"name":        "echo",
						"description": "echo text",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"text": map[string]any{"type": "string"},
							},
						},
					},
				},
			})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			text, _ := p.Arguments["text"].(string)
			writeResult(*req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "echo:" + text}},
			})
		default:
			if req.ID != nil {
				writeErr(*req.ID, "method not found")
			}
		}
	}
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
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func writeErr(id int64, msg string) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32601, "message": msg},
	})
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
