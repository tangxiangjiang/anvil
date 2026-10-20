// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestSnapshotSeedAndApprove(t *testing.T) {
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
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Majors) < 2 {
		t.Fatalf("expected major plan seed, got %+v", snap.Majors)
	}
	if snap.Majors[0].Major != 0 || snap.Majors[0].Status != MajorDeveloping {
		t.Fatalf("want major 0 developing, got %+v", snap.Majors[0])
	}
	if len(snap.EngReqs) == 0 {
		t.Fatal("expected seed eng req")
	}
	if len(snap.EngReqs[0].Versions) < 2 {
		t.Fatalf("expected versions, got %d", len(snap.EngReqs[0].Versions))
	}
	if len(snap.Designs) == 0 || len(snap.Roadmap.Milestones) == 0 {
		t.Fatal("expected designs and milestones")
	}

	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker:    WorkerRef{ID: "w_demo", Label: "demo", Fleet: "local", Node: "n1", ProjectRel: "demo"},
		Title:     "Demo eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(eng.Versions) != 1 || eng.Versions[0].Major != 0 || eng.Versions[0].Minor != 1 || eng.Versions[0].Patch != 1 {
		t.Fatalf("want first milestone 0.1.1, got %+v", eng.Versions)
	}
	eng2, err := s.CreateEngReq(CreateEngReqInput{
		Worker: WorkerRef{ID: "w_demo", Label: "demo", Fleet: "local", Node: "n1", ProjectRel: "demo"},
		Title:  "Demo eng", ReqsRel: "docs/reqs", MilestoneTitle: "M2", AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min, err := s.NextMilestone(eng2.ID, 0)
	if err != nil || maj != 0 || min != 3 {
		t.Fatalf("next after 0.2 want 0.3, got %d.%d err=%v", maj, min, err)
	}
	v, err := s.AddVersion(AddVersionInput{EngReqID: eng.ID, Major: 0, Minor: 3, Patch: 1, Title: "Phase"})
	if err != nil {
		t.Fatal(err)
	}
	title := "Phase updated"
	problem := "p"
	v, err = s.UpdateVersion(UpdateVersionInput{
		EngReqID: eng.ID, Major: v.Major, Minor: v.Minor, Patch: v.Patch,
		Title: &title, Problem: &problem,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != title || v.Problem != problem {
		t.Fatalf("update mismatch: %+v", v)
	}
	v, err = s.ApproveVersion(eng.ID, 0, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "approved" {
		t.Fatalf("status %s", v.Status)
	}

	_, err = s.UpdateMajor(0, nil, strPtr(MajorSealed), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateEngReq(CreateEngReqInput{
		Worker: WorkerRef{ID: "w_other", Label: "o", ProjectRel: "o"}, Title: "x", ReqsRel: "d", AutoMajor: true,
	})
	if err == nil {
		t.Fatal("expected error when no developing major")
	}
	_, err = s.UpdateMajor(1, nil, strPtr(MajorDeveloping), nil)
	if err != nil {
		t.Fatal(err)
	}

}

func TestDeleteVersionRenumber(t *testing.T) {
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
		Worker: WorkerRef{ID: "w_del", Label: "del", ProjectRel: "d"}, Title: "Del", ReqsRel: "r", AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	maj, min := eng.Versions[0].Major, eng.Versions[0].Minor
	if _, err := s.AddVersion(AddVersionInput{EngReqID: eng.ID, Major: maj, Minor: min, Patch: 2, Title: "P2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddVersion(AddVersionInput{EngReqID: eng.ID, Major: maj, Minor: min, Patch: 3, Title: "P3"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVersion(eng.ID, maj, min, 2); err != nil {
		t.Fatal(err)
	}
	vers, err := s.listVersions(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	var under []ReqVersion
	for _, v := range vers {
		if v.Major == maj && v.Minor == min {
			under = append(under, v)
		}
	}
	if len(under) != 2 {
		t.Fatalf("want 2 patches, got %d %+v", len(under), under)
	}
	if under[0].Patch != 1 || under[1].Patch != 2 {
		t.Fatalf("want patches 1,2 got %d,%d", under[0].Patch, under[1].Patch)
	}
	if under[1].Title != "P3" {
		t.Fatalf("patch 2 should be former P3, got %q", under[1].Title)
	}
	if err := s.DeleteVersion(eng.ID, maj, min, 1); err != nil {
		t.Fatal(err)
	}
	vers, err = s.listVersions(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	under = under[:0]
	for _, v := range vers {
		if v.Major == maj && v.Minor == min {
			under = append(under, v)
		}
	}
	if len(under) != 1 || under[0].Patch != 1 || under[0].Title != "P3" {
		t.Fatalf("want single renumbered P3 as .1, got %+v", under)
	}
	if err := s.DeleteVersion(eng.ID, maj, min, 1); err == nil {
		t.Fatal("expected error deleting last version")
	}
}

func strPtr(s string) *string { return &s }
