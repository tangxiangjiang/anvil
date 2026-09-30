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
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
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

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete implements Completer.
func (c *OpenAI) Complete(ctx context.Context, req Request) (*Response, error) {
	if c.cfg.APIKey == "" {
		return nil, &HTTPError{Status: 401, Message: "missing API key", Retryable: false}
	}
	body := openAIRequest{
		Model:    firstNonEmpty(req.Model, c.cfg.Model),
		Messages: toOpenAIMessages(req.Messages),
		Tools:    toOpenAITools(req.Tools),
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

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, &HTTPError{Status: 0, Message: "openai request failed", Retryable: true, Cause: err}
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode >= 400 {
		return nil, mapHTTPStatus(httpResp.StatusCode, string(respBody))
	}
	var parsed openAIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	if parsed.Error != nil {
		return nil, &HTTPError{Status: httpResp.StatusCode, Message: parsed.Error.Message, Retryable: false}
	}
	if len(parsed.Choices) == 0 {
		return nil, &HTTPError{Status: httpResp.StatusCode, Message: "no choices in response", Retryable: false}
	}
	msg := fromOpenAIMessage(parsed.Choices[0].Message)
	out := &Response{Message: msg}
	if parsed.Usage != nil {
		out.Usage = &Usage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
			TotalTokens:  parsed.Usage.TotalTokens,
		}
	}
	return out, nil
}

func toOpenAIMessages(in []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(in))
	for _, m := range in {
		om := openAIMessage{
			Role:       string(m.Role),
			Content:    m.Content,
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

func fromOpenAIMessage(m openAIMessage) Message {
	msg := Message{
		Role:       Role(m.Role),
		Content:    m.Content,
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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
