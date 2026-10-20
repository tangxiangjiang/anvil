// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build !windows

package tools

import "os/exec"

// configureShellCommand kills the command's process group when the context ends.
//
// The shell is started with Setpgid so `sh -c "cmd1 | cmd2"` puts both children
// in one group; killing only the shell leaves a pipeline half-alive, which is
// how a "cancelled" command keeps writing to disk after the run is gone.
func configureShellCommand(cmd *exec.Cmd) {
	setProcessGroup(cmd)
}
