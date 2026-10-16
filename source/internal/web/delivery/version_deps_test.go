// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestVersionDependsTopoAndCycle(t *testing.T) {
	dir := t.TempDir()
	as, err := auth.Open(filepath.Join(dir, "web.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer as.Close()
	s, err := Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}

	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker:    WorkerRef{ID: "w_dep", Label: "dep", Fleet: "local", Node: "n1", ProjectRel: "dep"},
		Title:     "Deps eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	d, err := s.CreateDesign(CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "Deps design",
	})
	if err != nil {
		t.Fatal(err)
	}
	gate, arch, class := "g", "a", "c"
	v1, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "Base"})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "Mid"})
	if err != nil {
		t.Fatal(err)
	}
	v3, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "Top"})
	if err != nil {
		t.Fatal(err)
	}
	fill := func(id string) {
		if _, err := s.PatchDesignVersion(d.ID, id, PatchDesignVersionInput{
			Gate: &gate, SubArchitecture: &arch, ClassDesign: &class,
		}); err != nil {
			t.Fatal(err)
		}
	}
	fill(v1.ID)
	fill(v2.ID)
	fill(v3.ID)

	// v3 depends on v2, v2 depends on v1 → topo v1, v2, v3
	deps2 := []string{v1.ID}
	if _, err := s.PatchDesignVersion(d.ID, v2.ID, PatchDesignVersionInput{DependsOn: &deps2}); err != nil {
		t.Fatal(err)
	}
	deps3 := []string{v2.ID}
	if _, err := s.PatchDesignVersion(d.ID, v3.ID, PatchDesignVersionInput{DependsOn: &deps3}); err != nil {
		t.Fatal(err)
	}

	// Cycle: v1 → v3
	bad := []string{v3.ID}
	if _, err := s.PatchDesignVersion(d.ID, v1.ID, PatchDesignVersionInput{DependsOn: &bad}); err == nil {
		t.Fatal("expected cycle rejection")
	}

	d, err = s.ApproveDesign(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "approved" {
		t.Fatalf("status %s", d.Status)
	}
	// After approve, sort_order follows topo
	byID := map[string]DesignVersion{}
	for _, v := range d.Versions {
		byID[v.ID] = v
	}
	if byID[v1.ID].SortOrder != 1 || byID[v2.ID].SortOrder != 2 || byID[v3.ID].SortOrder != 3 {
		t.Fatalf("want topo sort_order 1,2,3 got v1=%d v2=%d v3=%d",
			byID[v1.ID].SortOrder, byID[v2.ID].SortOrder, byID[v3.ID].SortOrder)
	}
	if len(byID[v2.ID].DependsOn) != 1 || byID[v2.ID].DependsOn[0] != v1.ID {
		t.Fatalf("v2 depends_on: %+v", byID[v2.ID].DependsOn)
	}
}

func TestTopoSortParallel(t *testing.T) {
	vers := []DesignVersion{
		{ID: "c", SortOrder: 3, DependsOn: []string{"a", "b"}},
		{ID: "a", SortOrder: 2},
		{ID: "b", SortOrder: 1},
	}
	out, err := topoSortVersions(vers)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("len %d", len(out))
	}
	// b (sort 1) and a (sort 2) first, then c
	if out[0].ID != "b" || out[1].ID != "a" || out[2].ID != "c" {
		t.Fatalf("order %+v", []string{out[0].ID, out[1].ID, out[2].ID})
	}
}
