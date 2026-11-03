// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

func TestRetrySignalLiveAndReplayPreserveResolvedPolicy(t *testing.T) {
	providerMessage := "temporarily unavailable for sk-1234567890abcd " + strings.Repeat("x", 700)
	errorBody, err := json.Marshal(map[string]any{
		"error": map[string]any{"message": providerMessage},
	})
	if err != nil {
		t.Fatal(err)
	}
	llm := newStubLLM(t,
		turn{
			status:    503,
			errorBody: string(errorBody),
		},
		turn{content: "recovered"},
	)
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)

	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "retry once"})
	out := h.collectRunOK(15 * time.Second)

	var live map[string]any
	retryIndex, assistantIndex := -1, -1
	for i, notification := range out.events {
		params := paramsOf(t, notification)
		switch params["type"] {
		case string(anvil.EventRetry):
			live, retryIndex = params, i
		case string(anvil.EventAssistant):
			if assistantIndex < 0 {
				assistantIndex = i
			}
		}
	}
	if live == nil || retryIndex < 0 || assistantIndex < 0 || retryIndex >= assistantIndex {
		t.Fatalf("retry must precede recovered output: events=%v", eventTypes(out.events))
	}
	if live["attempt"] != float64(1) || live["max_attempts"] != float64(3) {
		t.Fatalf("retry policy fields=%v", live)
	}
	// Full jitter deliberately includes zero, in which case omitempty leaves
	// delay_ms off the wire: that is an immediate retry, not a missing policy.
	if delay, ok := live["delay_ms"].(float64); ok && delay < 0 {
		t.Fatalf("retry delay is negative: %v", live)
	}
	if text, _ := live["text"].(string); !strings.Contains(text, "temporarily unavailable") {
		t.Fatalf("safe provider summary missing: %v", live)
	} else if strings.Contains(text, "sk-1234567890abcd") || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("retry summary leaked provider secret: %v", live)
	} else if len([]rune(text)) > 512 {
		t.Fatalf("retry summary was not bounded: %d runes", len([]rune(text)))
	}

	replayed := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	var retry map[string]any
	for _, raw := range replayed["entries"].([]any) {
		entry := raw.(map[string]any)
		if entry["kind"] == anvil.TranscriptRetry {
			retry = entry
			break
		}
	}
	if retry == nil || retry["run_id"] != live["run_id"] ||
		retry["attempt"] != live["attempt"] ||
		retry["max_attempts"] != live["max_attempts"] ||
		retry["delay_ms"] != live["delay_ms"] {
		t.Fatalf("replay retry=%v live=%v", retry, live)
	}
}

func TestTruncationSignalIsMarkerAndDoneIsAuthoritative(t *testing.T) {
	llm := newStubLLM(t,
		turn{content: "partial answer", finishReason: "length"},
		textTurn("continued answer"),
	)
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)

	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "be concise"})
	out := h.collectRunOK(15 * time.Second)

	truncatedCount := 0
	assistantIndex, truncatedIndex := -1, -1
	for i, notification := range out.events {
		params := paramsOf(t, notification)
		switch params["type"] {
		case string(anvil.EventAssistant):
			if assistantIndex < 0 {
				assistantIndex = i
			}
		case string(anvil.EventTruncated):
			truncatedCount++
			truncatedIndex = i
			if _, hasText := params["text"]; hasText {
				t.Fatalf("truncation marker duplicated assistant text: %v", params)
			}
		}
	}
	if truncatedCount != 1 || assistantIndex < 0 || truncatedIndex <= assistantIndex {
		t.Fatalf("events=%v", eventTypes(out.events))
	}
	done := paramsOf(t, out.done)
	if done["status"] != "finished" || done["truncated"] != true {
		t.Fatalf("terminal truncation missing: %v", done)
	}
	truncatedRunID, _ := done["run_id"].(string)

	replayed := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	replayTruncated, endTruncated := 0, false
	for _, raw := range replayed["entries"].([]any) {
		entry := raw.(map[string]any)
		if entry["kind"] == anvil.TranscriptTruncated {
			replayTruncated++
			if _, hasText := entry["text"]; hasText {
				t.Fatalf("persisted marker contains duplicate text: %v", entry)
			}
		}
		if entry["kind"] == anvil.TranscriptRunEnd {
			endTruncated, _ = entry["truncated"].(bool)
		}
	}
	if replayTruncated != 1 || !endTruncated {
		t.Fatalf("replayed entries=%v", replayed["entries"])
	}

	raw, err := json.Marshal(replayed)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"reasoning_content", "provider_state", "thinking_blocks"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("private provider state reached replay: %s", raw)
		}
	}

	h.callOK(MethodPrompt, PromptParams{
		SessionID:    sessionID,
		Message:      continuationPrompt,
		ContinueFrom: truncatedRunID,
	})
	continued := h.collectRunOK(15 * time.Second)
	if paramsOf(t, continued.done)["truncated"] == true || llm.callCount() != 2 {
		t.Fatalf("continuation did not make exactly one ordinary provider call: done=%v calls=%d",
			paramsOf(t, continued.done), llm.callCount())
	}
	replayed = resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	provenanceFound := false
	for _, raw := range replayed["entries"].([]any) {
		entry := raw.(map[string]any)
		if entry["kind"] == anvil.TranscriptMessage &&
			entry["role"] == "user" &&
			entry["continue_from"] == truncatedRunID {
			provenanceFound = true
		}
	}
	if !provenanceFound {
		t.Fatalf("continuation provenance missing from replay: %v", replayed["entries"])
	}
}

