// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
)

func newStoredAgent(t *testing.T, dir string) string {
	t.Helper()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	id := a.AgentID()
	if err := a.store.Append(id, store.Record{
		Type: store.TypeMessage, Role: "user", Content: "Fix the flaky test",
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRenameAgentNamesASessionInTheListing(t *testing.T) {
	dir := t.TempDir()
	id := newStoredAgent(t, dir)
	opts := ListOptions{Local: LocalOptions{CWD: dir}}

	before, err := Get(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	if before.Title != "Fix the flaky test" {
		t.Fatalf("title before=%q", before.Title)
	}

	if err := RenameAgent(context.Background(), id, "  Timeout work  ", opts); err != nil {
		t.Fatal(err)
	}
	after, err := Get(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "Timeout work" {
		t.Fatalf("title after=%q", after.Title)
	}

	// A rename is one appended record. Everything written before it stays, so a
	// resume replays the same history it did before the name changed.
	recs, err := store.New(dir).Load(id)
	if err != nil {
		t.Fatal(err)
	}
	last := recs[len(recs)-1]
	if last.Type != store.TypeMeta || last.Title != "Timeout work" {
		t.Fatalf("last record=%+v", last)
	}
	if recs[1].Content != "Fix the flaky test" {
		t.Fatalf("the first message was rewritten: %+v", recs[1])
	}
}

func TestRenameAgentRefusesAnEmptyName(t *testing.T) {
	dir := t.TempDir()
	id := newStoredAgent(t, dir)
	err := RenameAgent(context.Background(), id, "  \n\t ", ListOptions{Local: LocalOptions{CWD: dir}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if ae, ok := err.(*Error); !ok || ae.Retryable {
		t.Fatalf("%T %v", err, err)
	}
}

func TestRenameAgentRefusesAMissingSession(t *testing.T) {
	dir := t.TempDir()
	err := RenameAgent(context.Background(), "agent-missing", "name", ListOptions{Local: LocalOptions{CWD: dir}})
	if err == nil {
		t.Fatal("expected an error")
	}
	// Naming nothing would create a session holding only a title.
	if store.New(dir).Exists("agent-missing") {
		t.Fatal("rename created a session")
	}
}

func TestDeleteAgentForgetsTheSession(t *testing.T) {
	dir := t.TempDir()
	id := newStoredAgent(t, dir)
	opts := ListOptions{Local: LocalOptions{CWD: dir}}

	if err := DeleteAgent(context.Background(), id, opts); err != nil {
		t.Fatal(err)
	}
	list, err := List(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
	if _, err := Get(context.Background(), id, opts); err == nil {
		t.Fatal("expected the session to be gone")
	}
	// Asking twice is an error, not a silent success.
	if err := DeleteAgent(context.Background(), id, opts); err == nil {
		t.Fatal("expected a second delete to report the session missing")
	}
}

func TestSessionAdminWorksOverSQLite(t *testing.T) {
	dir := t.TempDir()
	opts := ListOptions{Local: LocalOptions{CWD: dir, Store: store.KindSQLite}}
	st, err := store.Open(dir, store.KindSQLite)
	if err != nil {
		t.Fatal(err)
	}
	id := "agent-sqliteadmin"
	if err := st.Append(id, store.Record{Type: store.TypeMeta, AgentID: id, CWD: dir}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	if err := RenameAgent(context.Background(), id, "Named", opts); err != nil {
		t.Fatal(err)
	}
	info, err := Get(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Named" {
		t.Fatalf("title=%q", info.Title)
	}
	if err := DeleteAgent(context.Background(), id, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(context.Background(), id, opts); err == nil {
		t.Fatal("expected the session to be gone")
	}
}
