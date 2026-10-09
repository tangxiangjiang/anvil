// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"fmt"
	"net/http"
)

// HTTPError represents a provider HTTP or transport failure.
type HTTPError struct {
	Status    int
	Message   string
	Retryable bool
	Cause     error
}

func (e *HTTPError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s (status=%d): %v", e.Message, e.Status, e.Cause)
	}
	if e.Status > 0 {
		return fmt.Sprintf("%s (status=%d)", e.Message, e.Status)
	}
	return e.Message
}

func (e *HTTPError) Unwrap() error { return e.Cause }

func mapHTTPStatus(status int, body string) *HTTPError {
	msg := body
	if len(msg) > 512 {
		msg = msg[:512] + "…"
	}
	retryable := status == http.StatusTooManyRequests || status >= 500 || status == 0
	return &HTTPError{
		Status:    status,
		Message:   fmt.Sprintf("provider error: %s", msg),
		Retryable: retryable,
	}
}
