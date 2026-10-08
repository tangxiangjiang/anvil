package store_test

import (
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestSQLiteRoundTripAndList(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id := "agent-sqlite1"
	if err := st.Append(id, store.Record{
		Type: store.TypeMeta, AgentID: id, CWD: dir, Provider: "openai", Model: "m",
		Time: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(id, store.Record{
		Type: store.TypeMessage, Role: "user", Content: "hi",
		Time: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(id, store.Record{
		Type: store.TypeRunEnd, RunID: "run-1", Status: "finished", Steps: 1,
		Usage: &store.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		Time:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	recs, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("len=%d", len(recs))
	}
	ids, err := st.ListIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("%v", ids)
	}
	if !st.Exists(id) {
		t.Fatal("expected exists")
	}
	sum := store.Summarize(recs)
	if sum.Runs != 1 || sum.Usage == nil || sum.Usage.TotalTokens != 5 {
		t.Fatalf("%+v", sum)
	}
}

func TestOpenFactory(t *testing.T) {
	dir := t.TempDir()
	b, err := store.Open(dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Append("agent-x", store.Record{Type: store.TypeMeta, AgentID: "agent-x", CWD: dir}); err != nil {
		t.Fatal(err)
	}
}
