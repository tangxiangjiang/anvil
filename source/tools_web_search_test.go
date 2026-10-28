// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
)

func TestWebSearchDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range a.registry.Names() {
		if name == "web_search" || name == "fetch_url" {
			t.Fatalf("web tools must be absent by default, got %s", name)
		}
	}
}

func TestWebSearchToolWithFakeProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{{
				"title": "Example", "url": "https://example.com", "snippet": "Example Domain",
			}},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		WebSearch: WebSearchOptions{
			Enabled: true,
			BaseURL: srv.URL,
			APIKey:  "tok",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	res := a.registry.Execute(context.Background(), "web_search", mustJSON(map[string]any{
		"query": "example", "max_results": 3,
	}))
	if res.IsError || !strings.Contains(res.Content, "example.com") {
		t.Fatalf("%+v", res)
	}
	if toolPresent(a, "fetch_url") {
		t.Fatal("fetch_url should be off unless AllowFetchURL")
	}
}

func TestWebSearchDisallowedGroup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:           LocalOptions{CWD: dir},
		WebSearch:       WebSearchOptions{Enabled: true, BaseURL: srv.URL, AllowFetchURL: true, AllowPrivateURLs: true},
		DisallowedTools: []string{"web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if toolPresent(a, "web_search") || toolPresent(a, "fetch_url") {
		t.Fatal("web group should be stripped")
	}
}

func TestWebSearchInLoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{{"title": "Hit", "url": "https://ex.test", "snippet": "ok"}},
		})
	}))
	defer srv.Close()
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:     LocalOptions{CWD: dir},
		WebSearch: WebSearchOptions{Enabled: true, BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	var saw bool
	res := runtime.Loop(context.Background(), &webScript{}, a.registry, runtime.Config{
		CWD: dir, Model: "m", MaxSteps: 8, UserMsg: "search",
	}, func(ev runtime.Event) {
		if ev.Type == "tool_call" && ev.Name == "web_search" {
			saw = true
		}
	})
	if res.Status != "finished" || !saw {
		t.Fatalf("status=%s saw=%v err=%s", res.Status, saw, res.Err)
	}
}

type webScript struct{ n int }

func (s *webScript) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	s.n++
	if s.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID: "1", Name: "web_search", Args: json.RawMessage(`{"query":"x"}`),
			}},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
}

func TestFetchURLTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>FETCH_OK_42</body></html>`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		WebSearch: WebSearchOptions{
			Enabled: true, BaseURL: "http://127.0.0.1:9/unused", // provider required for enable path
			APIKey: "x", AllowFetchURL: true, AllowPrivateURLs: true,
			Timeout: 2 * time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res := a.registry.Execute(context.Background(), "fetch_url", mustJSON(map[string]any{"url": srv.URL}))
	if res.IsError || !strings.Contains(res.Content, "FETCH_OK_42") {
		t.Fatalf("%+v", res)
	}
	blocked := a.registry.Execute(context.Background(), "fetch_url", mustJSON(map[string]any{
		"url": "http://127.0.0.1/",
	}))
	// AllowPrivateURLs true — 127.0.0.1 allowed. Create second agent without it.
	a2, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		WebSearch: WebSearchOptions{
			Enabled: true, BaseURL: srv.URL, AllowFetchURL: true, AllowPrivateURLs: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	blocked = a2.registry.Execute(context.Background(), "fetch_url", mustJSON(map[string]any{
		"url": "http://10.1.2.3/",
	}))
	if !blocked.IsError || !strings.Contains(blocked.Content, "SSRF") {
		t.Fatalf("%+v", blocked)
	}
}

func toolPresent(a *Agent, name string) bool {
	for _, n := range a.registry.Names() {
		if n == name {
			return true
		}
	}
	return false
}
