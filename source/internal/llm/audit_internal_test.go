// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// audit #17: an assistant tool call whose arguments do not parse is replayed with
// input {} and the error is dropped, so the history records a different call from
// the one the model emitted.
func TestAuditMalformedToolArgsAreNotReplacedSilently(t *testing.T) {
	t.Skip("audit: open, see audit.md #17")
	msgs := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Args: []byte(`{"path":`)}},
	}}
	_, out := toAnthropicMessages(msgs, false)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"input":{}`) {
		t.Fatalf("malformed arguments were replaced by {} in the replayed history: %s", raw)
	}
}
