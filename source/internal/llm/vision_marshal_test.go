// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestOpenAIImageURLParts(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer srv.Close()

	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	_, err := c.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: "what is this?",
			Parts: []llm.ContentPart{{
				Type:   "image",
				MIME:   "image/png",
				Data:   []byte{0x89, 0x50, 0x4e, 0x47},
				Detail: "low",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"type":"image_url"`) {
		t.Fatalf("missing image_url: %s", body)
	}
	if !strings.Contains(string(body), "data:image/png;base64,") {
		t.Fatalf("missing data url: %s", body)
	}
	if !strings.Contains(string(body), `"detail":"low"`) {
		t.Fatalf("missing detail: %s", body)
	}
}

func TestAnthropicImageSourceParts(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "claude"})
	_, err := c.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: "describe",
			Parts: []llm.ContentPart{{
				Type: "image",
				MIME: "image/png",
				Data: []byte{0x89, 0x50, 0x4e, 0x47},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"type":"image"`) {
		t.Fatalf("missing image block: %s", body)
	}
	if !strings.Contains(string(body), `"media_type":"image/png"`) {
		t.Fatalf("missing media_type: %s", body)
	}
	if !strings.Contains(string(body), `"type":"base64"`) {
		t.Fatalf("missing base64 source: %s", body)
	}
}
