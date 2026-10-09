// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import "fmt"

// FormatRunStart returns a host log line with agent and run ids and no secrets.
// Prefer printing this immediately after Send succeeds (before tool noise).
func FormatRunStart(agentID, runID string) string {
	return fmt.Sprintf("agent=%s run=%s", agentID, runID)
}

// FormatRunEnd returns a host log line for a finished Wait result.
func FormatRunEnd(r *RunResult) string {
	if r == nil {
		return "status=unknown"
	}
	if r.Code != "" {
		return fmt.Sprintf("status=%s code=%s steps=%d", r.Status, r.Code, r.Steps)
	}
	return fmt.Sprintf("status=%s steps=%d", r.Status, r.Steps)
}
