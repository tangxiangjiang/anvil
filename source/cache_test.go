// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"errors"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

type fixedResponse struct {
	resp *llm.Response
	err  error
}

func (f fixedResponse) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return f.resp, f.err
}

func TestRunReportsLastRequestAtAndCacheUsage(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()
	a.complete = fixedResponse{resp: &llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage: &llm.Usage{
			InputTokens: 30, OutputTokens: 4, TotalTokens: 34, CachedTokens: 20,
		},
	}}

	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage == nil || res.Usage.CachedTokens != 20 || res.Usage.TotalTokens != 34 {
		t.Fatalf("run usage = %+v", res.Usage)
	}
	if res.LastRequestAt.IsZero() {
		t.Fatal("LastRequestAt is zero after a successful request")
	}
	var terminal *Event
	for i := range events {
		if events[i].Type == EventDone {
			terminal = &events[i]
		}
	}
	if terminal == nil || terminal.LastRequestAt.IsZero() || !terminal.LastRequestAt.Equal(res.LastRequestAt) {
		t.Fatalf("terminal event = %+v, result anchor = %v", terminal, res.LastRequestAt)
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	st := store.New(dir)
	recs, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	var stored *store.Usage
	for _, rec := range recs {
		if rec.Type == store.TypeRunEnd {
			stored = rec.Usage
		}
	}
	if stored == nil || stored.CachedTokens != 20 || stored.TotalTokens != 34 {
		t.Fatalf("stored usage = %+v", stored)
	}
}

func TestRunThatNeverReachedTheProviderHasNoAnchor(t *testing.T) {
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = fixedResponse{err: errors.New("boom")}

	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, run)
	res, _ := run.Wait(context.Background())
	if res.Status != StatusError {
		t.Fatalf("%+v", res)
	}
	if !res.LastRequestAt.IsZero() {
		t.Fatalf("LastRequestAt = %v after a failed first call", res.LastRequestAt)
	}
	for _, ev := range events {
		if ev.Type == EventError && !ev.LastRequestAt.IsZero() {
			t.Fatalf("error event carries an anchor: %+v", ev)
		}
	}
}
