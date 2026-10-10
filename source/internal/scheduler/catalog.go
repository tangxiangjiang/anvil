// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/anvil-dev/anvil"
)

type modelJSON struct {
	ID            string   `json:"id"`
	OwnedBy       string   `json:"owned_by,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	UpstreamModel string   `json:"upstream_model,omitempty"`
	Params        []string `json:"params,omitempty"`
	Vision        bool     `json:"vision,omitempty"`
}

// ListModels returns a cached gateway catalog using current llm credentials.
func (h *Hub) ListModels(ctx context.Context) ([]modelJSON, error) {
	h.modelsMu.Lock()
	ttl := time.Duration(h.cfg.ModelsCacheSec) * time.Second
	if len(h.modelsCache) > 0 && time.Since(h.modelsAt) < ttl {
		out := append([]modelJSON(nil), h.modelsCache...)
		h.modelsMu.Unlock()
		return out, nil
	}
	h.modelsMu.Unlock()

	llm := h.GetLLM()
	if llm.BaseURL == "" {
		return nil, fmt.Errorf("llm.base_url not configured")
	}
	list, err := anvil.ListModels(ctx, anvil.ListModelsOptions{
		BaseURL: llm.BaseURL,
		APIKey:  llm.APIKey,
	})
	if err != nil {
		return nil, err
	}
	out := make([]modelJSON, 0, len(list))
	for _, m := range list {
		out = append(out, modelJSON{
			ID:            m.ID,
			OwnedBy:       m.OwnedBy,
			Provider:      m.Provider,
			UpstreamModel: m.UpstreamModel,
			Params:        append([]string(nil), m.Params...),
			Vision:        m.Vision,
		})
	}
	h.modelsMu.Lock()
	h.modelsCache = out
	h.modelsAt = time.Now()
	h.modelsMu.Unlock()
	return out, nil
}