func TestContinuationRejectsNonLatestOrNonTruncatedRun(t *testing.T) {
	llm := newStubLLM(t, turn{content: "complete"})
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "normal"})
	out := h.collectRunOK(15 * time.Second)
	runID, _ := paramsOf(t, out.done)["run_id"].(string)
	callsBefore := llm.callCount()

	for _, params := range []PromptParams{
		{SessionID: sessionID, Message: continuationPrompt, ContinueFrom: runID},
		{SessionID: sessionID, Message: "different text", ContinueFrom: runID},
		{SessionID: sessionID, Message: continuationPrompt, ContinueFrom: "foreign-run"},
	} {
		msg := h.call(MethodPrompt, params)
		if rpcErr, ok := rpcErrorOf(msg); !ok || rpcErr.Code != CodeInvalidParams {
			t.Fatalf("invalid continuation accepted: params=%+v response=%v", params, msg)
		}
	}
	if llm.callCount() != callsBefore {
		t.Fatalf("rejected continuations reached provider: before=%d after=%d",
			callsBefore, llm.callCount())
	}
}

func TestContinuationDoesNotReplayCompletedTools(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"continued.txt","content":"once"}`),
		turn{content: "partial after tool", finishReason: "length"},
		textTurn("remaining prose"),
	)
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "write then answer"})
	first := h.collectRunOK(15 * time.Second)
	runID, _ := paramsOf(t, first.done)["run_id"].(string)
	if paramsOf(t, first.done)["truncated"] != true {
		t.Fatalf("fixture did not truncate: %v", paramsOf(t, first.done))
	}

	h.callOK(MethodPrompt, PromptParams{
		SessionID:    sessionID,
		Message:      continuationPrompt,
		ContinueFrom: runID,
	})
	continued := h.collectRunOK(15 * time.Second)
	for _, event := range continued.events {
		switch paramsOf(t, event)["type"] {
		case string(anvil.EventToolCall), string(anvil.EventToolResult):
			t.Fatalf("continuation replayed a completed tool: %v", paramsOf(t, event))
		}
	}
	if llm.callCount() != 3 {
		t.Fatalf("want two original provider rounds and one continuation, got %d", llm.callCount())
	}
}

func TestCancellationDuringRetryBackoffRemainsCancellation(t *testing.T) {
	llm := newStubLLM(t,
		turn{status: 503, errorBody: `{"error":{"message":"temporarily unavailable"}}`},
		turn{content: "must not run"},
	)
	h, _ := initHost(t, llm.baseURL())
	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	sessionID, _ := sess["session_id"].(string)
	ack := resultOf(t, h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "cancel retry"}))
	runID, _ := ack["run_id"].(string)

	deadline := time.Now().Add(10 * time.Second)
	for {
		notification := h.next(time.Until(deadline))
		if notification["method"] != NotifyEvent {
			continue
		}
		if paramsOf(t, notification)["type"] == string(anvil.EventRetry) {
			break
		}
	}
	h.callOK(MethodCancel, CancelParams{SessionID: sessionID, RunID: runID})
	out := h.collectRunOK(10 * time.Second)
	done := paramsOf(t, out.done)
	if done["status"] != string(anvil.StatusCancelled) || done["truncated"] == true {
		t.Fatalf("cancelled retry became another outcome: %v", done)
	}
	if llm.callCount() != 1 {
		t.Fatalf("provider calls=%d, retry escaped cancellation", llm.callCount())
	}
}

func TestRunSignalsRemainDistinctFromOtherTerminalOutcomes(t *testing.T) {
	cancelled := projectDone("s", "cancelled", &anvil.RunResult{Status: anvil.StatusCancelled}, nil)
	failed := projectDone("s", "failed", &anvil.RunResult{
		Status: anvil.StatusError, Code: anvil.CodeProviderMidStream,
	}, nil)
	finished := projectDone("s", "finished", &anvil.RunResult{Status: anvil.StatusFinished}, nil)
	truncated := projectDone("s", "truncated", &anvil.RunResult{
		Status: anvil.StatusFinished, Truncated: true,
	}, nil)

	if cancelled.Status == failed.Status || failed.Status == finished.Status ||
		cancelled.Truncated || failed.Truncated || finished.Truncated || !truncated.Truncated {
		t.Fatalf("outcomes collapsed: cancelled=%+v failed=%+v finished=%+v truncated=%+v",
			cancelled, failed, finished, truncated)
	}
}
