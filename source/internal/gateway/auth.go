// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package gateway

import (
	"net/http"
	"strings"
)

// bearerToken extracts the Bearer token from Authorization.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const p = "Bearer "
	if len(h) < len(p) || !strings.EqualFold(h[:len(p)], p) {
		return ""
	}
	return strings.TrimSpace(h[len(p):])
}

// requireAuth wraps a handler with gateway token check.
func requireAuth(want string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if want == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]any{"message": "gateway token not configured", "type": "server_error"},
			})
			return
		}
		got := bearerToken(r)
		if got == "" || got != want {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{"message": "invalid or missing bearer token", "type": "auth_error"},
			})
			return
		}
		next(w, r)
	}
}
