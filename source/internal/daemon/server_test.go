// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"net"
	"testing"
	"time"
)

func startTestServer(t *testing.T, maxWorkers int, repos []RepoEntry) (*Server, string, context.CancelFunc) {
	t.Helper()
	t.Setenv("ANVIL_AGENTD_TOKEN", "test-token")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "tcp://" + ln.Addr().String()
	_ = ln.Close()

	cfg := DefaultConfig()
	cfg.Listen = addr
	cfg.MaxWorkers = maxWorkers
	cfg.Repos = repos
	cfg.NodeID = "test-node"
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, &EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if err := srv.SeedBoundWorker(r.ID, r.CWD); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()
	// Wait until accept works.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := Dial(addr)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	return srv, addr, cancel
}

func TestHelloAndWorkerList(t *testing.T) {
	dir := t.TempDir()
	_, addr, _ := startTestServer(t, 0, []RepoEntry{{ID: "demo", CWD: dir}})

	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	ok, err := cl.Hello("test-token", "unit")
	if err != nil {
		t.Fatal(err)
	}
	if ok.MaxWorkers != 1 {
		t.Fatalf("max=%d", ok.MaxWorkers)
	}
	list, err := cl.WorkerList()
	if err != nil {
		t.Fatal(err)
	}
	if list.Idle != 1 || list.Busy != 0 {
		t.Fatalf("list %+v", list)
	}
}

func TestHelloStartsWithZeroWorkers(t *testing.T) {
	_, addr, _ := startTestServer(t, 0, nil)
	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	ok, err := cl.Hello("test-token", "unit")
	if err != nil {
		t.Fatal(err)
	}
	if ok.MaxWorkers != 0 {
		t.Fatalf("max=%d", ok.MaxWorkers)
	}
	list, err := cl.WorkerList()
	if err != nil {
		t.Fatal(err)
	}
	if list.Idle != 0 || len(list.Workers) != 0 {
		t.Fatalf("list %+v", list)
	}
}

func TestHelloRejectsBadToken(t *testing.T) {
	dir := t.TempDir()
	_, addr, _ := startTestServer(t, 1, []RepoEntry{{ID: "demo", CWD: dir}})
	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	if _, err := cl.Hello("wrong", "unit"); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestAssignUnknownRepo(t *testing.T) {
	dir := t.TempDir()
	_, addr, _ := startTestServer(t, 1, []RepoEntry{{ID: "demo", CWD: dir}})
	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	if _, err := cl.Hello("test-token", "unit"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Assign(JobAssignBody{JobID: "j1", RepoID: "missing", Prompt: "x", ForceStub: true}); err != nil {
		t.Fatal(err)
	}
	env, err := cl.ReadMsg()
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != KindError {
		t.Fatalf("kind=%s", env.Kind)
	}
}

type hangRunner struct {
	started chan struct{}
}

func (h hangRunner) Run(ctx context.Context, job JobAssignBody, cwd string, emit func(JobEventBody)) JobDoneBody {
	close(h.started)
	<-ctx.Done()
	return JobDoneBody{JobID: job.JobID, Status: "cancelled", Code: "cancelled"}
}

func TestCapacityReject(t *testing.T) {
	t.Setenv("ANVIL_AGENTD_TOKEN", "test-token")
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "tcp://" + ln.Addr().String()
	_ = ln.Close()

	cfg := DefaultConfig()
	cfg.Listen = addr
	cfg.MaxWorkers = 0
	_ = cfg.Normalize()

	started := make(chan struct{})
	srv, err := NewServer(cfg, hangRunner{started: started}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SeedBoundWorker("demo", dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	if _, err := cl.Hello("test-token", "u"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Assign(JobAssignBody{JobID: "j1", RepoID: "demo", Prompt: "a", ForceStub: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not start")
	}
	if err := cl.Assign(JobAssignBody{JobID: "j2", RepoID: "demo", Prompt: "b", ForceStub: true}); err != nil {
		t.Fatal(err)
	}
	env, err := cl.ReadMsg()
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != KindError {
		t.Fatalf("expected capacity error, got %s", env.Kind)
	}
}

func TestAssignStubDone(t *testing.T) {
	dir := t.TempDir()
	_, addr, _ := startTestServer(t, 2, []RepoEntry{{ID: "demo", CWD: dir}})
	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	if _, err := cl.Hello("test-token", "u"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Assign(JobAssignBody{JobID: "j1", RepoID: "demo", Prompt: "hello", ForceStub: true}); err != nil {
		t.Fatal(err)
	}
	var sawDone bool
	deadline := time.After(3 * time.Second)
	for !sawDone {
		select {
		case <-deadline:
			t.Fatal("timeout waiting job.done")
		default:
		}
		env, err := cl.ReadMsg()
		if err != nil {
			t.Fatal(err)
		}
		switch env.Kind {
		case KindJobEvent:
			continue
		case KindJobDone:
			var d JobDoneBody
			_ = DecodeBody(env, &d)
			if d.Status != "finished" || d.Result != "stub:hello" {
				t.Fatalf("%+v", d)
			}
			sawDone = true
		case KindError:
			t.Fatalf("error envelope: %s", string(env.Body))
		}
	}
}

func TestCancelStubJob(t *testing.T) {
	t.Setenv("ANVIL_AGENTD_TOKEN", "test-token")
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "tcp://" + ln.Addr().String()
	_ = ln.Close()

	cfg := DefaultConfig()
	cfg.Listen = addr
	cfg.MaxWorkers = 0
	_ = cfg.Normalize()
	started := make(chan struct{})
	srv, err := NewServer(cfg, hangRunner{started: started}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SeedBoundWorker("demo", dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := NewClient(conn)
	_, _ = cl.Hello("test-token", "u")
	_ = cl.Assign(JobAssignBody{JobID: "j1", RepoID: "demo", Prompt: "x"})
	<-started
	if err := cl.Cancel("j1"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout")
		default:
		}
		env, err := cl.ReadMsg()
		if err != nil {
			t.Fatal(err)
		}
		if env.Kind == KindJobDone {
			var d JobDoneBody
			_ = DecodeBody(env, &d)
			if d.Status != "cancelled" {
				t.Fatalf("%+v", d)
			}
			return
		}
	}
}
