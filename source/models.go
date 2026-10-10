// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ModelInfo is one entry from a gateway (or OpenAI-compatible) /models catalog.
type ModelInfo struct {
	ID      string
	OwnedBy string
	// Provider is an optional Anvil extension hint from the gateway.
	Provider string
	// UpstreamModel is the upstream id (advisory).
	UpstreamModel string
	Params        []string
}

// ListModelsOptions configures ListModels.
type ListModelsOptions struct {
	BaseURL string // e.g. http://127.0.0.1:8080/v1
	APIKey  string // gateway bearer token
	Client  *http.Client
}

// ListModels fetches GET {BaseURL}/models (OpenAI-shaped list).
func ListModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("BaseURL is required")
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models: HTTP %d", res.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
			Anvil   *struct {
				Provider      string   `json:"provider"`
				UpstreamModel string   `json:"upstream_model"`
				Params        []string `json:"params"`
			} `json:"anvil"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(payload.Data))
	for _, d := range payload.Data {
		m := ModelInfo{ID: d.ID, OwnedBy: d.OwnedBy}
		if d.Anvil != nil {
			m.Provider = d.Anvil.Provider
			m.UpstreamModel = d.Anvil.UpstreamModel
			m.Params = d.Anvil.Params
		}
		out = append(out, m)
	}
	return out, nil
}
