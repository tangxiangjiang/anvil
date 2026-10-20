// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// configureShellCommand kills the command's process tree when the context ends.
//
// taskkill /T is the Windows process group: CommandContext's default Kill
// stops cmd.exe and leaves the program it started running. A `cmd /C` wrapper
// does not necessarily forward the termination signal to what it launched, so
// without /T a long command outlives the run that started it.
func configureShellCommand(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
	// CREATE_NEW_PROCESS_GROUP makes the child its own group, so taskkill /T
	// targets exactly this command's tree instead of whatever shares ours.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
		_ = kill.Run()
		return cmd.Process.Kill()
	}
}
