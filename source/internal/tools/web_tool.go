// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/websearch"
)

// WebConfig configures optional web_search / fetch_url tools.
type WebConfig struct {
	Provider         websearch.Provider
	AllowFetchURL    bool
	AllowPrivateURLs bool
	FetchMaxBytes    int
}

// SetWebConfig installs web tools when Provider is non-nil.
func (r *Registry) SetWebConfig(cfg WebConfig) {
	if r == nil {
		return
	}
	r.Unregister("web_search")
	r.Unregister("fetch_url")
	r.webCfg = cfg
	if cfg.Provider == nil {
		return
	}
	r.register(&webSearchTool{r: r})
	if cfg.AllowFetchURL {
		r.register(&fetchURLTool{r: r})
	}
}

type webSearchTool struct{ r *Registry }

func (t *webSearchTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "web_search",
		Description: "Search the public web via the configured provider. " +
			"Returns titled URLs and snippets (not local codebase search — use semantic_search/grep for that).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"},
				"max_results":{"type":"integer","description":"default 5, max 10"},
				"freshness":{"type":"string","description":"optional: day|week|month|any"}
			},
			"required":["query"]
		}`),
	}
}

func (t *webSearchTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		Freshness  string `json:"freshness"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: "invalid args: " + err.Error(), IsError: true}
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		return Result{Content: "query is required", IsError: true}
	}
	if t.r.webCfg.Provider == nil {
		return Result{Content: "web_search is not configured", IsError: true}
	}
	hits, err := t.r.webCfg.Provider.Search(ctx, in.Query, websearch.SearchOpts{
		MaxResults: in.MaxResults,
		Freshness:  in.Freshness,
	})
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: websearch.FormatHits(hits)}
}

type fetchURLTool struct{ r *Registry }

func (t *fetchURLTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "fetch_url",
		Description: "HTTP GET a public URL and return truncated readable text (SSRF-guarded; no file://).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"url":{"type":"string"},
				"max_bytes":{"type":"integer","description":"default 65536"}
			},
			"required":["url"]
		}`),
	}
}

func (t *fetchURLTool) Execute(ctx context.Context, args json.RawMessage) Result {
	var in struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: "invalid args: " + err.Error(), IsError: true}
	}
	max := in.MaxBytes
	if max <= 0 {
		max = t.r.webCfg.FetchMaxBytes
	}
	text, err := websearch.FetchURL(ctx, in.URL, websearch.FetchOpts{
		MaxBytes:         max,
		AllowPrivateURLs: t.r.webCfg.AllowPrivateURLs,
	})
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: text}
}
