// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package websearch provides HTTP web search providers for the web_search tool (0.15).
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/redact"
)

const (
	defaultTimeout    = 15 * time.Second
	defaultMaxResults = 5
	hardMaxResults    = 10
	maxSnippetRunes   = 400
)

// Hit is one search result.
type Hit struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	PublishedAt string `json:"published_at,omitempty"`
}

// SearchOpts are per-call options.
type SearchOpts struct {
	MaxResults int
	Freshness  string // day|week|month|any (provider hint)
}

// Provider executes a web search.
type Provider interface {
	Search(ctx context.Context, query string, opts SearchOpts) ([]Hit, error)
}

// HTTPConfig configures the generic JSON HTTP adapter.
//
// Wire shape (documented for operators / gateway proxies):
//
//	POST {BaseURL}  Authorization: Bearer <key>
//	{"query":"...","max_results":5,"freshness":"week"}
//	→ {"results":[{"title":"...","url":"...","snippet":"...","published_at":"..."}]}
//
// GET is used when Method is GET: query/max_results/freshness as query params;
// response body uses the same JSON shape.
type HTTPConfig struct {
	BaseURL    string
	APIKey     string // resolved; never log
	APIKeyEnv  string // if APIKey empty, read from this env
	Method     string // POST (default) or GET
	Timeout    time.Duration
	MaxResults int
	HTTPClient *http.Client // optional; tests inject
}

// NewHTTP builds a Provider. BaseURL is required.
func NewHTTP(cfg HTTPConfig) (*HTTPProvider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("web search BaseURL is required")
	}
	key := cfg.APIKey
	if key == "" && cfg.APIKeyEnv != "" {
		key = os.Getenv(cfg.APIKeyEnv)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	max := cfg.MaxResults
	if max <= 0 {
		max = defaultMaxResults
	}
	if max > hardMaxResults {
		max = hardMaxResults
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &HTTPProvider{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     key,
		method:     method,
		timeout:    timeout,
		maxResults: max,
		client:     client,
	}, nil
}

// HTTPProvider calls a JSON search endpoint.
type HTTPProvider struct {
	baseURL    string
	apiKey     string
	method     string
	timeout    time.Duration
	maxResults int
	client     *http.Client
}

type searchReq struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
	Freshness  string `json:"freshness,omitempty"`
}

type searchResp struct {
	Results []Hit `json:"results"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Search implements Provider.
func (p *HTTPProvider) Search(ctx context.Context, query string, opts SearchOpts) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	max := opts.MaxResults
	if max <= 0 {
		max = p.maxResults
	}
	if max > hardMaxResults {
		max = hardMaxResults
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var req *http.Request
	var err error
	if p.method == http.MethodGet {
		u, perr := url.Parse(p.baseURL)
		if perr != nil {
			return nil, perr
		}
		q := u.Query()
		q.Set("query", query)
		q.Set("max_results", fmt.Sprintf("%d", max))
		if opts.Freshness != "" {
			q.Set("freshness", opts.Freshness)
		}
		u.RawQuery = q.Encode()
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	} else {
		body, _ := json.Marshal(searchReq{Query: query, MaxResults: max, Freshness: opts.Freshness})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return nil, err
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("web search auth failed (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("web search HTTP %d", resp.StatusCode)
	}
	var out searchResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("web search decode: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("web search: %s", out.Error.Message)
	}
	hits := out.Results
	if len(hits) > max {
		hits = hits[:max]
	}
	for i := range hits {
		hits[i].Title = redact.String(hits[i].Title)
		hits[i].Snippet = trimRunes(redact.String(hits[i].Snippet), maxSnippetRunes)
		hits[i].URL = strings.TrimSpace(hits[i].URL)
	}
	return hits, nil
}

func trimRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// FormatHits renders hits for the model.
func FormatHits(hits []Hit) string {
	if len(hits) == 0 {
		return "(no results)"
	}
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, h.Title, h.URL, h.Snippet)
		if h.PublishedAt != "" {
			fmt.Fprintf(&b, "   published: %s\n", h.PublishedAt)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}
