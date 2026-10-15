// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package workspace_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/workspace"
)

func TestWorkspaceProjectGraphAndBindings(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(filepath.Join(dir, "w.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	wsStore, err := workspace.Open(store.DB())
	if err != nil {
		t.Fatal(err)
	}

	ws, err := wsStore.CreateWorkspace("local", "Demo", "sum")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Token == "" || !hasPrefix(ws.Token, "ws_") {
		t.Fatalf("token=%q", ws.Token)
	}

	a, err := wsStore.CreateProject(workspace.CreateProjectInput{
		WorkspaceID: ws.ID, Key: "a", Name: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := wsStore.CreateProject(workspace.CreateProjectInput{
		WorkspaceID: ws.ID, Key: "b", Name: "B", DependsOn: []string{a.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.DependsOn) != 1 || b.DependsOn[0] != a.ID {
		t.Fatalf("deps=%v", b.DependsOn)
	}

	if err := wsStore.BindWorker("w1", b.ID, "local", "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := wsStore.SetWorkerDeps("w1", []string{"w0"}); err != nil {
		t.Fatal(err)
	}
	deps, err := wsStore.ListWorkerDeps("w1")
	if err != nil || len(deps) != 1 || deps[0] != "w0" {
		t.Fatalf("worker deps=%v err=%v", deps, err)
	}

	g, err := wsStore.Graph(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Projects) != 2 || len(g.Edges) != 1 || len(g.Workers) != 1 {
		t.Fatalf("graph %+v", g)
	}
	if g.Edges[0].From != b.ID || g.Edges[0].To != a.ID {
		t.Fatalf("edge %+v", g.Edges[0])
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
