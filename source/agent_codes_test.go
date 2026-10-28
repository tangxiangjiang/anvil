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

func TestMaxStepsCode(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey:   "k",
		Model:    Model{ID: "m"},
		Local:    LocalOptions{CWD: dir},
		MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = alwaysToolCompleter{}

	run, err := a.Send(context.Background(), "loop")
	if err != nil {
		t.Fatal(err)
	}
	var errCode Code
	for ev := range run.Stream(context.Background()) {
		if ev.Type == EventError {
			errCode = ev.Code
		}
	}
	res, err := run.Wait(context.Background())
	if res == nil {
		t.Fatalf("nil result: %v", err)
	}
	if res.Status != StatusError || res.Code != CodeMaxSteps {
		t.Fatalf("want error/max_steps got status=%s code=%s err=%q waitErr=%v", res.Status, res.Code, res.Err, err)
	}
	if errCode != CodeMaxSteps {
		t.Fatalf("stream code=%s", errCode)
	}
	if !IsMaxStepsExceededResult(res) {
		t.Fatal("IsMaxStepsExceededResult")
	}
	if ResultCode(res) != CodeMaxSteps {
		t.Fatalf("ResultCode=%s", ResultCode(res))
	}
}

type alwaysToolCompleter struct{}

func (alwaysToolCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	return &llm.Response{Message: llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:   "1",
			Name: "read_file",
			Args: []byte(`{"path":"missing.txt"}`),
		}},
	}}, nil
}

func TestCancelCode(t *testing.T) {
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

	run, err := a.Send(context.Background(), "block")
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
	if res.Status != StatusCancelled || res.Code != CodeCancelled {
		t.Fatalf("status=%s code=%s err=%q", res.Status, res.Code, res.Err)
	}
}

func TestMaxWallTimeCode(t *testing.T) {
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
	if res.Status != StatusCancelled || res.Code != CodeMaxWallTime {
		t.Fatalf("status=%s code=%s err=%q", res.Status, res.Code, res.Err)
	}
	if !runtime.IsMaxWallTime(res.Err) {
		t.Fatalf("err %q", res.Err)
	}
}

func TestParentContextDeadlineCode(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
		// No MaxWallTime — parent deadline alone.
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = blockingCompleter{}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	run, err := a.Send(ctx, "slow")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCancelled || res.Code != CodeContextDeadline {
		t.Fatalf("status=%s code=%s err=%q", res.Status, res.Code, res.Err)
	}
}

func TestLoopCancelCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	reg := tools.NewRegistry(t.TempDir(), false)
	res := runtime.Loop(ctx, blockingCompleter{}, reg, runtime.Config{
		CWD: t.TempDir(), Model: "m", UserMsg: "x", MaxSteps: 4,
	}, nil)
	if res.Status != "cancelled" || res.Code != runtime.CodeCancelled {
		t.Fatalf("%+v", res)
	}
}

func TestFormatRunStart(t *testing.T) {
	got := FormatRunStart("agent-1", "run-2")
	if got != "agent=agent-1 run=run-2" {
		t.Fatalf("%q", got)
	}
	end := FormatRunEnd(&RunResult{Status: StatusError, Code: CodeMaxSteps, Steps: 3})
	if end != "status=error code=max_steps steps=3" {
		t.Fatalf("%q", end)
	}
}

func TestProviderCode(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = failCompleter{msg: "boom upstream"}

	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if res == nil {
		t.Fatalf("nil result: %v", err)
	}
	if res.Status != StatusError || res.Code != CodeProvider {
		t.Fatalf("%+v waitErr=%v", res, err)
	}
}

type failCompleter struct{ msg string }

func (f failCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	return nil, errString(f.msg)
}

type errString string

func (e errString) Error() string { return string(e) }
