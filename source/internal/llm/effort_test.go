// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"encoding/json"
	"testing"
)

func TestOpenAIEffortMarshalling(t *testing.T) {
	raw, err := json.Marshal(openAIRequest{Model: "m", ReasoningEffort: EffortMedium})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["reasoning_effort"] != "medium" {
		t.Fatalf("reasoning_effort=%v body=%s", body["reasoning_effort"], raw)
	}

	raw, err = json.Marshal(openAIRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"model":"m","messages":null}` {
		t.Fatalf("default effort was not omitted: %s", raw)
	}
}

func TestAnthropicEffortBudgets(t *testing.T) {
	tests := []struct {
		effort Effort
		budget int
		max    int
	}{
		{EffortLow, 1024, 8192},
		{EffortMedium, 4096, 8192},
		{EffortHigh, 16384, 24576},
	}
	for _, tt := range tests {
		thinking, maxTokens := anthropicThinkingFor(tt.effort)
		if thinking == nil || thinking.BudgetTokens != tt.budget || maxTokens != tt.max {
			t.Fatalf("%s: thinking=%+v max=%d", tt.effort, thinking, maxTokens)
		}
	}
	if thinking, maxTokens := anthropicThinkingFor(EffortDefault); thinking != nil || maxTokens != 8192 {
		t.Fatalf("default: thinking=%+v max=%d", thinking, maxTokens)
	}
}

func TestAnthropicThinkingStateRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"type":"thinking","thinking":"private","signature":"signed"}`)
	msg, err := fromAnthropicContent([]json.RawMessage{
		raw,
		json.RawMessage(`{"type":"text","text":"answer"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ProviderState) != 1 || string(msg.ProviderState[0]) != string(raw) {
		t.Fatalf("provider state=%s", msg.ProviderState)
	}
	_, messages, err := toAnthropicMessages([]Message{msg}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 2 {
		t.Fatalf("messages=%+v", messages)
	}
	replayed, ok := messages[0].Content[0].(json.RawMessage)
	if !ok || string(replayed) != string(raw) {
		t.Fatalf("replayed=%T %s", messages[0].Content[0], replayed)
	}
}
