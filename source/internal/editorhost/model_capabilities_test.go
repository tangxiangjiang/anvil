// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"strings"
	"testing"
)

func TestCapabilityWireRoundTrip(t *testing.T) {
	wire := &ModelCapabilitiesParams{
		Thinking:        "anthropic_adaptive",
		EffortLevels:    []string{"low", "medium", "high"},
		MaxOutputTokens: true,
		Vision:          true,
	}
	sdk := capabilitiesFromWire(wire)
	got := capabilitiesToWire(&sdk)
	if got == nil || got.Thinking != wire.Thinking ||
		strings.Join(got.EffortLevels, ",") != strings.Join(wire.EffortLevels, ",") ||
		!got.MaxOutputTokens || !got.Vision {
		t.Fatalf("round trip=%+v", got)
	}
	if capabilitiesToWire(nil) != nil || capabilitiesFromWire(nil).Configured() {
		t.Fatal("absent profile did not remain compatibility mode")
	}
}

func TestSessionCapabilitiesReachProvider(t *testing.T) {
	llm := newStubLLM(t, textTurn("done"))
	h, _ := initHost(t, llm.baseURL())
	session := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		Effort:          "high",
		MaxOutputTokens: 2048,
		ModelCapabilities: &ModelCapabilitiesParams{
			Thinking:        "openai_effort",
			EffortLevels:    []string{"low", "high"},
			MaxOutputTokens: true,
		},
	}))
	sessionID, _ := session["session_id"].(string)
	promptRun(t, h, sessionID, "profiled", autoApprove)
	body := llm.lastRequestBody(t)
	for _, field := range []string{`"reasoning_effort":"high"`, `"max_completion_tokens":2048`} {
		if !strings.Contains(body, field) {
			t.Fatalf("request missing %s:\n%s", field, body)
		}
	}
}

func TestInvalidSessionCapabilitiesFailBeforeProviderRequest(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())
	message := h.call(MethodSessionNew, SessionParams{
		Effort: "high",
		ModelCapabilities: &ModelCapabilitiesParams{
			Thinking:     "none",
			EffortLevels: []string{"high"},
		},
	})
	err, ok := rpcErrorOf(message)
	if !ok || !strings.Contains(err.Message, "conflicts with effort_levels") {
		t.Fatalf("invalid profile response=%v", message)
	}
	if llm.callCount() != 0 {
		t.Fatalf("provider requests=%d", llm.callCount())
	}
}
