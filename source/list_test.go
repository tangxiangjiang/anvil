// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

func TestListAndGet(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()
	_ = a.Close()

	list, err := List(context.Background(), ListOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("%+v", list)
	}
	if list[0].Model != "m" {
		t.Fatalf("model=%q", list[0].Model)
	}

	info, err := Get(context.Background(), id, ListOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != id {
		t.Fatalf("%+v", info)
	}

	_, err = Get(context.Background(), "agent-missing", ListOptions{Local: LocalOptions{CWD: dir}})
	if err == nil {
		t.Fatal("expected missing")
	}
	if ae, ok := err.(*Error); !ok || ae.Retryable {
		t.Fatalf("%T %v", err, err)
	}
}

func TestUsageAggregationFromStore(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	id := a.AgentID()
	_ = a.store.Append(id, store.Record{
		Type: store.TypeRunEnd, RunID: "r1", Status: "finished", Steps: 1,
		Usage: &store.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		Time:  time.Now().UTC(),
	})
	_ = a.store.Append(id, store.Record{
		Type: store.TypeRunEnd, RunID: "r2", Status: "finished", Steps: 2,
		Usage: &store.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5},
		Time:  time.Now().UTC(),
	})

	info, err := Get(context.Background(), id, ListOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Runs != 2 || info.LastStatus != "finished" {
		t.Fatalf("%+v", info)
	}
	if info.Usage == nil || info.Usage.TotalTokens != 20 || info.Usage.InputTokens != 13 {
		t.Fatalf("%+v", info.Usage)
	}
	u, err := a.Usage()
	if err != nil || u.TotalTokens != 20 {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestListCarriesTheTitle(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	id := a.AgentID()
	_ = a.store.Append(id, store.Record{
		Type: store.TypeMessage, Role: "user", Content: "看一下这是个什么工程",
		Time: time.Now().UTC(),
	})

	list, err := List(context.Background(), ListOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "看一下这是个什么工程" {
		t.Fatalf("%+v", list)
	}
}

func TestRunResultUsageFromMock(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = usageCompleter{}
	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 7 {
		t.Fatalf("%+v", res.Usage)
	}
}

// The run's usage reaches the event stream, not only Wait's result: a client
// reading events is entitled to the same count as one reading the result, and
// the terminal event is the only place it can say so.
func TestTerminalEventCarriesUsage(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = usageCompleter{}
	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	var done *Event
	for ev := range run.Stream(context.Background()) {
		if ev.Type == EventDone {
			e := ev
			done = &e
		}
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done == nil {
		t.Fatal("the stream carried no done event")
	}
	if done.Usage == nil || done.Usage.TotalTokens != 7 {
		t.Fatalf("done usage=%+v want total 7", done.Usage)
	}
}

type usageCompleter struct{}

func (usageCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:   &llm.Usage{InputTokens: 4, OutputTokens: 3, TotalTokens: 7},
	}, nil
}
