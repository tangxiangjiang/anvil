// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func startGW(t *testing.T, cfg Config, upstream http.HandlerFunc) (baseURL string, cancel context.CancelFunc) {
	t.Helper()
	t.Setenv("ANVIL_GATEWAY_TOKEN", "gw-token")
	t.Setenv("UP_KEY", "upstream-secret-key")

	var upURL string
	if upstream != nil {
		up := httptest.NewServer(upstream)
		t.Cleanup(up.Close)
		upURL = up.URL + "/v1"
		cfg.Upstreams = map[string]UpstreamConfig{
			"u1": {BaseURL: upURL, APIKeyEnv: "UP_KEY"},
		}
		for i := range cfg.Models {
			cfg.Models[i].Upstream = "u1"
		}
	}
	cfg.Listen = "127.0.0.1:0"
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a := srv.Addr(); a != "" && a != "127.0.0.1:0" {
			t.Cleanup(cancel)
			return "http://" + a, cancel
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	t.Fatal("gateway did not start")
	return "", nil
}

func TestModelsAuthAndCatalog(t *testing.T) {
	cfg := fixtureConfig()
	base, _ := startGW(t, cfg, nil)

	res, err := http.Get(base + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer gw-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var list modelsListResponse
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "demo" {
		t.Fatalf("%+v", list)
	}
	if list.Data[0].Anvil == nil || list.Data[0].Anvil.UpstreamModel != "real-model" {
		t.Fatalf("anvil ext %+v", list.Data[0].Anvil)
	}
}

func TestChatProxyRewritesModelAndHidesUpstreamKey(t *testing.T) {
	var sawAuth string
	var sawModel string
	cfg := fixtureConfig()
	base, _ := startGW(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hi"}}},
		})
	})

	req, _ := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{"model":"demo","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer gw-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, raw)
	}
	if sawAuth != "Bearer upstream-secret-key" {
		t.Fatalf("upstream auth=%q", sawAuth)
	}
	if sawModel != "real-model" {
		t.Fatalf("upstream model=%q", sawModel)
	}
	if strings.Contains(string(raw), "upstream-secret-key") {
		t.Fatal("upstream key leaked to client")
	}
}

func TestChatProxyUnknownModel(t *testing.T) {
	cfg := fixtureConfig()
	base, _ := startGW(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not hit upstream")
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{"model":"nope","messages":[]}`))
	req.Header.Set("Authorization", "Bearer gw-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestChatProxyStream(t *testing.T) {
	cfg := fixtureConfig()
	base, _ := startGW(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		fl.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{"model":"demo","stream":true,"messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer gw-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(raw), "data:") || !strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("stream body=%q", raw)
	}
}
