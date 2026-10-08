package anvil

import (
	"context"
	"testing"
)

func TestSQLiteCreateResumeList(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir, Store: "sqlite"},
	}
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()
	_ = a.Close()

	b, err := Resume(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.AgentID() != id {
		t.Fatalf("%s vs %s", b.AgentID(), id)
	}

	list, err := List(context.Background(), ListOptions{Local: LocalOptions{CWD: dir, Store: "sqlite"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("%+v", list)
	}
	info, err := Get(context.Background(), id, ListOptions{Local: LocalOptions{CWD: dir, Store: "sqlite"}})
	if err != nil || info.ID != id {
		t.Fatalf("%+v %v", info, err)
	}
}
