// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite internal/llm/testdata/*.golden")

// goldenHistory is a three-message history with one tool: the shape of a real
// request, and the one whose bytes must not move when no effort is configured.
func goldenHistory() llm.Request {
	return llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a test agent."},
			{Role: llm.RoleUser, Content: "read a.txt"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)}}},
			{Role: llm.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "hello"},
		},
		Tools: []llm.ToolDef{{
			Name: "read_file", Description: "Read a file.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		}},
	}
}

// captureServer answers one request with a minimal valid reply and returns the
// request body it received. It serves both wires: the path says which.
func captureServer(t *testing.T) (*httptest.Server, func() []byte) {
	t.Helper()
	var last []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last, _ = io.ReadAll(r.Body)
		if r.URL.Path == "/v1/messages" {
			if bytes.Contains(last, []byte(`"stream":true`)) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
				io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
				io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		if bytes.Contains(last, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []byte { return last }
}

// indented makes a golden diff readable and independent of json.Marshal's compact form.
func indented(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, raw)
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// TestEffortGolden proves that an empty effort sends today's request: the four bodies
// (two adapters, plain and streaming) are byte-identical to files recorded before
// the effort field existed. Regenerate only with -update-golden, and only on purpose.
func TestEffortGolden(t *testing.T) {
	cases := []struct {
		name string
		call func(srv *httptest.Server, req llm.Request) error
	}{
		{"openai_plain", func(srv *httptest.Server, req llm.Request) error {
			_, err := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).Complete(context.Background(), req)
			return err
		}},
		{"openai_stream", func(srv *httptest.Server, req llm.Request) error {
			_, err := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).CompleteStream(context.Background(), req, func(llm.Delta) {})
			return err
		}},
		{"anthropic_plain", func(srv *httptest.Server, req llm.Request) error {
			_, err := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).Complete(context.Background(), req)
			return err
		}},
		{"anthropic_stream", func(srv *httptest.Server, req llm.Request) error {
			_, err := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).CompleteStream(context.Background(), req, func(llm.Delta) {})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, body := captureServer(t)
			if err := tc.call(srv, goldenHistory()); err != nil {
				t.Fatal(err)
			}
			got := indented(t, body())
			path := filepath.Join("testdata", tc.name+".golden")
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden %s: %v", path, err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("request body changed with effort empty\nwant:\n%s\ngot:\n%s", want, got)
			}
		})
	}
}
