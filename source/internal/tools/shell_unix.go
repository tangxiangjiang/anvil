// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build unix

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// configureShellCommand puts the command in its own process group and kills
// that group when the context ends.
//
// Killing only the shell leaves the processes it started holding its output
// pipe, and CombinedOutput then waits forever after the timeout has already
// fired. The group dies together, and WaitDelay closes the pipes if a
// descendant still outlives the signal.
func configureShellCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid is the process group started by Setpgid.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
}
