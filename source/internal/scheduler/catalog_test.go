// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/scheduler"
)

func TestEnsureDefaultAndSelectModel(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "tok")
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "first/m", "provider": "openai"},
				{"id": "second/m", "provider": "anthropic"},
			},
		})
	}))
	t.Cleanup(gw.Close)

	cfg := scheduler.DefaultConfig()
	cfg.TokenEnv = "ANVIL_SCHEDULER_TOKEN"
	hub := scheduler.NewHub(cfg, nil)
	hub.SetLLM(daemon.ClusterLLMBody{BaseURL: gw.URL + "/v1", APIKey: "k"})

	if err := hub.EnsureDefaultModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	llm := hub.GetLLM()
	if llm.Model != "first/m" || llm.Provider != "openai" {
		t.Fatalf("%+v", llm)
	}
	if err := hub.SelectModel("second/m", "anthropic"); err != nil {
		t.Fatal(err)
	}
	llm = hub.GetLLM()
	if llm.Model != "second/m" || llm.Provider != "anthropic" {
		t.Fatalf("%+v", llm)
	}
}
