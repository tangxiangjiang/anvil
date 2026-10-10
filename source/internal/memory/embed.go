// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package memory

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

// EmbedConfig is optional OpenAI-compatible embeddings for memory_search/write.
type EmbedConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Configured reports whether vector path can run.
func (c EmbedConfig) Configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.Model) != ""
}

type embedClient struct {
	url    string
	apiKey string
	model  string
	http   *http.Client
}

func newEmbedClient(cfg EmbedConfig) *embedClient {
	url := strings.TrimRight(cfg.BaseURL, "/")
	if !strings.HasSuffix(url, "/embeddings") {
		url = url + "/embeddings"
	}
	return &embedClient{
		url:    url,
		apiKey: cfg.APIKey,
		model:  cfg.Model,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *embedClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c == nil || len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{"model": c.model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			continue
		}
		v := make([]float32, len(d.Embedding))
		for i, f := range d.Embedding {
			v[i] = float32(f)
		}
		vecs[d.Index] = v
	}
	return vecs, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// MaybeEmbed returns a vector when cfg is set; failures are ignored (lexical still works).
func MaybeEmbed(ctx context.Context, cfg EmbedConfig, text string) []float32 {
	if !cfg.Configured() || text == "" {
		return nil
	}
	vecs, err := newEmbedClient(cfg).Embed(ctx, []string{text})
	if err != nil || len(vecs) == 0 {
		return nil
	}
	return vecs[0]
}
