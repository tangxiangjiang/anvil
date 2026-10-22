// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/scheduler"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

func TestE2EConfigureJoinJobModels(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "sched-secret")
	t.Setenv("ANVIL_AGENTD_TOKEN", "local-token")

	var providerCalls int
	var providerRoles []string
	var providerTools int
	var providerModel string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			providerCalls++
			var body struct {
				Messages []struct {
					Role    string `json:"role"`
					Content any    `json:"content"`
				} `json:"messages"`
				Tools  []json.RawMessage `json:"tools"`
				Stream bool              `json:"stream"`
				Model  string            `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			providerTools = len(body.Tools)
			providerModel = body.Model
			hasToolResult := false
			wantMCP := false
			wantHookBlock := false
			wantApproval := false
			for _, msg := range body.Messages {
				providerRoles = append(providerRoles, msg.Role)
				hasToolResult = hasToolResult || msg.Role == "tool"
				wantMCP = wantMCP || strings.Contains(fmt.Sprint(msg.Content), "mcp")
				wantHookBlock = wantHookBlock || strings.Contains(fmt.Sprint(msg.Content), "hook")
				wantApproval = wantApproval || strings.Contains(fmt.Sprint(msg.Content), "approval")
			}
			message := map[string]any{"role": "assistant", "content": "read complete"}
			if !hasToolResult {
				toolName, args := "read_file", `{"path":"fixture.txt"}`
				if wantMCP {
					toolName, args = "mcp_fake_echo", `{"text":"loopback-mcp"}`
				}
				if wantHookBlock {
					toolName, args = "mcp_fake_echo", `{"text":"blocked"}`
				}
				if wantApproval {
					toolName, args = "write_file", `{"path":"approved.txt","content":"approved"}`
				}
				message = map[string]any{
					"role": "assistant", "content": "reading fixture",
					"tool_calls": []any{map[string]any{
						"id": "call_e2e", "type": "function",
						"function": map[string]any{"name": toolName, "arguments": args},
					}},
				}
			}
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				if !hasToolResult {
					toolName, args := "read_file", `{\"path\":\"fixture.txt\"}`
					if wantMCP {
						toolName, args = "mcp_fake_echo", `{\"text\":\"loopback-mcp\"}`
					}
					if wantHookBlock {
						toolName, args = "mcp_fake_echo", `{\"text\":\"blocked\"}`
					}
					if wantApproval {
						toolName, args = "write_file", `{\"path\":\"approved.txt\",\"content\":\"approved\"}`
					}
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_e2e\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]}}]}\n\n", toolName, args)
				} else {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"read complete\"}}]}\n\n")
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": message}},
				"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
			return
		}
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
	probe, err := llm.NewOpenAI(llm.Config{APIKey: "gw-token", BaseURL: gw.URL + "/v1", Model: "private/demo"}).
		Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "probe"}}})
	if err != nil || len(probe.Message.ToolCalls) != 1 {
		t.Fatalf("fake provider tool protocol: response=%+v err=%v", probe, err)
	}
	providerCalls, providerRoles, providerTools, providerModel = 0, nil, 0, ""

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
	fakeMCP := filepath.Join(t.TempDir(), "fake-mcp")
	if runtime.GOOS == "windows" {
		fakeMCP += ".exe"
	}
	buildMCP := exec.Command("go", "build", "-o", fakeMCP, "../mcp/fakecmd")
	buildMCP.Dir = "."
	if output, err := buildMCP.CombinedOutput(); err != nil {
		t.Fatalf("build fake MCP: %v\n%s", err, output)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"fake": map[string]any{"command": fakeMCP},
	}})
	if err := os.WriteFile(filepath.Join(dir, ".cursor", "mcp.json"), mcpConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "permissions.json"), []byte(
		`{"version":1,"rules":[{"effect":"allow","tool":"mcp_fake_echo","pattern":"*"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "hooks.json"), []byte(
		`{"version":1,"beforeMCP":[{"denyContains":"blocked","message":"hook blocked e2e"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if _, err := srv.Slots().SetWorkerLLM("demo", &daemon.WorkerLLMBody{Model: "worker-special"}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	go func() { _ = daemon.NewClusterEdge(agentCfg, srv, nil).Run(ctx) }()

	waitNodes(t, appAddr, 1)

	// join_ok LLM apply races registration: hub lists the node before the edge
	// finishes ApplyClusterLLM, so wait rather than assert immediately.
	var llm daemon.LLMConfig
	deadline = time.Now().Add(2 * time.Second)
	for {
		var ok bool
		llm, ok = srv.RunnerLLM()
		if ok && llm.APIKey == "gw-token" && llm.BaseURL == gw.URL+"/v1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agentd llm not provisioned: ok=%v %+v", ok, llm)
		}
		time.Sleep(20 * time.Millisecond)
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

	if err := os.WriteFile(filepath.Join(dir, "fixture.txt"), []byte("loopback tool data"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &schedclient.Client{BaseURL: "http://" + appAddr, Token: "sched-secret"}
	var events []daemon.JobEventBody
	done, err := client.CreateJobStream(ctx, schedclient.JobCreate{
		RepoID: "demo", Prompt: "read fixture.txt",
	}, func(ev daemon.JobEventBody) {
		events = append(events, ev)
	})
	if err != nil || done.Status != "finished" || providerCalls < 2 || providerModel != "worker-special" {
		t.Fatalf("done=%+v err=%v calls=%d roles=%v tools=%d model=%q events=%+v",
			done, err, providerCalls, providerRoles, providerTools, providerModel, events)
	}
	sawRead := false
	for _, event := range events {
		sawRead = sawRead || event.Name == "read_file"
	}
	if !sawRead {
		t.Fatalf("EmbeddedRunner did not emit the real read_file call: %+v", events)
	}

	_ = os.WriteFile(filepath.Join(dir, "ok"), []byte("1"), 0o644)

	// Wait for agentd heartbeat to restore idle after the first job.
	deadline = time.Now().Add(3 * time.Second)
	for {
		nodes := getJSON(t, appAddr, "/v1/nodes")
		list, _ := nodes["nodes"].([]any)
		idleOK := false
		for _, raw := range list {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			switch v := m["idle"].(type) {
			case float64:
				idleOK = v > 0
			case int:
				idleOK = v > 0
			}
			if idleOK {
				break
			}
		}
		if idleOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node idle not restored before stream job: %+v", nodes)
		}
		time.Sleep(50 * time.Millisecond)
	}

	providerCalls, providerRoles = 0, nil
	events = nil
	done, err = client.CreateJobStream(ctx, schedclient.JobCreate{
		RepoID: "demo", Prompt: "call the mcp echo tool",
	}, func(ev daemon.JobEventBody) {
		events = append(events, ev)
	})
	sawMCP := false
	for _, event := range events {
		sawMCP = sawMCP || event.Name == "mcp_fake_echo"
	}
	if err != nil || done.Status != "finished" || providerCalls < 2 || !sawMCP {
		t.Fatalf("real MCP path: done=%+v err=%v calls=%d events=%+v", done, err, providerCalls, events)
	}

	deadline = time.Now().Add(3 * time.Second)
	for {
		nodes := getJSON(t, appAddr, "/v1/nodes")
		list, _ := nodes["nodes"].([]any)
		idle := false
		for _, raw := range list {
			if row, ok := raw.(map[string]any); ok {
				if count, ok := row["idle"].(float64); ok && count > 0 {
					idle = true
				}
			}
		}
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node idle not restored after MCP job: %+v", nodes)
		}
		time.Sleep(50 * time.Millisecond)
	}

	providerCalls, providerRoles = 0, nil
	events = nil
	done, err = client.CreateJobStream(ctx, schedclient.JobCreate{
		RepoID: "demo", Prompt: "exercise the mcp hook denial",
	}, func(ev daemon.JobEventBody) {
		events = append(events, ev)
	})
	hookDenied := false
	for _, event := range events {
		hookDenied = hookDenied || (event.IsError && strings.Contains(event.Content+event.Text, "hook blocked e2e"))
	}
	if err != nil || done.Status != "finished" || providerCalls < 2 || !hookDenied {
		t.Fatalf("hook path: done=%+v err=%v calls=%d events=%+v", done, err, providerCalls, events)
	}

}

func TestSchedulerRestartNodeReconnectsAndRunsNewJob(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "sched-secret")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"restart-ok\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	appAddr := freeTCP(t)
	clusterAddr := freeTCP(t)
	cfg := scheduler.DefaultConfig()
	cfg.ListenApp = appAddr
	cfg.ListenCluster = "tcp://" + clusterAddr
	cfg.TokenEnv = "ANVIL_SCHEDULER_TOKEN"
	cfg.StaleSec = 5
	cfg.LLM.BaseURL = provider.URL
	cfg.LLM.APIKey = "local-key"
	cfg.LLM.Model = "restart-model"
	cfg.LLM.Provider = "openai"
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}

	rootCtx, stopAll := context.WithCancel(context.Background())
	defer stopAll()
	startHub := func() (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(rootCtx)
		done := make(chan error, 1)
		hub := scheduler.NewHub(cfg, nil)
		go func() { done <- hub.Run(ctx) }()
		return cancel, done
	}
	stopFirst, firstDone := startHub()
	waitTCP(t, appAddr)

	agentCfg := daemon.DefaultConfig()
	agentCfg.Listen = "tcp://" + freeTCP(t)
	agentCfg.SandboxDir = t.TempDir()
	agentCfg.Cluster = daemon.ClusterConfig{
		Enabled: true, URL: "tcp://" + clusterAddr, NodeID: "restart-node",
		TokenEnv: "ANVIL_SCHEDULER_TOKEN", HeartbeatSec: 1,
	}
	if err := agentCfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	server, err := daemon.NewServer(agentCfg, &daemon.EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SeedBoundWorker("restart-worker", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	go func() { _ = daemon.NewClusterEdge(agentCfg, server, nil).Run(rootCtx) }()
	waitNodes(t, appAddr, 1)

	stopFirst()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first scheduler did not stop")
	}
	time.Sleep(100 * time.Millisecond)
	stopSecond, _ := startHub()
	defer stopSecond()
	waitTCP(t, appAddr)
	waitNodes(t, appAddr, 1)

	client := &schedclient.Client{BaseURL: "http://" + appAddr, Token: "sched-secret"}
	done, err := client.CreateJob(context.Background(), schedclient.JobCreate{
		RepoID: "restart-worker", Prompt: "after restart",
	})
	if err != nil || done.Status != "finished" || done.Result != "restart-ok" {
		t.Fatalf("post-restart job = %+v, %v", done, err)
	}
}

func waitTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listen timeout: %s", addr)
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
