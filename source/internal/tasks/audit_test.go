// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tasks

import (
	"sync"
	"testing"
)

// audit #30: Manager.Cancel releases m.mu and then reads t.Info, which finish()
// writes under the lock. Only the race detector sees the conflict, so the test
// races Cancel against a task that exits on its own; go test -race reports it.
func TestAuditCancelRacesNaturalExit(t *testing.T) {
	// Reproduce under -race on AlmaLinux (cgo, a POSIX "true"). Skipped here:
	// this Windows machine has no C compiler, so -race cannot run.
	t.Skip("audit: open, see audit.md #30 (requires -race and cgo)")
	m := NewManager()
	for i := 0; i < 2000; i++ {
		info, err := m.StartShell(ShellSpec{Command: "true"})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Cancel(info.ID)
		}()
		wg.Wait()
	}
}
