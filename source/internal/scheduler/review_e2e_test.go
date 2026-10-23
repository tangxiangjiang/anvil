// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/scheduler"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

func TestReviewE2ESchedulerToAgentdSandbox(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "sched-secret")
	appAddr, clusterAddr := freeTCP(t), freeTCP(t)
	cfg := scheduler.DefaultConfig()
	cfg.ListenApp = appAddr
	cfg.ListenCluster = "tcp://" + clusterAddr
	cfg.TokenEnv = "ANVIL_SCHEDULER_TOKEN"
	cfg.StaleSec = 5
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := scheduler.NewHub(cfg, nil)
	go func() { _ = hub.Run(ctx) }()
	waitTCP(t, appAddr)

	sandbox := t.TempDir()
	repo := filepath.Join(sandbox, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "review@example.invalid")
	run("config", "user.name", "Review")
	if err := os.WriteFile(filepath.Join(repo, "review.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "review.txt")
	run("commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "review.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	agentCfg := daemon.DefaultConfig()
	agentCfg.Listen = "tcp://" + freeTCP(t)
	agentCfg.SandboxDir = sandbox
	agentCfg.Cluster = daemon.ClusterConfig{
		Enabled: true, URL: "tcp://" + clusterAddr, NodeID: "review-node",
		TokenEnv: "ANVIL_SCHEDULER_TOKEN", HeartbeatSec: 1,
	}
	if err := agentCfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	server, err := daemon.NewServer(agentCfg, &daemon.EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SeedBoundWorker("review-worker", repo); err != nil {
		t.Fatal(err)
	}
	go func() { _ = daemon.NewClusterEdge(agentCfg, server, nil).Run(ctx) }()
	waitNodes(t, appAddr, 1)
	deadline := time.Now().Add(3 * time.Second)
	for {
		nodes, _ := getJSON(t, appAddr, "/v1/nodes")["nodes"].([]any)
		if len(nodes) > 0 {
			node, _ := nodes[0].(map[string]any)
			caps, _ := node["capabilities"].([]any)
			for _, cap := range caps {
				if cap == "sandbox.review.read" {
					goto ready
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("review capability heartbeat timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
ready:

	client := &schedclient.Client{BaseURL: "http://" + appAddr, Token: "sched-secret"}
	callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	raw, err := client.ReadWorkerFile(callCtx, "review-node", "review-worker", "review.txt")
	if err != nil {
		t.Fatal(err)
	}
	var read daemon.ClusterSandboxResultBody
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	if !read.OK || read.Content != "after\n" {
		t.Fatalf("read = %+v", read)
	}
	raw, err = client.GitStatus(callCtx, "review-node", "review-worker")
	if err != nil {
		t.Fatal(err)
	}
	var status daemon.ClusterSandboxResultBody
	_ = json.Unmarshal(raw, &status)
	if !status.OK || len(status.Status) != 1 || status.Status[0].Path != "review.txt" {
		t.Fatalf("status = %+v", status)
	}
	raw, err = client.GitDiff(callCtx, "review-node", "review-worker", "review.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	var diff daemon.ClusterSandboxResultBody
	_ = json.Unmarshal(raw, &diff)
	if !diff.OK || !strings.Contains(diff.Diff, "+after") {
		t.Fatalf("diff = %+v", diff)
	}
	_, err = client.ReadWorkerFile(callCtx, "missing-node", "review-worker", "review.txt")
	var httpErr *schedclient.HTTPError
	if !errors.As(err, &httpErr) || !strings.Contains(string(httpErr.Body), `"code":"node_unavailable"`) {
		t.Fatalf("missing node error = %#v", err)
	}
}
