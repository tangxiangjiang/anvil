// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"context"
	"fmt"
	"strings"
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

// EnsureDefaultModel picks the first gateway model when none is selected yet.
func (h *Hub) EnsureDefaultModel(ctx context.Context) error {
	llm := h.GetLLM()
	if strings.TrimSpace(llm.Model) != "" {
		return nil
	}
	list, err := h.ListModels(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return fmt.Errorf("gateway returned no models")
	}
	llm.Model = list[0].ID
	if p := strings.TrimSpace(list[0].Provider); p != "" {
		llm.Provider = p
	} else if strings.TrimSpace(llm.Provider) == "" {
		llm.Provider = "openai"
	}
	h.SetLLM(llm)
	return nil
}

// SelectModel records the app-chosen model/provider and pushes to agentd.
func (h *Hub) SelectModel(model, provider string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("model is required")
	}
	llm := h.GetLLM()
	llm.Model = model
	if p := strings.TrimSpace(provider); p != "" {
		llm.Provider = p
	}
	h.SetLLM(llm)
	return nil
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
