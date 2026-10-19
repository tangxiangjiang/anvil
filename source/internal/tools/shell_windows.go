// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"time"
)

// configureShellCommand kills the command's process tree when the context ends.
//
// taskkill /T is the Windows process group: CommandContext's default Kill
// stops cmd.exe and leaves the program it started running.
func configureShellCommand(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
		_ = kill.Run()
		return cmd.Process.Kill()
	}
}
