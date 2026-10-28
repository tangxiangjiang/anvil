// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

// With thinking enabled and a tool call, the provider requires the assistant turn's thinking
// block, signature included, back on the next request of the same tool loop (docs: "Required:
// within a tool-use turn, pass thinking blocks back"). The adapter keeps the blocks as opaque
// bytes in Message.ProviderState and writes them first in that assistant turn.

const thinkingBlock = `{"type":"thinking","thinking":"I should call the tool.","signature":"SIG-abc123=="}`
const redactedBlock = `{"type":"redacted_thinking","data":"ENCRYPTED-xyz"}`

func anthropicBody(blocks ...string) string {
	return `{"stop_reason":"tool_use","content":[` + strings.Join(blocks, ",") +
		`],"usage":{"input_tokens":1,"output_tokens":1}}`
}

func sseThinkingStream() string {
	return strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"I should "}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"call the tool."}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG-abc123=="}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"get_marker","input":{}}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":1}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
}

func thinkingServer(t *testing.T, plain string, stream bool) (*httptest.Server, func() []byte) {
	t.Helper()
	var last []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last, _ = readAll(r)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(sseThinkingStream()))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(plain))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []byte { return last }
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, err := r.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			return []byte(buf.String()), nil
		}
	}
}

func callAnthropic(t *testing.T, srv *httptest.Server, stream bool, req llm.Request) *llm.Response {
	t.Helper()
	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	var resp *llm.Response
	var err error
	if stream {
		resp, err = c.CompleteStream(context.Background(), req, func(llm.Delta) {})
	} else {
		resp, err = c.Complete(context.Background(), req)
	}
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func assistantBlocks(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var parsed struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	for i := len(parsed.Messages) - 1; i >= 0; i-- {
		if parsed.Messages[i].Role == "assistant" {
			return parsed.Messages[i].Content
		}
	}
	t.Fatalf("no assistant message in %s", body)
	return nil
}

// A tool loop: the response's thinking block comes back, byte for byte, first in the
// assistant turn of the next request, in both the plain and the streaming path.
func TestThinkingBlockIsReturnedInTheToolLoop(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := map[bool]string{false: "plain", true: "stream"}[stream]
		t.Run(name, func(t *testing.T) {
			srv, body := thinkingServer(t, anthropicBody(thinkingBlock, `{"type":"tool_use","id":"t1","name":"get_marker","input":{}}`), stream)
			first := callAnthropic(t, srv, stream, goldenHistory())
			if len(first.Message.ProviderState) == 0 {
				t.Fatal("the thinking block was not kept")
			}
			if len(first.Message.ToolCalls) != 1 {
				t.Fatalf("tool calls = %+v", first.Message.ToolCalls)
			}

			next := goldenHistory()
			next.Effort = llm.EffortLow
			next.Messages = append(next.Messages, first.Message,
				llm.Message{Role: llm.RoleTool, ToolCallID: "t1", Name: "get_marker", Content: "MARK"})
			callAnthropic(t, srv, stream, next)

			blocks := assistantBlocks(t, body())
			if len(blocks) < 2 || blocks[0]["type"] != "thinking" {
				t.Fatalf("the assistant turn does not start with its thinking block: %v", blocks)
			}
			if blocks[0]["signature"] != "SIG-abc123==" || blocks[0]["thinking"] != "I should call the tool." {
				t.Fatalf("the thinking block was altered: %v", blocks[0])
			}
			if blocks[1]["type"] != "tool_use" {
				t.Fatalf("the tool_use did not follow the thinking block: %v", blocks)
			}
		})
	}
}

// redacted_thinking is the same requirement; it is kept and returned whole, in order.
func TestRedactedThinkingIsKeptInOrder(t *testing.T) {
	srv, body := thinkingServer(t, anthropicBody(thinkingBlock, redactedBlock, `{"type":"tool_use","id":"t1","name":"get_marker","input":{}}`), false)
	first := callAnthropic(t, srv, false, goldenHistory())
	next := goldenHistory()
	next.Messages = append(next.Messages, first.Message, llm.Message{Role: llm.RoleTool, ToolCallID: "t1", Content: "x"})
	callAnthropic(t, srv, false, next)
	blocks := assistantBlocks(t, body())
	if len(blocks) != 3 || blocks[0]["type"] != "thinking" || blocks[1]["type"] != "redacted_thinking" || blocks[1]["data"] != "ENCRYPTED-xyz" {
		t.Fatalf("blocks = %v", blocks)
	}
}

