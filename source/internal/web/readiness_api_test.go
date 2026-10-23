// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessAPIAuthScopeAndRefresh(t *testing.T) {
	s, tok := wbServer(t, nil)

	unauth := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/v1/delivery/readiness", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated readiness = %d", unauth.Code)
	}

	for i := 0; i < 2; i++ {
		w := call(t, s, tok, http.MethodGet,
			"/api/v1/delivery/readiness?project=missing&generation=0&milestone=missing", "")
		if w.Code != http.StatusOK {
			t.Fatalf("refresh %d = %d: %s", i, w.Code, w.Body.String())
		}
		if body := w.Body.String(); body == "" || body[0] != '{' {
			t.Fatalf("refresh %d returned no JSON: %q", i, body)
		}
	}

	bad := call(t, s, tok, http.MethodGet, "/api/v1/delivery/readiness?generation=oops", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad scope = %d: %s", bad.Code, bad.Body.String())
	}
}
