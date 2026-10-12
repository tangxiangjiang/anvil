// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/scheduler"
)

func TestE2EConfigureJoinJobModels(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "sched-secret")
	t.Setenv("ANVIL_AGENTD_TOKEN", "local-token")

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer gw-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "private/demo", "owned_by": "anvil"}},
		})
	}))
	defer gw.Close()

	appLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	appAddr := appLn.Addr().String()
	_ = appLn.Close()

	clusterLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clusterHost := clusterLn.Addr().String()
	_ = clusterLn.Close()

	cfg := scheduler.DefaultConfig()
	cfg.ListenApp = appAddr
	cfg.ListenCluster = "tcp://" + clusterHost
	cfg.TokenEnv = "ANVIL_SCHEDULER_TOKEN"
	cfg.StaleSec = 10
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}

	hub := scheduler.NewHub(cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = hub.Run(ctx) }()

	// Wait for app listen.
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", appAddr, 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("app listen timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}

	putLLM(t, appAddr, map[string]any{
		"base_url": gw.URL + "/v1",
		"api_key":  "gw-token",
		"model":    "private/demo",
		"provider": "openai",
	})

	dir := t.TempDir()
	agentListen := freeTCP(t)
	agentCfg := daemon.DefaultConfig()
	agentCfg.Listen = "tcp://" + agentListen
	agentCfg.MaxWorkers = 0
	agentCfg.Cluster = daemon.ClusterConfig{
		Enabled:      true,
		URL:          "tcp://" + clusterHost,
		NodeID:       "node-e2e",
		TokenEnv:     "ANVIL_SCHEDULER_TOKEN",
		HeartbeatSec: 1,
	}
	if err := agentCfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	srv, err := daemon.NewServer(agentCfg, &daemon.EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SeedBoundWorker("demo", dir); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	go func() { _ = daemon.NewClusterEdge(agentCfg, srv, nil).Run(ctx) }()

	waitNodes(t, appAddr, 1)

	llm, ok := srv.RunnerLLM()
	if !ok || llm.APIKey != "gw-token" || llm.BaseURL != gw.URL+"/v1" {
		t.Fatalf("agentd llm not provisioned: ok=%v %+v", ok, llm)
	}

	models := getJSON(t, appAddr, "/v1/models")
	data, _ := models["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("models=%v", models)
	}

	// Live config push
	putLLM(t, appAddr, map[string]any{
		"base_url": gw.URL + "/v1",
		"api_key":  "gw-token-2",
		"model":    "private/demo",
	})
	deadline = time.Now().Add(2 * time.Second)
	for {
		llm, _ = srv.RunnerLLM()
		if llm.APIKey == "gw-token-2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live config not applied: %+v", llm)
		}
		time.Sleep(20 * time.Millisecond)
	}

	jobBody := map[string]any{
		"repo_id": "demo", "prompt": "hello-sched", "force_stub": true,
	}
	raw, _ := json.Marshal(jobBody)
	req, _ := http.NewRequest(http.MethodPost, "http://"+appAddr+"/v1/jobs", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer sched-secret")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("jobs status=%d body=%s", res.StatusCode, b)
	}
	var done daemon.JobDoneBody
	if err := json.NewDecoder(res.Body).Decode(&done); err != nil {
		t.Fatal(err)
	}
	if done.Status != "finished" || done.Result != "stub:hello-sched" {
		t.Fatalf("%+v", done)
	}

	_ = os.WriteFile(filepath.Join(dir, "ok"), []byte("1"), 0o644)
}

func freeTCP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func putLLM(t *testing.T, appAddr string, body map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, "http://"+appAddr+"/v1/llm", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer sched-secret")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("put llm %d %s", res.StatusCode, b)
	}
}

func getJSON(t *testing.T, appAddr, path string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+appAddr+path, nil)
	req.Header.Set("Authorization", "Bearer sched-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("get %s %d %s", path, res.StatusCode, b)
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func waitNodes(t *testing.T, appAddr string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m := getJSON(t, appAddr, "/v1/nodes")
		nodes, _ := m["nodes"].([]any)
		if len(nodes) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %d nodes, got %v", n, m)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