// A response with no thinking block changes nothing: ProviderState stays empty and the
// request is byte-identical to one built without the field.
func TestNoThinkingBlockLeavesTheRequestUnchanged(t *testing.T) {
	srv, body := thinkingServer(t, anthropicBody(`{"type":"tool_use","id":"t1","name":"get_marker","input":{}}`), false)
	first := callAnthropic(t, srv, false, goldenHistory())
	if len(first.Message.ProviderState) != 0 {
		t.Fatalf("ProviderState = %q with no thinking block", first.Message.ProviderState)
	}
	with := goldenHistory()
	with.Messages = append(with.Messages, first.Message, llm.Message{Role: llm.RoleTool, ToolCallID: "t1", Content: "x"})
	callAnthropic(t, srv, false, with)
	got := string(body())

	without := goldenHistory()
	m := first.Message
	m.ProviderState = nil
	without.Messages = append(without.Messages, m, llm.Message{Role: llm.RoleTool, ToolCallID: "t1", Content: "x"})
	callAnthropic(t, srv, false, without)
	if got != string(body()) {
		t.Fatalf("an empty ProviderState changed the request:\n%s\n%s", got, body())
	}
}

// The state is Anthropic's alone: the OpenAI encoder has no place to put it, so a session
// that changes provider between runs sends none of it.
func TestOpenAIEncoderNeverCarriesProviderState(t *testing.T) {
	for _, stream := range []bool{false, true} {
		srv, body := captureServer(t)
		msg := llm.Message{Role: llm.RoleAssistant, Content: "ok", ProviderState: []byte(thinkingBlock)}
		req := goldenHistory()
		req.Messages = append(req.Messages, msg, llm.Message{Role: llm.RoleUser, Content: "go on"})
		c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
		var err error
		if stream {
			_, err = c.CompleteStream(context.Background(), req, func(llm.Delta) {})
		} else {
			_, err = c.Complete(context.Background(), req)
		}
		if err != nil {
			t.Fatal(err)
		}
		raw := string(body())
		for _, leak := range []string{"SIG-abc123", "I should call the tool", "signature", "thinking"} {
			if strings.Contains(raw, leak) {
				t.Fatalf("stream=%v: the OpenAI request carries %q: %s", stream, leak, raw)
			}
		}
	}
}

// What goes back is the provider's own block and nothing else: a thinking block with its
// signature, or a redacted block with its data. The reasoning text a user sees is a
// separate stream (Delta.Reasoning) and is never what this field is built from.
func TestProviderStateIsOnlyTheProvidersOwnBlocks(t *testing.T) {
	srv, _ := thinkingServer(t, anthropicBody(
		`{"type":"text","text":"hello"}`,
		thinkingBlock,
		`{"type":"tool_use","id":"t1","name":"f","input":{}}`), false)
	resp := callAnthropic(t, srv, false, goldenHistory())
	var kept []map[string]any
	if err := json.Unmarshal(resp.Message.ProviderState, &kept); err != nil {
		t.Fatalf("ProviderState is not a block list: %v (%q)", err, resp.Message.ProviderState)
	}
	if len(kept) != 1 || kept[0]["type"] != "thinking" || kept[0]["signature"] != "SIG-abc123==" {
		t.Fatalf("kept = %v, want only the signed thinking block", kept)
	}
	if strings.Contains(resp.Message.Content, "call the tool") {
		t.Fatalf("thinking text leaked into the visible answer: %q", resp.Message.Content)
	}
}

// A message with no provider state marshals exactly as it did before the field existed, so
// every request recorded earlier keeps its digest. Without omitempty the field changed four
// recordings' digests.
func TestEmptyProviderStateDoesNotChangeTheEncodedMessage(t *testing.T) {
	plain, err := json.Marshal(llm.Message{Role: llm.RoleAssistant, Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "ProviderState") {
		t.Fatalf("an empty ProviderState is encoded: %s", plain)
	}
	with, _ := json.Marshal(llm.Message{Role: llm.RoleAssistant, Content: "x", ProviderState: []byte("[]")})
	if !strings.Contains(string(with), "ProviderState") {
		t.Fatalf("a non-empty ProviderState is not encoded: %s", with)
	}
}
