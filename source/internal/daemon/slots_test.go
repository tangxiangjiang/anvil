// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import "testing"

func TestSlotsResize(t *testing.T) {
	s := NewSlots(2)
	if s.Max() != 2 {
		t.Fatal(s.Max())
	}
	if err := s.Resize(4); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.MaxWorkers != 4 || snap.Idle != 4 {
		t.Fatalf("%+v", snap)
	}
	_, err := s.TryAcquire("j1", func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(1); err != nil {
		t.Fatal(err)
	}
	// Busy slot kept; max target 1; idle removed as much as possible.
	if s.Max() != 1 {
		t.Fatalf("max=%d", s.Max())
	}
	s.Release("j1")
}
