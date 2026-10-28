// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

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

type anthropicStreamRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    any                `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
	Thinking  *anthropicThink    `json:"thinking,omitempty"`
}

// CompleteStream implements StreamCompleter for Anthropic SSE.
func (c *Anthropic) CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	if c.cfg.APIKey == "" {
		return nil, NewHTTPError(401, "missing API key")
	}
	if onDelta == nil {
		onDelta = func(Delta) {}
	}
	system, messages, err := toAnthropicMessages(req.Messages, req.PromptCache)
	if err != nil {
		return nil, err
	}
	body := anthropicStreamRequest{
		Model:     firstNonEmpty(req.Model, c.cfg.Model),
		MaxTokens: anthropicMaxTokens(req.Effort, req.MaxOutputTokens),
		System:    system,
		Messages:  messages,
		Tools:     toAnthropicTools(req.Tools),
		Stream:    true,
		Thinking:  thinkingFor(req.Effort),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := c.cfg.BaseURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, newTransportError("anthropic stream request failed", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
		return nil, withRetryAfter(mapHTTPStatus(httpResp.StatusCode, string(respBody)), httpResp.Header)
	}

	var content strings.Builder
	var toolCalls []ToolCall
	var stopReason string
	var curTool *ToolCall
	var curArgs strings.Builder
	var curThinking *thinkingAcc
	var thinkingBlocks []json.RawMessage
	var usage *Usage

	sc := bufio.NewScanner(httpResp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var envelope struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			ContentBlock struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
				// A redacted_thinking block arrives whole in content_block_start.
				Data string `json:"data"`
			} `json:"content_block"`
			Message struct {
				Usage *anthropicUsageWire `json:"usage"`
			} `json:"message"`
			Usage *anthropicUsageWire `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			// A chunk that does not parse may be a lost tool_use start or argument
			// fragment. Skipping it would return a call with truncated arguments and
			// no error (audit #16), so the stream fails instead.
			return nil, fmt.Errorf("anthropic stream: malformed event: %w", err)
		}
		if envelope.Error != nil {
			return nil, NewHTTPError(httpResp.StatusCode, envelope.Error.Message)
		}
		switch envelope.Type {
		case "content_block_start":
			switch envelope.ContentBlock.Type {
			case "tool_use":
				curTool = &ToolCall{ID: envelope.ContentBlock.ID, Name: envelope.ContentBlock.Name}
				curArgs.Reset()
			case "thinking":
				curThinking = &thinkingAcc{}
			case "redacted_thinking":
				if block, err := json.Marshal(map[string]string{"type": "redacted_thinking", "data": envelope.ContentBlock.Data}); err == nil {
					thinkingBlocks = append(thinkingBlocks, block)
				}
			}
		case "content_block_delta":
			if envelope.Delta.Type == "text_delta" && envelope.Delta.Text != "" {
				content.WriteString(envelope.Delta.Text)
				onDelta(Delta{Text: envelope.Delta.Text})
			}
			if envelope.Delta.Type == "input_json_delta" {
				curArgs.WriteString(envelope.Delta.PartialJSON)
			}
			// The reasoning text is a display stream; the block that goes back to the provider
			// is rebuilt from the same deltas, signature included, so it is the provider's own.
			if curThinking != nil {
				switch envelope.Delta.Type {
				case "thinking_delta":
					curThinking.text.WriteString(envelope.Delta.Thinking)
				case "signature_delta":
					curThinking.signature.WriteString(envelope.Delta.Signature)
				}
			}
		case "content_block_stop":
			if curThinking != nil {
				if block, err := json.Marshal(map[string]string{
					"type": "thinking", "thinking": curThinking.text.String(), "signature": curThinking.signature.String(),
				}); err == nil {
					thinkingBlocks = append(thinkingBlocks, block)
				}
				curThinking = nil
			}
			if curTool != nil {
				args := json.RawMessage(curArgs.String())
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				curTool.Args = args
				toolCalls = append(toolCalls, *curTool)
				curTool = nil
				curArgs.Reset()
			}
		case "message_start":
			if envelope.Message.Usage != nil {
				usage = envelope.Message.Usage.usage()
			}
		case "message_delta":
			if envelope.Delta.StopReason != "" {
				stopReason = envelope.Delta.StopReason
			}
			if envelope.Usage != nil {
				if usage == nil {
					usage = &Usage{}
				}
				if envelope.Usage.OutputTokens > 0 {
					usage.OutputTokens = envelope.Usage.OutputTokens
				}
				if envelope.Usage.InputTokens > 0 {
					usage.InputTokens = envelope.Usage.InputTokens
				}
				if envelope.Usage.CacheReadInputTokens > 0 {
					usage.CachedTokens = envelope.Usage.CacheReadInputTokens
				}
				if envelope.Usage.CacheCreationInputTokens > 0 {
					usage.CacheWriteTokens = envelope.Usage.CacheCreationInputTokens
				}
				usage.TotalTokens = usage.InputTokens + usage.CachedTokens + usage.CacheWriteTokens + usage.OutputTokens
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// A tool_use that started but never received its content_block_stop was cut
	// off, so its arguments are incomplete. Returning success would drop the
	// call without a word (audit #15); the stream fails instead.
	if curTool != nil {
		return nil, fmt.Errorf("anthropic stream: tool call %q was cut off before it finished", curTool.Name)
	}
	return &Response{
		Message:    Message{Role: RoleAssistant, Content: content.String(), ToolCalls: toolCalls, ProviderState: encodeProviderState(thinkingBlocks)},
		Usage:      usage,
		StopReason: anthropicStop(stopReason),
	}, nil
}

var _ StreamCompleter = (*Anthropic)(nil)

// thinkingAcc collects one streamed thinking block: its text and, arriving last, its signature.
type thinkingAcc struct {
	text      strings.Builder
	signature strings.Builder
}
