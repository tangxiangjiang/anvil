// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import "testing"

func TestSlotsResizeShrinkOnly(t *testing.T) {
	s := NewSlots(0)
	if _, err := s.AddBoundWorker("a", "A", "", "/tmp/a", "", "a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBoundWorker("b", "B", "", "/tmp/b", "", "b", true); err != nil {
		t.Fatal(err)
	}
	if s.Max() != 2 {
		t.Fatal(s.Max())
	}
	if err := s.Resize(4); err == nil {
		t.Fatal("expected grow error")
	}
	_, err := s.TryAcquire("j1", func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(1); err != nil {
		t.Fatal(err)
	}
	// Busy kept; one idle removed.
	if s.Max() != 1 {
		t.Fatalf("max=%d", s.Max())
	}
	s.Release("j1")
}

func TestSlotsStartsEmpty(t *testing.T) {
	s := NewSlots(0)
	snap := s.Snapshot()
	if snap.MaxWorkers != 0 || snap.Idle != 0 || len(snap.Workers) != 0 {
		t.Fatalf("%+v", snap)
	}
}
