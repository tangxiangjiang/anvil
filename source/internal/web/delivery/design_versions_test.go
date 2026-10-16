// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestDesignVersionsApproveAndBootstrap(t *testing.T) {
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
		Worker:    WorkerRef{ID: "w_ver", Label: "ver", Fleet: "local", Node: "n1", ProjectRel: "ver"},
		Title:     "Versions eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor

	d, err := s.CreateDesign(CreateDesignInput{
		EngReqID: eng.ID, Major: maj, Minor: min, Title: "ADR Versions test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Options) != 0 {
		t.Fatalf("new designs should not seed options, got %d", len(d.Options))
	}
	if _, err := s.ApproveDesign(d.ID); err == nil {
		t.Fatal("approve without versions should fail")
	}

	v1, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "API skeleton"})
	if err != nil {
		t.Fatal(err)
	}
	if v1.SortOrder != 1 || v1.Status != "draft" {
		t.Fatalf("want draft v1, got %+v", v1)
	}
	gate := "Entry: empty API package. Exit: handlers compile."
	arch := "```mermaid\nflowchart LR\n  A-->B\n```"
	class := "package api; type Handler struct{}"
	_, err = s.PatchDesignVersion(d.ID, v1.ID, PatchDesignVersionInput{
		Gate: &gate, SubArchitecture: &arch, ClassDesign: &class,
	})
	if err != nil {
		t.Fatal(err)
	}

	v2, err := s.AddDesignVersion(d.ID, AddDesignVersionInput{Title: "Store wire"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PatchDesignVersion(d.ID, v2.ID, PatchDesignVersionInput{
		Gate: &gate, SubArchitecture: &arch, ClassDesign: &class,
	})
	if err != nil {
		t.Fatal(err)
	}

	d, err = s.ApproveDesign(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "approved" {
		t.Fatalf("status %s", d.Status)
	}

	// Eng-req patches aligned (patch 2 added for second Version).
	engs, err := s.listEngReqs()
	if err != nil {
		t.Fatal(err)
	}
	var under []ReqVersion
	for _, e := range engs {
		if e.ID != eng.ID {
			continue
		}
		for _, rv := range e.Versions {
			if rv.Major == maj && rv.Minor == min {
				under = append(under, rv)
			}
		}
	}
	if len(under) < 2 {
		t.Fatalf("want ≥2 patches after seed, got %d", len(under))
	}

	msID := fmt.Sprintf("ms_%s_%d_%d", eng.ID, maj, min)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_phases WHERE milestone_id=?`, msID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("want 2 canvas phases, got %d (ms=%s)", n, msID)
	}

	// Idempotent bootstrap
	if _, err := s.BootstrapDesignRoadmap(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_phases WHERE milestone_id=?`, msID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("bootstrap should not duplicate phases, got %d", n)
	}
}
