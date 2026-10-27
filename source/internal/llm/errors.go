// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPError represents a provider HTTP or transport failure.
type HTTPError struct {
	Status    int
	Message   string
	Retryable bool
	Cause     error
	// RetryAfter is the provider's Retry-After, if it sent one the adapter could read.
	// Zero means none. It is a hint for the retry wrapper, not part of the message.
	RetryAfter time.Duration
	// HasRetryAfter distinguishes "Retry-After: 0" from no header at all.
	HasRetryAfter bool
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

// parseRetryAfter reads Retry-After as delta-seconds or an HTTP date. An absent or
// unparseable value reports ok=false, and the caller falls back to its own backoff.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(value, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs * float64(time.Second)), true
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := when.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// withRetryAfter copies a response's Retry-After onto err.
func withRetryAfter(err *HTTPError, header http.Header) *HTTPError {
	if d, ok := parseRetryAfter(header.Get("Retry-After"), time.Now()); ok {
		err.RetryAfter, err.HasRetryAfter = d, true
	}
	return err
}

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
