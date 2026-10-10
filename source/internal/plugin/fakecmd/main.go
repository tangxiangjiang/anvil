// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Command fakecmd is a stdio JSON-RPC exec plugin fixture for tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type req struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type resp struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	crash := os.Getenv("ANVIL_FAKE_PLUGIN_CRASH") == "1"
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var r req
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		switch r.Method {
		case "initialize":
			reply(r.ID, map[string]any{
				"manifest": map[string]any{
					"id":           "fakeexec",
					"version":      "0.1.0",
					"anvil_api":    1,
					"capabilities": []string{"tools.fake_echo"},
					"permissions":  []string{"fs.read", "net.none", "process.spawn"},
				},
			})
		case "list_tools":
			reply(r.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "fake_echo",
					"description": "Echo args.message from fakeexec plugin",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message": map[string]string{"type": "string"},
						},
					},
				}},
			})
		case "call_tool":
			if crash {
				os.Exit(2)
			}
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(r.Params, &p)
			var args struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(p.Arguments, &args)
			msg := args.Message
			if msg == "" {
				msg = "(empty)"
			}
			reply(r.ID, map[string]any{"content": "echo:" + msg, "is_error": false})
		case "shutdown":
			reply(r.ID, map[string]any{})
			return
		default:
			fail(r.ID, "unknown method "+r.Method)
		}
	}
	if err := sc.Err(); err != nil && !strings.Contains(err.Error(), "EOF") {
		fmt.Fprintln(os.Stderr, err)
	}
}

func reply(id int64, result any) {
	_ = json.NewEncoder(os.Stdout).Encode(resp{JSONRPC: "2.0", ID: id, Result: result})
}

func fail(id int64, msg string) {
	_ = json.NewEncoder(os.Stdout).Encode(resp{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: 1, Message: msg},
	})
}
