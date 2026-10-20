// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup starts the command in its own process group and makes
// cancellation kill the whole group, so a pipeline or a shell that spawned a
// background job cannot survive the run.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid targets the group. Fall back to the process itself when
		// the group is already gone, which is the ordinary race with exit.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
