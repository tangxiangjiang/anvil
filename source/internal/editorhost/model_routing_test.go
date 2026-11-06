// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil"
)

func TestModelRoutesResolveProviderCredentialsWithoutCrossingRPC(t *testing.T) {
	t.Setenv("ANVIL_API_KEY", "default-key")
	t.Setenv("ANVIL_OPENAI_API_KEY", "openai-key")
	t.Setenv("ANVIL_ANTHROPIC_API_KEY", "anthropic-key")

	dir := newWorkspace(t)
	srv := New(Config{
		CWD:      dir,
		Model:    "main",
		Provider: string(anvil.ProviderAnthropic),
		BaseURL:  "https://api.anthropic.com",
	}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-routes", dir, ModeAsk, false)
	opts, rpcErr := srv.buildOptions(context.Background(), SessionParams{
		ModelRoutes: map[string]ModelRouteParams{
			"gpt-worker": {
				Provider:   "openai",
				BaseURL:    "https://gateway.example/v1/",
				Credential: "openai",
				Capabilities: &ModelCapabilitiesParams{
					Thinking: "openai_effort",
				},
			},
			"claude-reviewer": {
				Provider:   "anthropic",
				BaseURL:    "https://api.anthropic.com/",
				Credential: "anthropic",
			},
			"default-worker": {
				Provider:   "openai",
				BaseURL:    "https://default.example/v1",
				Credential: "default",
			},
		},
	}, sess)
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}

	cases := map[string]struct {
		provider anvil.Provider
		baseURL  string
		key      string
	}{
		"gpt-worker":      {anvil.ProviderOpenAI, "https://gateway.example/v1", "openai-key"},
		"claude-reviewer": {anvil.ProviderAnthropic, "https://api.anthropic.com", "anthropic-key"},
		"default-worker":  {anvil.ProviderOpenAI, "https://default.example/v1", "default-key"},
	}
	for id, want := range cases {
		got := opts.ModelRoutes[id]
		if got.Provider != want.provider || got.BaseURL != want.baseURL || got.APIKey != want.key {
			t.Fatalf("%s route=%+v", id, got)
		}
	}
	if opts.ModelRoutes["gpt-worker"].Capabilities.Thinking != anvil.ThinkingOpenAIEffort {
		t.Fatalf("capabilities=%+v", opts.ModelRoutes["gpt-worker"].Capabilities)
	}
	main := opts.ModelRoutes["main"]
	if main.Provider != anvil.ProviderAnthropic || main.BaseURL != "https://api.anthropic.com" ||
		main.APIKey != "default-key" {
		t.Fatalf("main return route=%+v", main)
	}
}

func TestModelRoutesRejectMalformedWireValues(t *testing.T) {
	t.Setenv("ANVIL_OPENAI_API_KEY", "")
	t.Setenv("ANVIL_ANTHROPIC_API_KEY", "")
	tests := []struct {
		name  string
		route map[string]ModelRouteParams
		want  string
	}{
		{"blank id", map[string]ModelRouteParams{" ": {Provider: "openai", BaseURL: "https://example.test"}}, "model id"},
		{"provider", map[string]ModelRouteParams{"worker": {Provider: "future", BaseURL: "https://example.test"}}, "provider"},
		{"base url", map[string]ModelRouteParams{"worker": {Provider: "openai"}}, "base_url"},
		{"relative base url", map[string]ModelRouteParams{"worker": {Provider: "openai", BaseURL: "/v1"}}, "absolute HTTP"},
		{"duplicate trimmed id", map[string]ModelRouteParams{
			"worker":  {Provider: "openai", BaseURL: "https://example.test"},
			" worker": {Provider: "openai", BaseURL: "https://example.test"},
		}, "duplicate model id"},
		{"missing provider key", map[string]ModelRouteParams{
			"worker": {Provider: "openai", BaseURL: "https://example.test", Credential: "openai"},
		}, "route key is not configured"},
		{"credential", map[string]ModelRouteParams{"worker": {Provider: "openai", BaseURL: "https://example.test", Credential: "named"}}, "credential"},
		{"capability protocol", map[string]ModelRouteParams{
			"worker": {
				Provider: "openai",
				BaseURL:  "https://example.test",
				Capabilities: &ModelCapabilitiesParams{
					Thinking: "anthropic_adaptive",
				},
			},
		}, "conflicts with provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, rpcErr := modelRoutesFromWire(tt.route)
			if rpcErr == nil || !strings.Contains(rpcErr.Message, tt.want) {
				t.Fatalf("error=%v, want %q", rpcErr, tt.want)
			}
		})
	}
}
