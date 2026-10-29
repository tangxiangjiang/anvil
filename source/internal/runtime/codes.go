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
	// CodeProviderMidStream is a provider failure after the answer had begun to
	// reach the user. It is not retried, because a retry would show the text twice.
	// CodeProviderBusy is a 429 that outlasted the retries; CodeRequestRejected is a 4xx
	// that names the request itself (an unknown parameter, a context overflow); CodeAuth is
	// a 401 or 403. The provider's own text stays in the error.
	CodeProviderBusy      = "provider_busy"
	CodeRequestRejected   = "request_rejected"
	CodeAuth              = "auth"
	CodeProviderMidStream = "provider_mid_stream"
	CodeToolPanic         = "tool_panic"
	// CodeEmptyResponse is a provider that kept returning neither text nor a tool call
	// after bounded recovery. CodeFinishProtocol is a run that required finish_task and
	// never called it. Both belong to the editor completion protocol.
	CodeEmptyResponse  = "empty_response"
	CodeFinishProtocol = "finish_protocol"
)

// Sentinel causes for context.WithCancelCause / WithTimeoutCause.
var (
	ErrCancelled   = errors.New("cancelled")
	ErrMaxWallTime = errors.New("max wall time exceeded")
)
