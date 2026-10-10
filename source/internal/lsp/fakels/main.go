// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Fake stdio language server for Anvil LSP tests.
package main

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
	docs := map[string]string{}
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
				"capabilities": map[string]any{
					"textDocumentSync": 1,
					"definitionProvider": true,
					"referencesProvider": true,
					"renameProvider":     true,
					"hoverProvider":      true,
				},
				"serverInfo": map[string]any{"name": "fakels", "version": "0.0.1"},
			})
		case "initialized", "exit":
			if req.Method == "exit" {
				return
			}
		case "shutdown":
			writeResult(*req.ID, nil)
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(req.Params, &p)
			docs[p.TextDocument.URI] = p.TextDocument.Text
			writeNotify("textDocument/publishDiagnostics", map[string]any{
				"uri": p.TextDocument.URI,
				"diagnostics": []map[string]any{{
					"range": map[string]any{
						"start": map[string]int{"line": 0, "character": 0},
						"end":   map[string]int{"line": 0, "character": 1},
					},
					"severity": 2,
					"source":   "fakels",
					"message":  "fake warning on open",
				}},
			})
		case "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if len(p.ContentChanges) > 0 {
				docs[p.TextDocument.URI] = p.ContentChanges[0].Text
			}
		case "textDocument/definition":
			uri := docURI(req.Params)
			writeResult(*req.ID, map[string]any{
				"uri": uri,
				"range": map[string]any{
					"start": map[string]int{"line": 1, "character": 5},
					"end":   map[string]int{"line": 1, "character": 10},
				},
			})
		case "textDocument/references":
			uri := docURI(req.Params)
			other := strings.Replace(uri, "a.go", "b.go", 1)
			writeResult(*req.ID, []any{
				map[string]any{
					"uri": uri,
					"range": map[string]any{
						"start": map[string]int{"line": 1, "character": 5},
						"end":   map[string]int{"line": 1, "character": 10},
					},
				},
				map[string]any{
					"uri": other,
					"range": map[string]any{
						"start": map[string]int{"line": 0, "character": 0},
						"end":   map[string]int{"line": 0, "character": 4},
					},
				},
			})
		case "textDocument/hover":
			writeResult(*req.ID, map[string]any{
				"contents": map[string]string{"kind": "markdown", "value": "fake hover docs"},
			})
		case "textDocument/rename":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				NewName string `json:"newName"`
			}
			_ = json.Unmarshal(req.Params, &p)
			uriA := p.TextDocument.URI
			uriB := strings.Replace(uriA, "a.go", "b.go", 1)
			// Also support escape probe: if newName is "__escape__", include outside path
			changes := map[string]any{
				uriA: []map[string]any{{
					"range": map[string]any{
						"start": map[string]int{"line": 0, "character": 0},
						"end":   map[string]int{"line": 0, "character": 3},
					},
					"newText": p.NewName,
				}},
				uriB: []map[string]any{{
					"range": map[string]any{
						"start": map[string]int{"line": 0, "character": 0},
						"end":   map[string]int{"line": 0, "character": 3},
					},
					"newText": p.NewName,
				}},
			}
			if p.NewName == "__escape__" {
				changes["file:///tmp/outside-anvil-escape.txt"] = []map[string]any{{
					"range": map[string]any{
						"start": map[string]int{"line": 0, "character": 0},
						"end":   map[string]int{"line": 0, "character": 1},
					},
					"newText": "x",
				}}
			}
			writeResult(*req.ID, map[string]any{"changes": changes})
		default:
			if req.ID != nil {
				writeErr(*req.ID, "method not found: "+req.Method)
			}
		}
	}
}

func docURI(params json.RawMessage) string {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	_ = json.Unmarshal(params, &p)
	return p.TextDocument.URI
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
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "content-length:") {
			n, err = strconv.Atoi(strings.TrimSpace(line[len("Content-Length:"):]))
			if err != nil {
				return nil, err
			}
		}
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeFrame(v any) {
	raw, _ := json.Marshal(v)
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(raw), raw)
}

func writeResult(id int64, result any) {
	writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeErr(id int64, msg string) {
	writeFrame(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32601, "message": msg},
	})
}

func writeNotify(method string, params any) {
	writeFrame(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
