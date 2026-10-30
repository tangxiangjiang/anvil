// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"fmt"

	"github.com/anvil-dev/anvil/internal/llm"
)

// Validate checks the profile itself, independently of a selected provider or
// request. The zero value is valid compatibility mode.
func (c ModelCapabilities) Validate() error {
	if !c.Configured() {
		return nil
	}
	switch c.Thinking {
	case ThinkingNone, ThinkingOpenAIEffort, ThinkingAnthropicBudget, ThinkingAnthropicAdaptive:
	case ThinkingUnspecified:
		return fmt.Errorf("model capabilities: thinking is required for an explicit profile")
	default:
		return fmt.Errorf("model capabilities: unknown thinking protocol %q", c.Thinking)
	}
	seen := map[Effort]bool{}
	for _, effort := range c.EffortLevels {
		switch effort {
		case EffortLow, EffortMedium, EffortHigh:
		default:
			return fmt.Errorf("model capabilities: invalid effort level %q", effort)
		}
		if seen[effort] {
			return fmt.Errorf("model capabilities: duplicate effort level %q", effort)
		}
		seen[effort] = true
	}
	if c.Thinking == ThinkingNone && len(c.EffortLevels) > 0 {
		return fmt.Errorf("model capabilities: thinking %q conflicts with effort_levels", c.Thinking)
	}
	return nil
}

func validateModelSelection(
	provider Provider,
	c ModelCapabilities,
	effort Effort,
	maxOutputTokens int,
) error {
	if maxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens must be positive or zero")
	}
	if err := c.Validate(); err != nil || !c.Configured() {
		return err
	}
	switch provider {
	case ProviderOpenAI:
		if c.Thinking == ThinkingAnthropicBudget || c.Thinking == ThinkingAnthropicAdaptive {
			return fmt.Errorf("model capabilities: thinking %q conflicts with provider %q", c.Thinking, provider)
		}
	case ProviderAnthropic:
		if c.Thinking == ThinkingOpenAIEffort {
			return fmt.Errorf("model capabilities: thinking %q conflicts with provider %q", c.Thinking, provider)
		}
	}
	if effort != EffortDefault {
		if c.Thinking == ThinkingNone {
			return fmt.Errorf("model capabilities: thinking %q conflicts with effort %q", c.Thinking, effort)
		}
		supported := false
		for _, level := range c.EffortLevels {
			if level == effort {
				supported = true
				break
			}
		}
		if !supported {
			return fmt.Errorf("model capabilities: effort %q is not in effort_levels", effort)
		}
	}
	if maxOutputTokens > 0 && !c.MaxOutputTokens {
		return fmt.Errorf("model capabilities: max_output_tokens conflicts with max_output_tokens=false")
	}
	return nil
}

// ValidateFor checks a profile against the selected provider and request
// controls. Containers may use it while loading configuration, before Create.
func (c ModelCapabilities) ValidateFor(
	provider Provider,
	effort Effort,
	maxOutputTokens int,
) error {
	return validateModelSelection(provider, c, effort, maxOutputTokens)
}

func requestCapabilities(c ModelCapabilities) *llm.ModelCapabilities {
	if !c.Configured() {
		return nil
	}
	levels := make([]llm.Effort, len(c.EffortLevels))
	for i, level := range c.EffortLevels {
		levels[i] = llm.Effort(level)
	}
	return &llm.ModelCapabilities{
		Thinking:        llm.ThinkingProtocol(c.Thinking),
		EffortLevels:    levels,
		MaxOutputTokens: c.MaxOutputTokens,
		Vision:          c.Vision,
	}
}
