// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestDeleteMajorTrashAndRestoreConflict(t *testing.T) {
	dir := t.TempDir()
	as, err := auth.Open(filepath.Join(dir, "t.db"), time.Hour)
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
	// Seed has majors 0 (developing) and 1 (planning). Add milestone under 1.
	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker: WorkerRef{ID: "w1", Label: "w1", ProjectRel: "p"}, Title: "T", ReqsRel: "docs/r",
		Major: 1, MilestoneTitle: "M1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if eng.Versions[0].Major != 1 {
		t.Fatalf("want major 1, got %+v", eng.Versions[0])
	}

	_, err = s.DeleteMajorToTrash(0)
	if err == nil {
		t.Fatal("should not delete non-planning / non-last major 0")
	}

	item, err := s.DeleteMajorToTrash(1)
	if err != nil {
		t.Fatal(err)
	}
	if item.Preview.MilestoneCount < 1 {
		t.Fatalf("expected milestones in trash preview: %+v", item.Preview)
	}
	list, err := s.ListTrash()
	if err != nil || len(list) != 1 {
		t.Fatalf("trash %v %v", list, err)
	}

	// Recreate major 1 empty, then restore should conflict.
	if _, err := s.AddMajor(1, "1.0", MajorPlanning, ""); err != nil {
		t.Fatal(err)
	}
	res, err := s.RestoreMajorFromTrash(item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Conflict {
		t.Fatal("expected conflict")
	}
	res, err = s.RestoreMajorFromTrash(item.ID, RestoreAppend)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict || res.Mode != RestoreAppend {
		t.Fatalf("append restore: %+v", res)
	}
	trash, _ := s.ListTrash()
	if len(trash) != 0 {
		t.Fatal("trash should be empty after restore")
	}
}
