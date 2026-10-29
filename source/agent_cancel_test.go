// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

type blockingCompleter struct{}

func (blockingCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "too late"}}, nil
	}
}

func TestRunCancel(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = blockingCompleter{}

	run, err := a.Send(context.Background(), "block please")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := run.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCancelled {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if res.Code != CodeCancelled {
		t.Fatalf("code=%s", res.Code)
	}
}

func TestMaxWallTime(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey:      "k",
		Model:       Model{ID: "m"},
		Local:       LocalOptions{CWD: dir},
		MaxWallTime: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = blockingCompleter{}

	run, err := a.Send(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCancelled {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if res.Code != CodeMaxWallTime {
		t.Fatalf("code=%s err=%q", res.Code, res.Err)
	}
	if res.Err != "max wall time exceeded" {
		t.Fatalf("want wall-time message, got %q", res.Err)
	}
}

func TestLoopCancelMidStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	reg := tools.NewRegistry(t.TempDir(), false)
	res := runtime.Loop(ctx, blockingCompleter{}, reg, runtime.Config{
		CWD:      t.TempDir(),
		Model:    "m",
		UserMsg:  "x",
		MaxSteps: 4,
	}, nil)
	if res.Status != "cancelled" || res.Code != runtime.CodeCancelled {
		t.Fatalf("%+v", res)
	}
}

func TestZeroMaxWallTimeUnchanged(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = scriptOnce{msg: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}
	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished || res.Result != "ok" {
		t.Fatalf("%+v", res)
	}
}

type scriptOnce struct{ msg llm.Message }

func (s scriptOnce) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return &llm.Response{Message: s.msg}, nil
}
