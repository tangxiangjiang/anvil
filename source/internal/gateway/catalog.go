// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package gateway

import "net/http"

type modelsListResponse struct {
	Object string       `json:"object"`
	Data   []modelEntry `json:"data"`
}

type modelEntry struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	OwnedBy string         `json:"owned_by"`
	Anvil   *anvilExt      `json:"anvil,omitempty"`
}

type anvilExt struct {
	Provider      string   `json:"provider"`
	UpstreamModel string   `json:"upstream_model"`
	Params        []string `json:"params,omitempty"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": map[string]any{"message": "method not allowed", "type": "invalid_request"},
		})
		return
	}
	out := modelsListResponse{Object: "list"}
	for _, m := range s.cfg.Models {
		out.Data = append(out.Data, modelEntry{
			ID:      m.ID,
			Object:  "model",
			OwnedBy: m.OwnedBy,
			Anvil: &anvilExt{
				Provider:      m.Provider,
				UpstreamModel: m.UpstreamModel,
				Params:        m.Params,
			},
		})
	}
	writeJSON(w, http.StatusOK, out)
}
