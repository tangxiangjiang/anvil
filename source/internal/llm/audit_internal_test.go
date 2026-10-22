// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"strings"
	"testing"
)

// audit #17: an assistant tool call whose arguments do not parse is replayed with
// input {} and the error is dropped, so the history records a different call from
// the one the model emitted.
func TestAuditMalformedToolArgsAreNotReplacedSilently(t *testing.T) {
	msgs := []Message{{
		Role:      RoleAssistant,
		ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Args: []byte(`{"path":`)}},
	}}
	_, _, convErr := toAnthropicMessages(msgs, false)
	if convErr == nil {
		t.Fatal("malformed arguments were converted without an error")
	}
	if !strings.Contains(convErr.Error(), "not valid JSON") {
		t.Fatalf("the error does not say the arguments are invalid: %v", convErr)
	}
}
