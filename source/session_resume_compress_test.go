// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

func TestCompressMarkerResumeSkipsPreSummary(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey:            "k",
		Model:             Model{ID: "m"},
		Local:             LocalOptions{CWD: dir},
		HistoryMaxChars:   200,
		HistoryKeepRecent: 2,
	}
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()

	// Seed a long history that will compress on next Send.
	for i := 0; i < 12; i++ {
		content := strings.Repeat("x", 40) + "-msg-" + string(rune('a'+i))
		if err := a.store.Append(id, store.Record{
			Type: store.TypeMessage, Role: "user", Content: content,
		}); err != nil {
			t.Fatal(err)
		}
		a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: content})
	}
	a.complete = scriptOnce{msg: llm.Message{Role: llm.RoleAssistant, Content: "ok after compress"}}

	run, err := a.Send(context.Background(), "final user turn")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil && res == nil {
		t.Fatal(err)
	}
	if res.Status != StatusFinished {
		t.Fatalf("%+v", res)
	}
	_ = a.Close()

	raw, err := os.ReadFile(filepath.Join(dir, ".anvil", "agents", id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"type":"summary"`) {
		t.Fatalf("expected summary marker in store:\n%s", raw)
	}

	resumed, err := Resume(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()

	if len(resumed.history) == 0 {
		t.Fatal("empty history")
	}
	if resumed.history[0].Name != compress.SummaryMarker {
		t.Fatalf("want summary marker first, got %+v", resumed.history[0])
	}
	// Pre-summary bulk should not all be present.
	preCount := 0
	for _, m := range resumed.history {
		if strings.Contains(m.Content, "-msg-") && m.Name != compress.SummaryMarker {
			preCount++
		}
	}
	if preCount > 4 {
		t.Fatalf("too many pre-summary messages retained: %d hist=%+v", preCount, resumed.history)
	}
}
