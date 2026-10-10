// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultAnthropicBase = "https://api.anthropic.com"
const anthropicVersion = "2023-06-01"

// Anthropic is a Claude Messages API client.
type Anthropic struct {
	cfg    Config
	client *http.Client
}

// NewAnthropic builds an Anthropic Messages client.
func NewAnthropic(cfg Config) *Anthropic {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultAnthropicBase
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Anthropic{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string        `json:"role"`
	Content []any         `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	Content []json.RawMessage `json:"content"`
	Usage   *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Completer.
func (c *Anthropic) Complete(ctx context.Context, req Request) (*Response, error) {
	if c.cfg.APIKey == "" {
		return nil, &HTTPError{Status: 401, Message: "missing API key", Retryable: false}
	}
	system, messages := toAnthropicMessages(req.Messages)
	body := anthropicRequest{
		Model:     firstNonEmpty(req.Model, c.cfg.Model),
		MaxTokens: 8192,
		System:    system,
		Messages:  messages,
		Tools:     toAnthropicTools(req.Tools),
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

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, &HTTPError{Status: 0, Message: "anthropic request failed", Retryable: true, Cause: err}
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode >= 400 {
		return nil, mapHTTPStatus(httpResp.StatusCode, string(respBody))
	}
	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	if parsed.Error != nil {
		return nil, &HTTPError{Status: httpResp.StatusCode, Message: parsed.Error.Message, Retryable: false}
	}
	msg, err := fromAnthropicContent(parsed.Content)
	if err != nil {
		return nil, err
	}
	out := &Response{Message: msg}
	if parsed.Usage != nil {
		out.Usage = &Usage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
			TotalTokens:  parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		}
	}
	return out, nil
}

func toAnthropicMessages(in []Message) (system string, out []anthropicMessage) {
	// Anthropic requires alternating user/assistant; tool results are user content blocks.
	var pendingToolResults []any
	flushTools := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		out = append(out, anthropicMessage{Role: "user", Content: pendingToolResults})
		pendingToolResults = nil
	}

	for _, m := range in {
		switch m.Role {
		case RoleSystem:
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		case RoleUser:
			flushTools()
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: anthropicUserContent(m),
			})
		case RoleAssistant:
			flushTools()
			blocks := make([]any, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				blocks = append(blocks, map[string]string{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				if len(tc.Args) > 0 {
					_ = json.Unmarshal(tc.Args, &input)
				}
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": input,
				})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]string{"type": "text", "text": ""})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
		case RoleTool:
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			})
		}
	}
	flushTools()
	return system, out
}

func anthropicUserContent(m Message) []any {
	if len(m.Parts) == 0 {
		return []any{map[string]string{"type": "text", "text": m.Content}}
	}
	blocks := make([]any, 0, len(m.Parts)+1)
	if m.Content != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": m.Content})
	}
	for _, p := range m.Parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				blocks = append(blocks, map[string]string{"type": "text", "text": p.Text})
			}
		case "image":
			mime := p.MIME
			if mime == "" {
				mime = "image/png"
			}
			data := p.Text
			if data == "" && len(p.Data) > 0 {
				data = encodeB64(p.Data)
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]string{
					"type":       "base64",
					"media_type": mime,
					"data":       data,
				},
			})
		}
	}
	if len(blocks) == 0 {
		return []any{map[string]string{"type": "text", "text": m.Content}}
	}
	return blocks
}

func fromAnthropicContent(blocks []json.RawMessage) (Message, error) {
	msg := Message{Role: RoleAssistant}
	for _, raw := range blocks {
		var meta struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return msg, err
		}
		switch meta.Type {
		case "text":
			var t struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &t); err != nil {
				return msg, err
			}
			msg.Content += t.Text
		case "tool_use":
			var t struct {
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(raw, &t); err != nil {
				return msg, err
			}
			args := t.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: t.ID, Name: t.Name, Args: args})
		}
	}
	return msg, nil
}

func toAnthropicTools(in []ToolDef) []anthropicTool {
	if len(in) == 0 {
		return nil
	}
	out := make([]anthropicTool, 0, len(in))
	for _, t := range in {
		schema := t.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out
}
