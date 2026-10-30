// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func profile(thinking llm.ThinkingProtocol, efforts ...llm.Effort) *llm.ModelCapabilities {
	return &llm.ModelCapabilities{
		Thinking:        thinking,
		EffortLevels:    efforts,
		MaxOutputTokens: true,
	}
}

func TestAnthropicCapabilityRequestShapes(t *testing.T) {
	for _, tc := range wireCases() {
		if !tc.anthropic {
			continue
		}
		t.Run(tc.name+"/legacy-budget", func(t *testing.T) {
			req := goldenHistory()
			req.Effort = llm.EffortMedium
			req.Capabilities = profile(llm.ThinkingAnthropicBudget, llm.EffortMedium)
			body := sentBody(t, tc, req)
			if string(body["thinking"]) != `{"type":"enabled","budget_tokens":4096}` {
				t.Fatalf("thinking=%s", body["thinking"])
			}
			if _, ok := body["output_config"]; ok {
				t.Fatalf("legacy request gained output_config: %s", body["output_config"])
			}
			if string(body["max_tokens"]) != "8192" {
				t.Fatalf("max_tokens=%s", body["max_tokens"])
			}
		})

		t.Run(tc.name+"/adaptive", func(t *testing.T) {
			req := goldenHistory()
			req.Effort = llm.EffortMedium
			req.MaxOutputTokens = 2048
			req.Capabilities = profile(llm.ThinkingAnthropicAdaptive, llm.EffortMedium)
			body := sentBody(t, tc, req)
			if string(body["thinking"]) != `{"type":"adaptive"}` {
				t.Fatalf("thinking=%s", body["thinking"])
			}
			if string(body["output_config"]) != `{"effort":"medium"}` {
				t.Fatalf("output_config=%s", body["output_config"])
			}
			if strings.Contains(string(body["thinking"]), "budget_tokens") {
				t.Fatalf("adaptive request gained a budget: %s", body["thinking"])
			}
			if string(body["max_tokens"]) != "2048" {
				t.Fatalf("max_tokens=%s", body["max_tokens"])
			}
		})

		t.Run(tc.name+"/adaptive-default-effort", func(t *testing.T) {
			req := goldenHistory()
			req.Capabilities = profile(llm.ThinkingAnthropicAdaptive)
			body := sentBody(t, tc, req)
			if string(body["thinking"]) != `{"type":"adaptive"}` {
				t.Fatalf("thinking=%s", body["thinking"])
			}
			if _, ok := body["output_config"]; ok {
				t.Fatalf("default effort sent output_config: %s", body["output_config"])
			}
		})
	}
}

func TestOpenAICapabilityRequestShape(t *testing.T) {
	for _, tc := range wireCases() {
		if tc.anthropic {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			req := goldenHistory()
			req.Effort = llm.EffortHigh
			req.MaxOutputTokens = 3000
			req.Capabilities = profile(llm.ThinkingOpenAIEffort, llm.EffortHigh)
			body := sentBody(t, tc, req)
			if string(body["reasoning_effort"]) != `"high"` {
				t.Fatalf("reasoning_effort=%s", body["reasoning_effort"])
			}
			if string(body["max_completion_tokens"]) != "3000" {
				t.Fatalf("max_completion_tokens=%s", body["max_completion_tokens"])
			}
			if _, ok := body["thinking"]; ok {
				t.Fatal("OpenAI request gained Anthropic thinking")
			}
		})
	}
}

func TestInvalidCapabilityCombinationsSendNoRequest(t *testing.T) {
	cases := []struct {
		name      string
		anthropic bool
		caps      *llm.ModelCapabilities
		effort    llm.Effort
		max       int
		want      string
	}{
		{"missing-thinking", false, &llm.ModelCapabilities{MaxOutputTokens: true}, "", 0, "thinking is required"},
		{"unknown-thinking", false, profile("future"), "", 0, "unknown thinking protocol"},
		{"duplicate-effort", false, profile(llm.ThinkingOpenAIEffort, llm.EffortLow, llm.EffortLow), "", 0, "duplicate effort level"},
		{"none-with-levels", false, profile(llm.ThinkingNone, llm.EffortLow), "", 0, "conflicts with effort_levels"},
		{"unsupported-effort", false, profile(llm.ThinkingOpenAIEffort, llm.EffortLow), llm.EffortHigh, 0, "not in effort_levels"},
		{"none-with-effort", false, profile(llm.ThinkingNone), llm.EffortLow, 0, "conflicts with effort"},
		{"output-disabled", false, &llm.ModelCapabilities{Thinking: llm.ThinkingOpenAIEffort}, "", 10, "max_output_tokens=false"},
		{"negative-output", false, nil, "", -1, "must be positive or zero"},
		{"anthropic-on-openai", false, profile(llm.ThinkingAnthropicAdaptive), "", 0, "conflicts with OpenAI"},
		{"openai-on-anthropic", true, profile(llm.ThinkingOpenAIEffort), "", 0, "conflicts with Anthropic"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var chosen wireCase
			for _, candidate := range wireCases() {
				if candidate.anthropic == testCase.anthropic && !strings.Contains(candidate.name, "stream") {
					chosen = candidate
					break
				}
			}
			srv, body := captureServer(t)
			req := goldenHistory()
			req.Capabilities = testCase.caps
			req.Effort = testCase.effort
			req.MaxOutputTokens = testCase.max
			err := chosen.call(srv, req)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error=%v want %q", err, testCase.want)
			}
			if raw := body(); len(raw) != 0 {
				var formatted any
				_ = json.Unmarshal(raw, &formatted)
				t.Fatalf("invalid profile reached provider: %v", formatted)
			}
		})
	}
}
