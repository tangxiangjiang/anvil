// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"testing"
	"time"
)

// TestSessionNewMapsContextBudget pins the one mapping that turns a client's
// declared window into a session that compacts. A session that sends no
// context must behave exactly as it did before the field existed.
func TestSessionNewMapsContextBudget(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		Context: &ContextParams{WindowTokens: 100000, AutoCompactAt: 0.5, KeepRecent: 4},
	}))
	info := resultOf(t, h.callOK(MethodSessionContext, SessionRefParams{
		SessionID: sess["session_id"].(string),
	}))
	if got := info["window_tokens"]; got != float64(100000) {
		t.Fatalf("window_tokens = %v", got)
	}
	if got := info["compact_at_tokens"]; got != float64(50000) {
		t.Fatalf("compact_at_tokens = %v", got)
	}
	if got := info["messages"]; got != float64(0) {
		t.Fatalf("messages = %v", got)
	}

	// No context: no window, and therefore no automatic compaction.
	plain := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	info = resultOf(t, h.callOK(MethodSessionContext, SessionRefParams{
		SessionID: plain["session_id"].(string),
	}))
	if got := info["window_tokens"]; got != float64(0) {
		t.Fatalf("undeclared session reports a window: %v", got)
	}
	if got := info["compact_at_tokens"]; got != float64(0) {
		t.Fatalf("undeclared session has a mark: %v", got)
	}
}

// TestSessionContextUnknownSession checks the error a tab sees after its
// session was closed in another window.
func TestSessionContextUnknownSession(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	m := h.call(MethodSessionContext, SessionRefParams{SessionID: "sess-nope"})
	if rpcErr, ok := rpcErrorOf(m); !ok || rpcErr.Code != CodeNoSession {
		t.Fatalf("response = %v", m)
	}
}

// TestAutoCompactionDuringRunEmitsCompact walks a session past its budget and
// checks that the client sees the runtime's own compact event — forwarded like
// a todo — and that the terminal done already carries the post-run estimate.
func TestAutoCompactionDuringRunEmitsCompact(t *testing.T) {
	llm := newStubLLM(t,
		textTurn("one"),
		textTurn("summary"), // the summarizer's call during run 2
		textTurn("two"),
	)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		// Small enough that the second turn crosses the mark whatever the
		// system prompt weighs; KeepRecent 2 makes the first turn foldable.
		Context: &ContextParams{WindowTokens: 10, AutoCompactAt: 0.5, KeepRecent: 2},
	}))
	id := sess["session_id"].(string)

	h.callOK(MethodPrompt, PromptParams{SessionID: id, Message: "first"})
	h.collectRunOK(15 * time.Second)

	h.callOK(MethodPrompt, PromptParams{SessionID: id, Message: "second"})
	out := h.collectRunOK(15 * time.Second)

	if types := eventTypes(out.events); !contains(types, "compact") {
		t.Fatalf("no compact event in %v", types)
	}
	var compact map[string]any
	for _, e := range out.events {
		p := paramsOf(t, e)
		if p["type"] != "compact" {
			continue
		}
		if p["session_id"] != id {
			t.Fatalf("compact event has wrong session: %v", p)
		}
		compact, _ = p["compact"].(map[string]any)
	}
	if compact == nil || compact["reason"] != "auto" {
		t.Fatalf("compact payload = %v", compact)
	}
	if compact["summarized"] != float64(2) {
		t.Fatalf("summarized = %v, want the first turn's two messages", compact["summarized"])
	}

	done := paramsOf(t, out.done)
	ctx, _ := done["context"].(map[string]any)
	if ctx == nil || ctx["messages"] != float64(3) {
		t.Fatalf("done context = %v, want summary plus the kept turn", ctx)
	}
	usage, _ := done["usage"].(map[string]any)
	if _, ok := usage["cached_tokens"]; ok {
		t.Fatalf("usage invented a cache count: %v", usage)
	}
	if done["last_request_at"] == nil || done["last_request_at"] == "" {
		t.Fatalf("no last_request_at: %v", done)
	}
}

