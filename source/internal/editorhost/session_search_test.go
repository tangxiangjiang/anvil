// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSessionSearchPagesClosedSessionsWithoutResuming(t *testing.T) {
	llm := newStubLLM(t, textTurn("done a"), textTurn("done b"), textTurn("done c"))
	h, _ := initHost(t, llm.baseURL())
	createClosedSearchSessions(t, h, 3)

	first := resultOf(t, h.callOK(MethodSessionSearch, SessionSearchParams{
		Query: "needle", Limit: 1,
	}))
	hits, _ := first["hits"].([]any)
	cursor, _ := first["next_cursor"].(string)
	if len(hits) != 1 || cursor == "" {
		t.Fatalf("first=%v", first)
	}
	second := resultOf(t, h.callOK(MethodSessionSearch, SessionSearchParams{
		Query: "needle", Limit: 1, Cursor: cursor,
	}))
	if hits, _ := second["hits"].([]any); len(hits) != 1 {
		t.Fatalf("second=%v", second)
	}
	h.srv.mu.Lock()
	liveSessions := len(h.srv.sessions)
	h.srv.mu.Unlock()
	if liveSessions != 0 {
		t.Fatalf("search resumed %d live sessions", liveSessions)
	}
}

func TestSessionSearchRejectsStaleCursorAndInvalidTime(t *testing.T) {
	llm := newStubLLM(t, textTurn("done a"), textTurn("done b"))
	h, _ := initHost(t, llm.baseURL())
	createClosedSearchSessions(t, h, 2)
	first := resultOf(t, h.callOK(MethodSessionSearch, SessionSearchParams{
		Query: "needle", Limit: 1,
	}))
	cursor, _ := first["next_cursor"].(string)
	stale := h.call(MethodSessionSearch, SessionSearchParams{
		Query: "changed", Limit: 1, Cursor: cursor,
	})
	rpcErr, ok := rpcErrorOf(stale)
	if !ok || rpcErr.Code != CodeSessionCursor {
		t.Fatalf("stale=%v", stale)
	}
	invalidTime := h.call(MethodSessionSearch, SessionSearchParams{
		Query: "needle", After: "not-a-date",
	})
	rpcErr, ok = rpcErrorOf(invalidTime)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("invalid time=%v", invalidTime)
	}
}

func TestSessionSearchHistoryContextUsesHitAnchorWithoutResuming(t *testing.T) {
	llm := newStubLLM(t, textTurn("done"))
	h, _ := initHost(t, llm.baseURL())
	createClosedSearchSessions(t, h, 1)
	search := resultOf(t, h.callOK(MethodSessionSearch, SessionSearchParams{Query: "needle"}))
	hits, _ := search["hits"].([]any)
	if len(hits) == 0 {
		t.Fatalf("search=%v", search)
	}
	var hit map[string]any
	for _, raw := range hits {
		candidate, _ := raw.(map[string]any)
		if candidate["kind"] == "message" {
			hit = candidate
			break
		}
	}
	if hit == nil {
		t.Fatalf("no message hit: %v", search)
	}
	agentID, _ := hit["agent_id"].(string)
	recordPos, _ := hit["record_pos"].(float64)
	contextResult := resultOf(t, h.callOK(MethodSessionHistoryContext, SessionHistoryContextParams{
		AgentID: agentID, RecordPos: uint64(recordPos), Before: 1, After: 1,
	}))
	entries, _ := contextResult["entries"].([]any)
	target, _ := contextResult["target_index"].(float64)
	if len(entries) == 0 || int(target) >= len(entries) {
		t.Fatalf("context=%v", contextResult)
	}
	h.srv.mu.Lock()
	liveSessions := len(h.srv.sessions)
	h.srv.mu.Unlock()
	if liveSessions != 0 {
		t.Fatalf("history context resumed %d live sessions", liveSessions)
	}

	expired := h.call(MethodSessionHistoryContext, SessionHistoryContextParams{
		AgentID: agentID, RecordPos: 999,
	})
	rpcErr, ok := rpcErrorOf(expired)
	if !ok || rpcErr.Code != CodeSessionHistoryAnchor {
		t.Fatalf("expired=%v", expired)
	}
}

func TestSessionSearchCancellationNotificationCancelsRequestContext(t *testing.T) {
	server := New(Config{}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	server.requests[7] = cancel
	params, err := json.Marshal(map[string]int64{"id": 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.handleNotification(context.Background(), &Request{
		Method: "$/cancelRequest", Params: params,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("request context was not cancelled")
	}
}

func createClosedSearchSessions(t *testing.T, h *harness, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		session := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
		sessionID, _ := session["session_id"].(string)
		promptRun(t, h, sessionID, "local needle", autoApprove)
		h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})
	}
}
