// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// handleReadiness serves the independent readiness and whole-record diagnostic
// view. Filters are exact scope boundaries, not client-side hiding.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if s.delivery == nil {
		http.Error(w, "delivery store not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	scope := delivery.ReadinessScope{
		EngReqID:    strings.TrimSpace(r.URL.Query().Get("project")),
		MilestoneID: strings.TrimSpace(r.URL.Query().Get("milestone")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("generation")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "generation must be a non-negative integer", http.StatusBadRequest)
			return
		}
		scope.Major = &n
	}
	report, err := s.delivery.Readiness(scope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
