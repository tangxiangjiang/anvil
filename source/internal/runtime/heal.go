// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import "github.com/anvil-dev/anvil/internal/llm"

// InterruptedToolContent is the tool result written for a call whose real
// response never reached the store.
//
// It exists because the wire format has no way to express "this call has no
// answer": OpenAI-compatible providers reject the whole request with
// "An assistant message with 'tool_calls' must be followed by tool messages
// responding to each 'tool_call_id'". A session that lost one response — a
// killed process, a torn-down worker, two runs interleaved into one store file
// by a fork — would therefore be permanently unusable, because the hole is
// resent on every later turn. The wording tells the model what happened so it
// retries rather than assuming the call succeeded.
const InterruptedToolContent = "tool call was interrupted before a result was recorded; " +
	"its effect is unknown, so re-run it if the result is still needed"

// HealToolPairs returns messages that satisfy the provider's tool-call
// invariant: every assistant message carrying tool_calls is followed by
// exactly one tool message per call id, in call order.
//
// Two repairs, both idempotent:
//
//   - A call with no response gets a synthetic one (InterruptedToolContent), so
//     the turn is complete instead of dangling.
//   - A tool message that answers no call in the conversation is dropped: it
//     answers nothing, and a provider rejects it just as firmly. This is what a
//     truncated store tail produces when the assistant record is the part that
//     was lost.
//
// Responses are re-emitted in call order and moved directly behind their call,
// because that is the shape the providers accept — a response that arrived
// after an interleaved user turn (two runs writing to one store file) is placed
// where it belongs rather than left where it landed.
//
// A conversation that is already valid is returned unchanged (same slice), so
// the common path allocates nothing.
func HealToolPairs(messages []llm.Message) []llm.Message {
	if !hasToolPairDefect(messages) {
		return messages
	}
	out := make([]llm.Message, 0, len(messages))
	// known is every call id the conversation introduces, so a stray tool
	// message can be told apart from one whose call is simply far above.
	known := map[string]bool{}
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		if m.Role == llm.RoleTool {
			// Outside a call's own block a tool message is stray: any call it
			// answers was written into `known` before reaching here, so a miss
			// means nothing ever asked for it.
			if !known[m.ToolCallID] {
				continue
			}
			out = append(out, m)
			continue
		}
		out = append(out, m)
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			known[tc.ID] = true
		}
		// Claim the contiguous tool block that follows, keyed by call id. The
		// first response for an id wins; a duplicate is dropped rather than
		// sent twice.
		answered := make(map[string]llm.Message, len(m.ToolCalls))
		j := i + 1
		for ; j < len(messages) && messages[j].Role == llm.RoleTool; j++ {
			id := messages[j].ToolCallID
			if _, dup := answered[id]; dup {
				continue
			}
			// Only responses to *this* turn's calls belong behind it. One that
			// answers an older call keeps its own place in `known` and is
			// dropped here, which is what keeps the block contiguous.
			if !answersCall(m.ToolCalls, id) {
				continue
			}
			answered[id] = messages[j]
		}
		// Emit in call order, synthesising the holes.
		for _, tc := range m.ToolCalls {
			if resp, ok := answered[tc.ID]; ok {
				out = append(out, resp)
				continue
			}
			out = append(out, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    InterruptedToolContent,
			})
		}
		if j > i+1 {
			i = j - 1
		}
	}
	return out
}

func answersCall(calls []llm.ToolCall, id string) bool {
	if id == "" {
		return false
	}
	for _, tc := range calls {
		if tc.ID == id {
			return true
		}
	}
	return false
}

// hasToolPairDefect reports whether any repair is needed, so a healthy history
// is passed through without copying it.
func hasToolPairDefect(messages []llm.Message) bool {
	known := map[string]bool{}
	for i, m := range messages {
		switch {
		case m.Role == llm.RoleTool:
			if !known[m.ToolCallID] {
				return true
			}
		case m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0:
			for _, tc := range m.ToolCalls {
				known[tc.ID] = true
			}
			for k, tc := range m.ToolCalls {
				j := i + 1 + k
				if j >= len(messages) || messages[j].Role != llm.RoleTool || messages[j].ToolCallID != tc.ID {
					return true
				}
			}
		}
	}
	return false
}
