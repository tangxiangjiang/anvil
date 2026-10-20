// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
}

// CompleteStream implements StreamCompleter for Anthropic SSE.
func (c *Anthropic) CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	if c.cfg.APIKey == "" {
		return nil, &HTTPError{Status: 401, Message: "missing API key", Retryable: false}
	}
	if onDelta == nil {
		onDelta = func(Delta) {}
	}
	system, messages := toAnthropicMessages(req.Messages, req.PromptCache)
	body := anthropicStreamRequest{
		Model:     firstNonEmpty(req.Model, c.cfg.Model),
		MaxTokens: 8192,
		System:    system,
		Messages:  messages,
		Tools:     toAnthropicTools(req.Tools),
		Stream:    true,
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
		return nil, &HTTPError{Status: 0, Message: "anthropic stream request failed", Retryable: true, Cause: err}
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
		return nil, mapHTTPStatus(httpResp.StatusCode, string(respBody))
	}

	var content strings.Builder
	var toolCalls []ToolCall
	var curTool *ToolCall
	var curArgs strings.Builder
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
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			ContentBlock struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
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
			continue
		}
		if envelope.Error != nil {
			return nil, &HTTPError{Status: httpResp.StatusCode, Message: envelope.Error.Message, Retryable: false}
		}
		switch envelope.Type {
		case "content_block_start":
			if envelope.ContentBlock.Type == "tool_use" {
				curTool = &ToolCall{ID: envelope.ContentBlock.ID, Name: envelope.ContentBlock.Name}
				curArgs.Reset()
			}
		case "content_block_delta":
			if envelope.Delta.Type == "text_delta" && envelope.Delta.Text != "" {
				content.WriteString(envelope.Delta.Text)
				onDelta(Delta{Text: envelope.Delta.Text})
			}
			if envelope.Delta.Type == "input_json_delta" {
				curArgs.WriteString(envelope.Delta.PartialJSON)
			}
		case "content_block_stop":
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
	return &Response{
		Message: Message{Role: RoleAssistant, Content: content.String(), ToolCalls: toolCalls},
		Usage:   usage,
	}, nil
}

var _ StreamCompleter = (*Anthropic)(nil)