// TestSessionCompactOnIdleSession is /compact end to end: counts, the divider
// notification, the stored summary a replay reads, and the no-op second call.
func TestSessionCompactOnIdleSession(t *testing.T) {
	llm := newStubLLM(t,
		textTurn("one"),
		textTurn("two"),
		textTurn("summary"),
	)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		Context: &ContextParams{KeepRecent: 2},
	}))
	id := sess["session_id"].(string)
	agentID := sess["agent_id"].(string)

	for _, msg := range []string{"first", "second"} {
		h.callOK(MethodPrompt, PromptParams{SessionID: id, Message: msg})
		h.collectRunOK(15 * time.Second)
	}

	res := resultOf(t, h.callOK(MethodSessionCompact, SessionRefParams{SessionID: id}))
	if res["compacted"] != true {
		t.Fatalf("compacted = %v", res["compacted"])
	}
	if res["summarized"] != float64(2) || res["before"] == float64(0) || res["after"] == float64(0) {
		t.Fatalf("counts = %v", res)
	}
	ctx, _ := res["context"].(map[string]any)
	if ctx == nil || ctx["messages"] != float64(3) {
		t.Fatalf("context after compaction = %v", ctx)
	}

	note := h.next(5 * time.Second)
	if note["method"] != NotifyCompact {
		t.Fatalf("expected a compact notification, got %v", note)
	}
	p := paramsOf(t, note)
	compact, _ := p["compact"].(map[string]any)
	if p["type"] != "compact" || compact == nil || compact["reason"] != "manual" {
		t.Fatalf("notification = %v", p)
	}
	if p["agent_id"] != agentID {
		t.Fatalf("notification agent_id = %v", p["agent_id"])
	}

	// The replay finds the boundary as a stored entry, so a reloaded tab draws
	// the same divider the live one did.
	tr := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	var summary *TranscriptEntry
	entries, _ := tr["entries"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry["kind"] == "summary" {
			summary = &TranscriptEntry{Kind: "summary", Content: entry["content"].(string)}
		}
	}
	if summary == nil || summary.Content == "" {
		t.Fatalf("transcript has no summary entry: %v", tr)
	}

	// Nothing new to fold: the second call answers, and says so.
	again := resultOf(t, h.callOK(MethodSessionCompact, SessionRefParams{SessionID: id}))
	if again["compacted"] != false {
		t.Fatalf("second compaction = %v", again)
	}
}

// TestSessionCompactDuringRunIsRefused checks that a busy session answers with
// the same code prompt uses, and that the run is untouched by the attempt.
func TestSessionCompactDuringRunIsRefused(t *testing.T) {
	llm := newStubLLM(t)
	llm.blockUntilCancel = true
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	id := sess["session_id"].(string)
	h.callOK(MethodPrompt, PromptParams{SessionID: id, Message: "hang"})

	m := h.call(MethodSessionCompact, SessionRefParams{SessionID: id})
	if rpcErr, ok := rpcErrorOf(m); !ok || rpcErr.Code != CodeRunInProgress {
		t.Fatalf("response = %v", m)
	}

	h.callOK(MethodCancel, CancelParams{SessionID: id})
	out := h.collectRunOK(15 * time.Second)
	if out.done == nil {
		t.Fatal("the cancelled run never reached done")
	}
}

// TestDoneCarriesCacheUsageAndAnchor checks the two facts the editor's cache
// countdown needs, once the provider reports them.
func TestDoneCarriesCacheUsageAndAnchor(t *testing.T) {
	llm := newStubLLM(t, textTurn("hello"))
	llm.setCachedTokens(20)
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	id := sess["session_id"].(string)
	h.callOK(MethodPrompt, PromptParams{SessionID: id, Message: "hi"})
	out := h.collectRunOK(15 * time.Second)

	done := paramsOf(t, out.done)
	usage, _ := done["usage"].(map[string]any)
	if usage == nil || usage["cached_tokens"] != float64(20) {
		t.Fatalf("usage = %v", usage)
	}
	anchor, _ := done["last_request_at"].(string)
	if anchor == "" {
		t.Fatalf("no last_request_at: %v", done)
	}
	if _, err := time.Parse(time.RFC3339, anchor); err != nil {
		t.Fatalf("last_request_at %q is not RFC 3339: %v", anchor, err)
	}
	ctx, _ := done["context"].(map[string]any)
	if ctx == nil || ctx["messages"] != float64(2) {
		t.Fatalf("context = %v", ctx)
	}
}
