// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPError represents a provider HTTP or transport failure.
type HTTPError struct {
	Status  int
	Message string
	// Kind classifies the failure once. Retryable is derived from it by NewHTTPError, so
	// the two cannot disagree. Build an HTTPError with NewHTTPError, not a literal.
	Kind      Kind
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

// Kind says what sort of failure a provider call was, independent of any message.
type Kind int

const (
	KindUnknown     Kind = iota // zero value: an error that was not built by NewHTTPError
	KindTransport               // no response was received
	KindRateLimited             // 429
	KindServer                  // 5xx
	KindRejected                // other 4xx: the request itself is wrong
	KindAuth                    // 401 / 403
)

func (k Kind) String() string {
	switch k {
	case KindTransport:
		return "transport"
	case KindRateLimited:
		return "rate_limited"
	case KindServer:
		return "server"
	case KindRejected:
		return "rejected"
	case KindAuth:
		return "auth"
	}
	return "unknown"
}

// KindRetryable is the one place that decides whether a failure may be tried again.
func KindRetryable(k Kind) bool {
	return k == KindTransport || k == KindRateLimited || k == KindServer
}

// KindCode maps a kind to the stable run code a container switches on. The provider's
// message text is kept verbatim in the error; the code is what is stable.
func KindCode(k Kind) string {
	switch k {
	case KindRateLimited:
		return "provider_busy"
	case KindRejected:
		return "request_rejected"
	case KindAuth:
		return "auth"
	}
	return "provider"
}

func kindOfStatus(status int) Kind {
	switch {
	case status == 0:
		return KindTransport
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return KindAuth
	case status == http.StatusTooManyRequests:
		return KindRateLimited
	case status >= 500:
		return KindServer
	case status >= 400:
		return KindRejected
	}
	// A 2xx whose body is an error envelope: the request was accepted but refused.
	return KindRejected
}

// NewHTTPError is the only constructor: it sets Kind from the status and derives
// Retryable from the Kind.
func NewHTTPError(status int, message string) *HTTPError {
	k := kindOfStatus(status)
	return &HTTPError{Status: status, Message: message, Kind: k, Retryable: KindRetryable(k)}
}

// kindError builds an error with an explicit kind, for failures with no usable status.
func newTransportError(message string, cause error) *HTTPError {
	e := NewHTTPError(0, message)
	e.Cause = cause
	return e
}

// KindOf returns err's Kind, or KindUnknown when err is not an *HTTPError.
func KindOf(err error) Kind {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Kind
	}
	return KindUnknown
}

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
	return NewHTTPError(status, fmt.Sprintf("provider error: %s", msg))
}
