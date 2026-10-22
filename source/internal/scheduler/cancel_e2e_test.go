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
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/scheduler"
)

type cancelRunner struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (r *cancelRunner) Run(ctx context.Context, job daemon.JobAssignBody, _ string, _ func(daemon.JobEventBody)) daemon.JobDoneBody {
	close(r.started)
	<-ctx.Done()
	close(r.cancelled)
	return daemon.JobDoneBody{JobID: job.JobID, Status: "cancelled", Code: "cancelled"}
}

func TestE2ECancelReachesNode(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "sched-secret")

	appAddr := freeTCP(t)
	clusterHost := freeTCP(t)
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
	cancelWaitHTTP(t, appAddr)

	dir := t.TempDir()
	agentCfg := daemon.DefaultConfig()
	agentCfg.Listen = "tcp://" + freeTCP(t)
	agentCfg.Cluster = daemon.ClusterConfig{
		Enabled:      true,
		URL:          "tcp://" + clusterHost,
		NodeID:       "node-cancel",
		TokenEnv:     "ANVIL_SCHEDULER_TOKEN",
		HeartbeatSec: 1,
	}
	if err := agentCfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runner := &cancelRunner{started: make(chan struct{}), cancelled: make(chan struct{})}
	srv, err := daemon.NewServer(agentCfg, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SeedBoundWorker("cancel-repo", dir); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	go func() { _ = daemon.NewClusterEdge(agentCfg, srv, nil).Run(ctx) }()
	waitNodes(t, appAddr, 1)

	type jobResponse struct {
		status int
		body   []byte
		err    error
	}
	jobResult := make(chan jobResponse, 1)
	go func() {
		raw, _ := json.Marshal(map[string]any{
			"job_id": "cancel-e2e", "repo_id": "cancel-repo", "prompt": "wait",
		})
		req, _ := http.NewRequest(http.MethodPost, "http://"+appAddr+"/v1/jobs", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer sched-secret")
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			jobResult <- jobResponse{err: err}
			return
		}
		defer res.Body.Close()
		body, readErr := io.ReadAll(res.Body)
		jobResult <- jobResponse{status: res.StatusCode, body: body, err: readErr}
	}()

	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("node job did not start")
	}

	cancelReq, _ := http.NewRequest(http.MethodPost, "http://"+appAddr+"/v1/jobs/cancel-e2e/cancel", nil)
	cancelReq.Header.Set("Authorization", "Bearer sched-secret")
	cancelRes, err := http.DefaultClient.Do(cancelReq)
	if err != nil {
		t.Fatal(err)
	}
	cancelBody, _ := io.ReadAll(cancelRes.Body)
	_ = cancelRes.Body.Close()
	if cancelRes.StatusCode != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelRes.StatusCode, cancelBody)
	}

	select {
	case <-runner.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("job.cancel did not reach the node runner")
	}
	select {
	case got := <-jobResult:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.status != http.StatusOK {
			t.Fatalf("job status=%d body=%s", got.status, got.body)
		}
		var done daemon.JobDoneBody
		if err := json.Unmarshal(got.body, &done); err != nil {
			t.Fatal(err)
		}
		if done.Status != "cancelled" {
			t.Fatalf("job done = %+v", done)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled job did not finish")
	}
}

func cancelWaitHTTP(t *testing.T, appAddr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", appAddr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("app listen timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
