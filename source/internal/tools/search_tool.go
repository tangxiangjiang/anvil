// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anvil-dev/anvil/internal/index"
	"github.com/anvil-dev/anvil/internal/llm"
)

// IndexEmbed configures optional query embedding for semantic_search hybrid mode.
type IndexEmbed struct {
	BaseURL string
	APIKey  string
	Model   string
}

// SetIndexEmbed installs embed settings used when vectors exist in the project index.
func (r *Registry) SetIndexEmbed(cfg IndexEmbed) {
	if r == nil {
		return
	}
	r.indexEmbed = cfg
}

type semanticSearchTool struct{ r *Registry }

func (t *semanticSearchTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "semantic_search",
		Description: "Search the project index for code/docs relevant to a natural-language or symbol query. " +
			"Uses local FTS (BM25-ish); hybrid with embeddings when the index was built with vectors. " +
			"Run `anvil index build` if results are empty or stale.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"query":{"type":"string","description":"Search query (symbols, phrases, or natural language)"},
				"path_glob":{"type":"string","description":"Optional filepath glob, e.g. **/*.go or src/*"},
				"top_k":{"type":"integer","description":"Max hits (default 8, max 32)"}
			},
			"required":["query"]
		}`),
	}
}

func (t *semanticSearchTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		Query    string `json:"query"`
		PathGlob string `json:"path_glob"`
		TopK     int    `json:"top_k"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: "invalid args: " + err.Error(), IsError: true}
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		return Result{Content: "query is required", IsError: true}
	}

	if !index.Exists(t.r.cwd) {
		return Result{Content: "no project index yet; run: anvil index build <cwd>", IsError: true}
	}

	opts := index.BuildOptions{
		EmbedURL:    t.r.indexEmbed.BaseURL,
		EmbedAPIKey: t.r.indexEmbed.APIKey,
		EmbedModel:  t.r.indexEmbed.Model,
	}
	if st, err := index.Stat(t.r.cwd); err == nil {
		if opts.EmbedModel == "" {
			opts.EmbedModel = st.EmbedModel
		}
		if opts.EmbedURL == "" {
			opts.EmbedURL = st.EmbedURL
		}
	}

	hits, err := index.SearchWithEmbed(ctx, t.r.cwd, index.Query{
		Text:     in.Query,
		PathGlob: in.PathGlob,
		TopK:     in.TopK,
		Mode:     "auto",
	}, opts)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if len(hits) == 0 {
		return Result{Content: "no matches (try a different query, rebuild index, or drop path_glob)"}
	}

	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "%d. %s:%d-%d score=%.4f [%s]\n%s\n\n",
			i+1, h.Path, h.StartLine, h.EndLine, h.Score, h.Source, h.Excerpt)
	}
	return Result{Content: strings.TrimSpace(b.String())}
}
