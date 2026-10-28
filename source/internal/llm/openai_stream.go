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

// openAIStreamOptions asks for the final usage chunk. Without it a conforming
// server sends no usage at all in a streamed response, so every streamed run
// would report zero tokens. The field is optional in the protocol: a compatible
// server that does not know it ignores it and keeps streaming.
type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIStreamRequest struct {
	Model           string               `json:"model"`
	Messages        []openAIMessage      `json:"messages"`
	Tools           []openAITool         `json:"tools,omitempty"`
	ReasoningEffort Effort               `json:"reasoning_effort,omitempty"`
	Stream          bool                 `json:"stream"`
	StreamOptions   *openAIStreamOptions `json:"stream_options,omitempty"`
}

// CompleteStream implements StreamCompleter for OpenAI-compatible SSE.
func (c *OpenAI) CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	if c.cfg.APIKey == "" {
		return nil, &HTTPError{Status: 401, Message: "missing API key", Retryable: false}
	}
	if onDelta == nil {
		onDelta = func(Delta) {}
	}
	body := openAIStreamRequest{
		Model:           firstNonEmpty(req.Model, c.cfg.Model),
		Messages:        toOpenAIMessages(req.Messages),
		Tools:           toOpenAITools(req.Tools),
		ReasoningEffort: req.Effort,
		Stream:          true,
		StreamOptions:   &openAIStreamOptions{IncludeUsage: true},
	}
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
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, &HTTPError{Status: 0, Message: "openai stream request failed", Retryable: true, Cause: err}
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
		return nil, mapHTTPStatus(httpResp.StatusCode, string(respBody))
	}

	var content strings.Builder
	toolParts := map[int]*openAIToolCall{}
	var usage *Usage

	sc := bufio.NewScanner(httpResp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					// Reasoning carries the model's chain of thought on the
					// OpenAI-compatible providers that stream one in a separate
					// delta field: `reasoning_content` on DeepSeek, vLLM and LM
					// Studio, `reasoning` on OpenRouter. Naming the fields is the
					// whole decode: encoding/json drops unknown keys, so before
					// this they arrived on the socket and were discarded.
					//
					// Decode-only, and deliberately not mirrored onto
					// llm.Message: openAIMessage is shared with the request
					// encoder, so a field here that Message also carried would be
					// how private reasoning got echoed back upstream.
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *openAIUsageWire `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return nil, &HTTPError{Status: httpResp.StatusCode, Message: chunk.Error.Message, Retryable: false}
		}
		if chunk.Usage != nil {
			usage = chunk.Usage.usage()
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		// Reasoning is streamed, never accumulated into msg below: it is shown
		// once and has no replay, so there is nothing to keep it for.
		if r := firstNonEmpty(delta.ReasoningContent, delta.Reasoning); r != "" {
			onDelta(Delta{Reasoning: r})
		}
		if delta.Content != "" {
			content.WriteString(delta.Content)
			onDelta(Delta{Text: delta.Content})
		}
		for _, tc := range delta.ToolCalls {
			part, ok := toolParts[tc.Index]
			if !ok {
				part = &openAIToolCall{Type: "function"}
				toolParts[tc.Index] = part
			}
			if tc.ID != "" {
				part.ID = tc.ID
			}
			if tc.Type != "" {
				part.Type = tc.Type
			}
			if tc.Function.Name != "" {
				part.Function.Name = tc.Function.Name
			}
			part.Function.Arguments += tc.Function.Arguments
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	msg := Message{Role: RoleAssistant, Content: content.String()}
	for i := 0; ; i++ {
		part, ok := toolParts[i]
		if !ok {
			break
		}
		args := json.RawMessage(part.Function.Arguments)
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:   part.ID,
			Name: part.Function.Name,
			Args: args,
		})
	}
	return &Response{Message: msg, Usage: usage}, nil
}

// Ensure OpenAI implements StreamCompleter.
var _ StreamCompleter = (*OpenAI)(nil)
