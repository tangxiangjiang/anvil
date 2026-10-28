// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

// writeThenFinish is a two-turn script: a write_file call, then a closing
// sentence. Both turns report usage, so the run boundary has something to carry.
type writeThenFinish struct{ n int }

func (c *writeThenFinish) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	if c.n == 1 {
		return &llm.Response{
			Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Name: "write_file",
				Args: json.RawMessage(`{"path":"note.txt","content":"hello"}`),
			}}},
			Usage: &llm.Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
		}, nil
	}
	return &llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Content: "wrote note.txt"},
		Usage:   &llm.Usage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25},
	}, nil
}

func TestTranscriptReadsAStoredConversation(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = &writeThenFinish{}

	ctx := context.Background()
	run, err := a.Send(ctx, "make me a note")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := LoadTranscript(ctx, a.AgentID(), TranscriptOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dropped != 0 {
		t.Fatalf("nothing was dropped, got %d", got.Dropped)
	}
	// The user turn, the assistant turn that called the tool, the tool's result,
	// the assistant turn that ended the run, and the run's boundary.
	if len(got.Entries) != 5 {
		t.Fatalf("entries=%d %+v", len(got.Entries), got.Entries)
	}
	runID := got.Entries[0].RunID
	if runID == "" {
		t.Fatalf("entries carry no run id: %+v", got.Entries)
	}
	for i, e := range got.Entries {
		if e.RunID != runID {
			t.Fatalf("entry %d belongs to run %q, want %q", i, e.RunID, runID)
		}
	}

	if e := got.Entries[0]; e.Kind != TranscriptMessage || e.Role != "user" || e.Content != "make me a note" {
		t.Fatalf("first entry = %+v", e)
	}
	call := got.Entries[1]
	if call.Role != "assistant" || len(call.ToolCalls) != 1 {
		t.Fatalf("second entry = %+v", call)
	}
	// The arguments are the whole point of carrying them: they are what a
	// replayed card opens its change from.
	if tc := call.ToolCalls[0]; tc.ID != "call-1" || tc.Name != "write_file" ||
		string(tc.Args) != `{"path":"note.txt","content":"hello"}` {
		t.Fatalf("tool call = %+v (%s)", tc, tc.Args)
	}
	if out := got.Entries[2]; out.Role != "tool" || out.ToolCallID != "call-1" ||
		out.Name != "write_file" || out.Content == "" {
		t.Fatalf("third entry = %+v", out)
	}
	if last := got.Entries[3]; last.Role != "assistant" || last.Content != "wrote note.txt" ||
		len(last.ToolCalls) != 0 {
		t.Fatalf("fourth entry = %+v", last)
	}
	end := got.Entries[4]
	if end.Kind != TranscriptRunEnd || end.Status != "finished" || end.Steps != 2 {
		t.Fatalf("run end = %+v", end)
	}
	if end.Usage == nil || end.Usage.TotalTokens != 39 {
		t.Fatalf("run usage = %+v", end.Usage)
	}
}

func TestTranscriptCapKeepsTheNewestRun(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	id := a.AgentID()

	msg := func(runID, role, content string) {
		t.Helper()
		if err := a.store.Append(id, store.Record{
			Type: store.TypeMessage, RunID: runID, Role: role, Content: content,
			Time: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	end := func(runID string) {
		t.Helper()
		if err := a.store.Append(id, store.Record{
			Type: store.TypeRunEnd, RunID: runID, Status: "finished", Steps: 1,
			Time: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A first run of four entries, then a second of three. Eight in all.
	msg("r1", "user", "first")
	msg("r1", "assistant", "one")
	msg("r1", "tool", "out")
	msg("r1", "assistant", "two")
	end("r1")
	msg("r2", "user", "second")
	msg("r2", "assistant", "three")
	end("r2")

	ctx := context.Background()
	// A cap that lands inside run one: the window is moved forward to the user
	// turn that opens run two, so a replay never opens on a result whose call
	// was cut, nor on a reply attributed to a question that scrolled off.
	got, err := LoadTranscript(ctx, id, TranscriptOptions{Local: LocalOptions{CWD: dir}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 3 || got.Dropped != 5 {
		t.Fatalf("entries=%d dropped=%d %+v", len(got.Entries), got.Dropped, got.Entries)
	}
	if e := got.Entries[0]; e.Role != "user" || e.Content != "second" {
		t.Fatalf("window opens on %+v", e)
	}
	if e := got.Entries[2]; e.Kind != TranscriptRunEnd || e.RunID != "r2" {
		t.Fatalf("window ends on %+v", e)
	}

	// A cap that lands inside run two, with no user turn in the window at all:
	// the window moves back to the turn that opened the run rather than opening
	// on the assistant's reply. The extra entries are reported as dropped.
	got, err = LoadTranscript(ctx, id, TranscriptOptions{Local: LocalOptions{CWD: dir}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 3 || got.Dropped != 5 {
		t.Fatalf("entries=%d dropped=%d %+v", len(got.Entries), got.Dropped, got.Entries)
	}
	if e := got.Entries[0]; e.Role != "user" || e.Content != "second" {
		t.Fatalf("window opens on %+v", e)
	}

	// A cap that already lands on the boundary reports the excess and no more.
	got, err = LoadTranscript(ctx, id, TranscriptOptions{Local: LocalOptions{CWD: dir}, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 3 || got.Dropped != 5 {
		t.Fatalf("entries=%d dropped=%d %+v", len(got.Entries), got.Dropped, got.Entries)
	}

	// A cap nobody reaches changes nothing.
	got, err = LoadTranscript(ctx, id, TranscriptOptions{Local: LocalOptions{CWD: dir}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 8 || got.Dropped != 0 {
		t.Fatalf("entries=%d dropped=%d", len(got.Entries), got.Dropped)
	}
}

// A store with no user turn at all has no boundary to move to, so the window is
// what it would have been: the cap's tail. Returning it beats returning nothing,
// and moving in either direction would invent a start the store does not have.
func TestTranscriptCapWithoutAUserTurnKeepsTheTail(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	id := a.AgentID()

	for _, role := range []string{"tool", "assistant"} {
		if err := a.store.Append(id, store.Record{
			Type: store.TypeMessage, RunID: "r1", Role: role, Content: role,
			Time: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadTranscript(context.Background(), id, TranscriptOptions{
		Local: LocalOptions{CWD: dir}, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Dropped != 1 {
		t.Fatalf("entries=%d dropped=%d", len(got.Entries), got.Dropped)
	}
	if e := got.Entries[0]; e.Role != "assistant" {
		t.Fatalf("window opens on %+v", e)
	}
}

func TestTranscriptUnknownAgentIsNotFound(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	_, err := LoadTranscript(ctx, "agent-nope", TranscriptOptions{Local: LocalOptions{CWD: dir}})
	if err == nil {
		t.Fatal("expected an unknown agent to be an error, not an empty transcript")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
	if ae, ok := err.(*Error); !ok || ae.Retryable {
		t.Fatalf("%T %v", err, err)
	}

	if _, err := LoadTranscript(ctx, "", TranscriptOptions{Local: LocalOptions{CWD: dir}}); err == nil {
		t.Fatal("expected an empty agent id to be an error")
	}
}

func TestTranscriptCarriesNoImageBytesAndNoKey(t *testing.T) {
	dir := t.TempDir()
	writeTestPNG(t, filepath.Join(dir, "shot.png"), 64, 48)
	const key = "sk-ant-api03-transcriptreadsnever000"
	a, err := Create(context.Background(), Options{
		APIKey: key, Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir}, Vision: VisionOptions{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = &readImageThenFinish{}

	ctx := context.Background()
	run, err := a.Send(ctx, "what is in this screenshot?",
		WithAttachments(ImageAttachment{Path: "shot.png"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()

	got, err := LoadTranscript(ctx, id, TranscriptOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) == 0 {
		t.Fatal("the run left nothing to read")
	}

	png, err := os.ReadFile(filepath.Join(dir, "shot.png"))
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(png)
	raw, err := os.ReadFile(filepath.Join(dir, ".anvil", "agents", id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	// Every entry is one of the two kinds the reader declares. Reasoning was
	// streamed and dropped rather than stored, so there is no third kind for it
	// to arrive under, and this is what says so.
	for _, e := range got.Entries {
		if e.Kind != TranscriptMessage && e.Kind != TranscriptRunEnd {
			t.Fatalf("unexpected entry kind %q", e.Kind)
		}
		texts := []string{e.Content, e.Status, e.Name}
		for _, tc := range e.ToolCalls {
			texts = append(texts, tc.Name, string(tc.Args))
		}
		for _, text := range texts {
			for _, banned := range []string{encoded, key, "base64,"} {
				if banned != "" && strings.Contains(text, banned) {
					t.Fatalf("entry %+v carries %q", e, banned)
				}
			}
		}
	}

	// And the store itself holds neither: the reader can only be trusted to
	// return what was never written in the first place.
	if strings.Contains(string(raw), encoded) {
		t.Fatal("the image's bytes are in the store")
	}
	if strings.Contains(string(raw), key) {
		t.Fatal("the API key is in the store")
	}
}
