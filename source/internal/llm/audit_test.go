// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

// audit #15: a tool_use block still open when the Anthropic stream ends is dropped
// without an error, so the call returns success with the tool call missing.
func TestAuditAnthropicStreamTruncatedToolUseIsAnError(t *testing.T) {
	t.Skip("audit: open, see audit.md #15")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_start\n"+
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"read_file"}}`+"\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a\"}"}}`+"\n\n")
		// The stream ends here, with no content_block_stop.
	}))
	defer srv.Close()

	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := c.CompleteStream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, func(llm.Delta) {})
	if err == nil {
		t.Fatalf("a truncated tool_use returned success with %d tool call(s)", len(resp.Message.ToolCalls))
	}
}

// audit #16: a stream chunk whose JSON fails to decode is skipped silently, so a
// lost tool_use start yields a call with truncated arguments and no error.
func TestAuditAnthropicStreamBadChunkIsAnError(t *testing.T) {
	t.Skip("audit: open, see audit.md #16")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_start\n"+
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"read_file"}}`+"\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			`data: {bad json`+"\n\n")
		fmt.Fprint(w, "event: content_block_stop\n"+
			`data: {"type":"content_block_stop","index":0}`+"\n\n")
	}))
	defer srv.Close()

	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	_, err := c.CompleteStream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, func(llm.Delta) {})
	if err == nil {
		t.Fatal("a corrupt stream chunk was skipped and the call returned success")
	}
}
