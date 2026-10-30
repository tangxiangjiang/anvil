// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"testing"
)

func TestModelCapabilityProfilesAtCreate(t *testing.T) {
	allEfforts := []Effort{EffortLow, EffortMedium, EffortHigh}
	cases := []struct {
		name     string
		provider Provider
		caps     ModelCapabilities
		effort   Effort
		max      int
		wantErr  string
	}{
		{"absent-compatibility", ProviderOpenAI, ModelCapabilities{}, EffortHigh, 1000, ""},
		{"non-reasoning", ProviderOpenAI, ModelCapabilities{Thinking: ThinkingNone}, "", 0, ""},
		{"legacy-budget", ProviderAnthropic, ModelCapabilities{Thinking: ThinkingAnthropicBudget, EffortLevels: allEfforts, MaxOutputTokens: true}, EffortMedium, 1000, ""},
		{"adaptive", ProviderAnthropic, ModelCapabilities{Thinking: ThinkingAnthropicAdaptive, EffortLevels: allEfforts, MaxOutputTokens: true}, EffortHigh, 1000, ""},
		{"openai-effort", ProviderOpenAI, ModelCapabilities{Thinking: ThinkingOpenAIEffort, EffortLevels: allEfforts, MaxOutputTokens: true}, EffortLow, 1000, ""},
		{"none-effort-conflict", ProviderOpenAI, ModelCapabilities{Thinking: ThinkingNone}, EffortLow, 0, "conflicts with effort"},
		{"unsupported-effort", ProviderOpenAI, ModelCapabilities{Thinking: ThinkingOpenAIEffort, EffortLevels: []Effort{EffortLow}}, EffortHigh, 0, "not in effort_levels"},
		{"output-conflict", ProviderOpenAI, ModelCapabilities{Thinking: ThinkingOpenAIEffort}, "", 1000, "max_output_tokens=false"},
		{"provider-conflict", ProviderOpenAI, ModelCapabilities{Thinking: ThinkingAnthropicAdaptive}, "", 0, "conflicts with provider"},
		{"duplicate-level", ProviderOpenAI, ModelCapabilities{Thinking: ThinkingOpenAIEffort, EffortLevels: []Effort{EffortLow, EffortLow}}, "", 0, "duplicate effort level"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			agent, err := Create(context.Background(), Options{
				Provider: testCase.provider,
				APIKey:   "test",
				Model: Model{
					ID:           "configured-model",
					Capabilities: testCase.caps,
				},
				Effort:          testCase.effort,
				MaxOutputTokens: testCase.max,
				Local:           LocalOptions{CWD: t.TempDir()},
			})
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("error=%v want %q", err, testCase.wantErr)
				}
				if agent != nil {
					agent.Close()
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			agent.Close()
		})
	}
}

func TestComplexStageCapabilityInheritance(t *testing.T) {
	allEfforts := []Effort{EffortLow, EffortMedium, EffortHigh}
	session := ModelCapabilities{
		Thinking:        ThinkingAnthropicAdaptive,
		EffortLevels:    allEfforts,
		MaxOutputTokens: true,
	}
	base := Options{
		Provider: ProviderAnthropic,
		APIKey:   "test",
		Model:    Model{ID: "session-model", Capabilities: session},
		Mode:     ModeComplex,
		Subagents: SubagentOptions{
			Enabled: true,
		},
		Local: LocalOptions{CWD: t.TempDir()},
	}
	agent, err := Create(context.Background(), base)
	if err != nil {
		t.Fatalf("empty stage model should inherit session capabilities: %v", err)
	}
	agent.Close()

	different := base
	different.Local.CWD = t.TempDir()
	different.Complex.Worker.Model = "other-model"
	agent, err = Create(context.Background(), different)
	if err != nil {
		t.Fatalf("different model without profile should remain unspecified: %v", err)
	}
	agent.Close()

	invalid := base
	invalid.Local.CWD = t.TempDir()
	invalid.Complex.Worker.Capabilities = ModelCapabilities{Thinking: ThinkingAnthropicAdaptive}
	if agent, err = Create(context.Background(), invalid); err == nil ||
		!strings.Contains(err.Error(), "capabilities require a stage model") {
		if agent != nil {
			agent.Close()
		}
		t.Fatalf("stage capabilities without stage model: %v", err)
	}
}
