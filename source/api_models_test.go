// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{
					"id": "demo", "object": "model", "owned_by": "gateway",
					"anvil": map[string]any{"provider": "openai", "upstream_model": "real"},
				},
			},
		})
	}))
	defer srv.Close()

	list, err := ListModels(context.Background(), ListModelsOptions{
		BaseURL: srv.URL + "/v1",
		APIKey:  "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "demo" || list[0].UpstreamModel != "real" {
		t.Fatalf("%+v", list)
	}
}
