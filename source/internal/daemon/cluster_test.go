// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

func TestClusterJoinHeartbeatAndAssign(t *testing.T) {
	t.Setenv("ANVIL_AGENTD_TOKEN", "local-token")
	t.Setenv("ANVIL_CLUSTER_TOKEN", "cluster-token")

	dir := t.TempDir()
	schedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer schedLn.Close()
	schedAddr := "tcp://" + schedLn.Addr().String()

	joined := make(chan struct{}, 1)
	hbSeen := make(chan struct{}, 1)
	doneCh := make(chan JobDoneBody, 1)

	var once sync.Once
	go func() {
		for {
			conn, err := schedLn.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				env, err := ReadEnvelope(conn)
				if err != nil || env.Kind != KindClusterJoin {
					return
				}
				var join ClusterJoinBody
				_ = DecodeBody(env, &join)
				if join.Token != "cluster-token" {
					return
				}
				okBody, _ := MarshalBody(ClusterJoinOKBody{LLM: &ClusterLLMBody{
					BaseURL: "http://127.0.0.1:8080/v1", APIKey: "gw-key", Model: "private/demo",
				}})
				if err := WriteEnvelope(conn, Envelope{Kind: KindClusterJoinOK, Body: okBody}); err != nil {
					return
				}
				once.Do(func() { joined <- struct{}{} })

				assigned := false
				for {
					msg, err := ReadEnvelope(conn)
					if err != nil {
						return
					}
					switch msg.Kind {
					case KindClusterHeartbeat:
						select {
						case hbSeen <- struct{}{}:
						default:
						}
						if !assigned {
							assigned = true
							body, _ := MarshalBody(JobAssignBody{
								JobID: "cj1", RepoID: "demo", Prompt: "from-cluster", ForceStub: true,
							})
							_ = WriteEnvelope(conn, Envelope{Kind: KindClusterAssign, Body: body})
						}
					case KindClusterDone:
						var d JobDoneBody
						_ = DecodeBody(msg, &d)
						doneCh <- d
						return
					}
				}
			}(conn)
		}
	}()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	localAddr := "tcp://" + ln.Addr().String()
	_ = ln.Close()

	cfg := DefaultConfig()
	cfg.Listen = localAddr
	cfg.MaxWorkers = 2
	cfg.Repos = []RepoEntry{{ID: "demo", CWD: dir}}
	cfg.Cluster = ClusterConfig{
		Enabled:      true,
		URL:          schedAddr,
		NodeID:       "node-a",
		TokenEnv:     "ANVIL_CLUSTER_TOKEN",
		HeartbeatSec: 1,
	}
	_ = cfg.Normalize()

	srv, err := NewServer(cfg, &EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	edge := NewClusterEdge(cfg, srv, nil)
	go func() { _ = edge.Run(ctx) }()

	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("join timeout")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		llm, ok := srv.RunnerLLM()
		if ok && llm.BaseURL == "http://127.0.0.1:8080/v1" && llm.APIKey == "gw-key" && llm.Model == "private/demo" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("llm not applied: ok=%v %+v", ok, llm)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case <-hbSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("heartbeat timeout")
	}
	select {
	case d := <-doneCh:
		if d.Status != "finished" || d.Result != "stub:from-cluster" {
			t.Fatalf("%+v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("assign done timeout")
	}
}
