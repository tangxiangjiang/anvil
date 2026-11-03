// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
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

func createClosedSearchSessions(t *testing.T, h *harness, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		session := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
		sessionID, _ := session["session_id"].(string)
		promptRun(t, h, sessionID, "local needle", autoApprove)
		h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})
	}
}
