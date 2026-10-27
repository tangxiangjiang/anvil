// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import "errors"

// Stable run / stream error codes (0.7+). Values are part of the public contract.
const (
	CodeMaxSteps        = "max_steps" // optional hard cap only (0 = unlimited)
	CodeLoopDetected    = "loop_detected"
	CodeCancelled       = "cancelled"
	CodeContextDeadline = "context_deadline"
	CodeMaxWallTime     = "max_wall_time"
	CodeProvider        = "provider"
	CodeEmptyResponse   = "empty_response"
	CodeFinishProtocol  = "finish_protocol"
	CodeToolPanic       = "tool_panic"
)

// Sentinel causes for context.WithCancelCause / WithTimeoutCause.
var (
	ErrCancelled   = errors.New("cancelled")
	ErrMaxWallTime = errors.New("max wall time exceeded")
)
