// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
)

// seededConversation writes the shape a real run leaves behind: messages
// between run boundaries, and a second turn after the first has ended. The
// source id is fixed so a test can assert a new id was generated for the fork.
func seededConversation(t *testing.T, dir string) string {
	t.Helper()
	id := "agent-source"
	st := store.New(dir)
	records := []store.Record{
		{Type: store.TypeMeta, AgentID: id, CWD: dir, Provider: "openai", Model: "test-model"},
		{Type: store.TypeMessage, Role: "user", Content: "Fix the flaky test"},
		{Type: store.TypeRunStart, RunID: "run-1"},
		{Type: store.TypeMessage, Role: "assistant", Content: "Looking at it."},
		{Type: store.TypeRunEnd, RunID: "run-1", Status: "finished"},
		{Type: store.TypeMessage, Role: "user", Content: "now the timeout too"},
		{Type: store.TypeRunStart, RunID: "run-2"},
		{Type: store.TypeMessage, Role: "assistant", Content: "Changed it."},
		{Type: store.TypeRunEnd, RunID: "run-2", Status: "finished"},
	}
	for _, rec := range records {
		if err := st.Append(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func forkOptions(dir string) ListOptions {
	return ListOptions{Local: LocalOptions{CWD: dir}}
}

// The boundary is the run: everything up to and including its `run_end` is
// copied in order, the turn after it is not, and the only record added is the
// title that tells the two rows apart.
func TestForkCopiesThroughTheRunEnd(t *testing.T) {
	dir := t.TempDir()
	sourceID := seededConversation(t, dir)
	before, err := store.New(dir).Load(sourceID)
	if err != nil {
		t.Fatal(err)
	}

	newID, err := ForkAgent(context.Background(), sourceID, "run-1", forkOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(newID, "agent-") || newID == sourceID {
		t.Fatalf("fork id = %q, want a fresh agent id", newID)
	}

	after, err := store.New(dir).Load(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the source was changed by a fork:\nbefore=%+v\nafter=%+v", before, after)
	}

	copied, err := store.New(dir).Load(newID)
	if err != nil {
		t.Fatal(err)
	}
	// Records 0..4 end at run-1's run_end; one title record follows them.
	if len(copied) != 6 {
		t.Fatalf("fork holds %d records, want 5 copied plus a title: %+v", len(copied), copied)
	}
	for i := 0; i < 5; i++ {
		want := before[i]
		got := copied[i]
		if want.Type == store.TypeMeta {
			// The one field a fork changes: the record has to name the file it
			// now lives in, or a listing and a resume disagree about the id.
			if got.AgentID != newID {
				t.Fatalf("copied meta agent id = %q, want %q", got.AgentID, newID)
			}
			want.AgentID = newID
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("record %d = %+v, want %+v", i, got, want)
		}
	}
	named := copied[len(copied)-1]
	if named.Type != store.TypeMeta || named.AgentID != newID || named.Title != "Fix the flaky test (fork)" {
		t.Fatalf("title record = %+v", named)
	}
}

// A fork is a conversation, not a file: it resumes with the prefix's messages
// and none of the source's later ones.
func TestForkResumesWithThePrefixHistory(t *testing.T) {
	dir := t.TempDir()
	sourceID := seededConversation(t, dir)
	newID, err := ForkAgent(context.Background(), sourceID, "run-1", forkOptions(dir))
	if err != nil {
		t.Fatal(err)
	}

	opts := Options{APIKey: "test-key-not-a-secret-for-assert", Model: Model{ID: "test-model"}, Local: LocalOptions{CWD: dir}}
	source, err := Resume(context.Background(), sourceID, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	fork, err := Resume(context.Background(), newID, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()

	if len(fork.history) != 2 {
		t.Fatalf("fork history = %+v, want the two messages through run-1", fork.history)
	}
	if !reflect.DeepEqual(fork.history, source.history[:2]) {
		t.Fatalf("fork history = %+v, want the source's prefix %+v", fork.history, source.history[:2])
	}
	if len(source.history) != 4 {
		t.Fatalf("the source's history changed: %+v", source.history)
	}
}

// The listing is how a person finds the fork again, so its row has to read as
// a branch of the conversation rather than as the same conversation.
func TestForkIsListedUnderADistinguishableTitle(t *testing.T) {
	dir := t.TempDir()
	sourceID := seededConversation(t, dir)
	newID, err := ForkAgent(context.Background(), sourceID, "run-2", forkOptions(dir))
	if err != nil {
		t.Fatal(err)
	}

	list, err := List(context.Background(), forkOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	var titles = map[string]string{}
	for _, info := range list {
		titles[info.ID] = info.Title
	}
	if titles[sourceID] != "Fix the flaky test" {
		t.Fatalf("source title = %q", titles[sourceID])
	}
	if titles[newID] != "Fix the flaky test (fork)" {
		t.Fatalf("fork title = %q", titles[newID])
	}
}

// A suffixed title is no use if the listing's own cut removes the suffix.
func TestForkTitleKeepsTheSuffixPastTheLimit(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	sourceID := "agent-long"
	long := strings.Repeat("测", 100)
	for _, rec := range []store.Record{
		{Type: store.TypeMeta, AgentID: sourceID},
		{Type: store.TypeMessage, Role: "user", Content: long},
		{Type: store.TypeRunStart, RunID: "run-1"},
		{Type: store.TypeRunEnd, RunID: "run-1", Status: "finished"},
	} {
		if err := st.Append(sourceID, rec); err != nil {
			t.Fatal(err)
		}
	}
	newID, err := ForkAgent(context.Background(), sourceID, "run-1", forkOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	info, err := Get(context.Background(), newID, forkOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(info.Title, " (fork)") {
		t.Fatalf("fork title = %q, want the suffix to survive the cut", info.Title)
	}
}

// A turn that is still running (or was killed before it could record an end)
// has no boundary to copy to, and a fork that guessed at one would resume from
// a half-recorded run.
func TestForkRefusesAnUnfinishedRun(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	id := "agent-open"
	for _, rec := range []store.Record{
		{Type: store.TypeMeta, AgentID: id},
		{Type: store.TypeRunStart, RunID: "run-open"},
		{Type: store.TypeMessage, Role: "user", Content: "still going"},
	} {
		if err := st.Append(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	idsBefore, err := st.ListIDs()
	if err != nil {
		t.Fatal(err)
	}

	_, err = ForkAgent(context.Background(), id, "run-open", forkOptions(dir))
	if err == nil {
		t.Fatal("expected a run that never ended to be refused")
	}
	if ae, ok := err.(*Error); !ok || ae.Retryable {
		t.Fatalf("want non-retryable *Error, got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "has not ended") {
		t.Fatalf("err = %v, want it to say the run has not ended", err)
	}
	idsAfter, err := st.ListIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(idsAfter) != len(idsBefore) {
		t.Fatalf("a refused fork left an agent behind: %v", idsAfter)
	}
}

func TestForkRefusesAMissingRunAndAgent(t *testing.T) {
	dir := t.TempDir()
	sourceID := seededConversation(t, dir)
	if _, err := ForkAgent(context.Background(), sourceID, "run-nope", forkOptions(dir)); err == nil {
		t.Fatal("expected an unknown run to be refused")
	}
	if _, err := ForkAgent(context.Background(), "agent-nope", "run-1", forkOptions(dir)); err == nil {
		t.Fatal("expected an unknown agent to be refused")
	}
	if _, err := ForkAgent(context.Background(), "", "run-1", forkOptions(dir)); err == nil {
		t.Fatal("expected an empty agent id to be refused")
	}
	if _, err := ForkAgent(context.Background(), sourceID, "", forkOptions(dir)); err == nil {
		t.Fatal("expected an empty run id to be refused")
	}
}

// The store backend is not the fork's concern; both kinds answer the same.
func TestForkWorksOverSQLite(t *testing.T) {
	dir := t.TempDir()
	opts := ListOptions{Local: LocalOptions{CWD: dir, Store: store.KindSQLite}}
	st, err := store.Open(dir, store.KindSQLite)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := "agent-sqlitefork"
	for _, rec := range []store.Record{
		{Type: store.TypeMeta, AgentID: sourceID},
		{Type: store.TypeMessage, Role: "user", Content: "Branch this"},
		{Type: store.TypeRunStart, RunID: "run-1"},
		{Type: store.TypeRunEnd, RunID: "run-1", Status: "finished"},
	} {
		if err := st.Append(sourceID, rec); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	newID, err := ForkAgent(context.Background(), sourceID, "run-1", opts)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Get(context.Background(), newID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Branch this (fork)" {
		t.Fatalf("title = %q", info.Title)
	}
}
