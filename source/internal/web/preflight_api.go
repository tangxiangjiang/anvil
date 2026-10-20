// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"strings"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// handlePreflight answers GET /api/v1/delivery/preflight?transition=…&target=….
//
// It is the preview half of p_0.46.11: the console asks before offering an action,
// so a disabled button can say why instead of the operator discovering it after a
// failed request. The enforcement half calls the same delivery.Preflight, so the
// two cannot disagree.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if s.delivery == nil {
		http.Error(w, "delivery store not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	t := delivery.Transition(strings.TrimSpace(r.URL.Query().Get("transition")))
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if !delivery.KnownTransitions[t] {
		http.Error(w, "unknown transition: "+string(t), http.StatusBadRequest)
		return
	}
	res, err := s.delivery.Preflight(t, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// preflightOrRefuse runs the same check a transition is about to enforce and, on
// failure, writes the refusal.
//
// Every mutating transition that has a preflight goes through this, which is what
// keeps the preview honest: there is one place that decides, and the UI reads its
// answer twice.
func (s *Server) preflightOrRefuse(w http.ResponseWriter, t delivery.Transition, target string) bool {
	if s.delivery == nil {
		return true // no store: the handler will report its own error
	}
	res, err := s.delivery.Preflight(t, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	if res.OK {
		return true
	}
	writeJSON(w, http.StatusConflict, res)
	return false
}
