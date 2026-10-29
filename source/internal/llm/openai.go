// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultOpenAIBase = "https://api.openai.com/v1"

// OpenAI is an OpenAI-compatible Chat Completions client.
type OpenAI struct {
	cfg    Config
	client *http.Client
}

// NewOpenAI builds an OpenAI-compatible client.
func NewOpenAI(cfg Config) *OpenAI {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultOpenAIBase
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &OpenAI{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
	// ReasoningEffort is a request parameter (a level word), not reasoning text.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// MaxCompletionTokens caps the answer; omitted when zero.
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"` // string or []parts
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// openAIUsageWire is the usage object as OpenAI-compatible providers send it.
// The cache shapes differ by provider and both are named: OpenAI reports input
// tokens served from its own cache in prompt_tokens_details.cached_tokens,
// while DeepSeek reports a prompt_cache_hit_tokens count for its disk cache.
// Decoding both means a proxy or a compatible endpoint is accounted for without
// a provider table.
type openAIUsageWire struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
}

// usage projects the wire shape. The total stays the provider's own: OpenAI's
// cached_tokens is a subset of prompt_tokens, so adding it would double-count.
func (u *openAIUsageWire) usage() *Usage {
	if u == nil {
		return nil
	}
	cached := u.PromptCacheHitTokens
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		cached = u.PromptTokensDetails.CachedTokens
	}
	return &Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
		CachedTokens: cached,
	}
}

type openAIResponse struct {
	Choices []struct {
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsageWire `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete implements Completer.
func (c *OpenAI) Complete(ctx context.Context, req Request) (*Response, error) {
	if c.cfg.APIKey == "" {
		return nil, NewHTTPError(401, "missing API key")
	}
	body := openAIRequest{
		Model:           firstNonEmpty(req.Model, c.cfg.Model),
		Messages:        toOpenAIMessages(req.Messages),
		Tools:           toOpenAITools(req.Tools),
		ReasoningEffort: req.Effort,
	}
	body.ReasoningEffort = string(req.Effort)
	body.MaxCompletionTokens = req.MaxOutputTokens
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := c.cfg.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, newTransportError("openai request failed", err)
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode >= 400 {
		return nil, withRetryAfter(mapHTTPStatus(httpResp.StatusCode, string(respBody)), httpResp.Header)
	}
	var parsed openAIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	if parsed.Error != nil {
		return nil, NewHTTPError(httpResp.StatusCode, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, NewHTTPError(httpResp.StatusCode, "no choices in response")
	}
	msg := fromOpenAIMessage(parsed.Choices[0].Message)
	out := &Response{Message: msg, Usage: parsed.Usage.usage(), StopReason: openAIStop(parsed.Choices[0].FinishReason)}
	return out, nil
}

func toOpenAIMessages(in []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(in))
	for _, m := range in {
		om := openAIMessage{
			Role:       string(m.Role),
			Content:    openAIContent(m),
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		for _, tc := range m.ToolCalls {
			otc := openAIToolCall{ID: tc.ID, Type: "function"}
			otc.Function.Name = tc.Name
			otc.Function.Arguments = string(tc.Args)
			if otc.Function.Arguments == "" {
				otc.Function.Arguments = "{}"
			}
			om.ToolCalls = append(om.ToolCalls, otc)
		}
		out = append(out, om)
	}
	return out
}

func openAIContent(m Message) any {
	if len(m.Parts) == 0 {
		return m.Content
	}
	parts := make([]map[string]any, 0, len(m.Parts)+1)
	if m.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": m.Content})
	}
	for _, p := range m.Parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": p.Text})
			}
		case "image":
			url := p.Text // may already be data URL
			if url == "" && len(p.Data) > 0 {
				url = "data:" + p.MIME + ";base64," + encodeB64(p.Data)
			}
			img := map[string]any{"url": url}
			if p.Detail != "" {
				img["detail"] = p.Detail
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": img})
		}
	}
	if len(parts) == 0 {
		return m.Content
	}
	return parts
}

func fromOpenAIMessage(m openAIMessage) Message {
	msg := Message{
		Role:       Role(m.Role),
		Content:    openAIContentString(m.Content),
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: json.RawMessage(tc.Function.Arguments),
		})
	}
	return msg
}

func toOpenAITools(in []ToolDef) []openAITool {
	if len(in) == 0 {
		return nil
	}
	out := make([]openAITool, 0, len(in))
	for _, t := range in {
		ot := openAITool{Type: "function"}
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.Parameters
		if len(ot.Function.Parameters) == 0 {
			ot.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, ot)
	}
	return out
}

func encodeB64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func openAIContentString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		// Multimodal responses rarely appear; stringify best-effort.
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
