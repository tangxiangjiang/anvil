// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// RetryPolicy bounds how a failed provider call is retried. The zero value is on:
// three attempts. MaxAttempts 1 restores a single attempt.
type RetryPolicy struct {
	MaxAttempts int           // 0 means 3; 1 means no retry
	BaseDelay   time.Duration // 0 means 500ms
	MaxDelay    time.Duration // 0 means 8s
}

// Resolved fills in the defaults.
func (p RetryPolicy) Resolved() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 500 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 8 * time.Second
	}
	if p.MaxDelay < p.BaseDelay {
		p.MaxDelay = p.BaseDelay
	}
	return p
}

// RetryInfo describes one retry that is about to happen.
type RetryInfo struct {
	Attempt int           // the attempt that just failed, 1-based
	Max     int           // the policy's MaxAttempts
	Delay   time.Duration // how long the wrapper will wait before the next attempt
	Message string        // the provider's message for the failure
}

type retryObserverKey struct{}

// WithRetryObserver returns a context whose retries are reported to fn. The runtime
// threads its emitter this way so the Completer interface keeps its shape.
func WithRetryObserver(ctx context.Context, fn func(RetryInfo)) context.Context {
	return context.WithValue(ctx, retryObserverKey{}, fn)
}

func observeRetry(ctx context.Context, info RetryInfo) {
	if fn, ok := ctx.Value(retryObserverKey{}).(func(RetryInfo)); ok && fn != nil {
		fn(info)
	}
}

// BackoffDelay is exponential from BaseDelay with full jitter, capped at MaxDelay.
// attempt is the attempt that just failed, 1-based.
func BackoffDelay(p RetryPolicy, attempt int) time.Duration {
	p = p.Resolved()
	ceiling := p.BaseDelay
	for i := 1; i < attempt && ceiling < p.MaxDelay; i++ {
		ceiling *= 2
	}
	if ceiling > p.MaxDelay {
		ceiling = p.MaxDelay
	}
	return time.Duration(rand.Int64N(int64(ceiling) + 1))
}

// midStreamError marks a failure that happened after text reached the user. A retry
// would show the answer twice, so it is surfaced, and the run reports a distinct code.
type midStreamError struct{ cause error }

func (e *midStreamError) Error() string { return e.cause.Error() }
func (e *midStreamError) Unwrap() error { return e.cause }

// IsMidStream reports whether err is a stream failure after the first delta.
func IsMidStream(err error) bool {
	var m *midStreamError
	return errors.As(err, &m)
}

// retryable is the decision table of chapter 48 section 4.3.
func retryable(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Retryable
}

// WithRetry returns a Completer that retries c on retryable failures. It implements
// StreamCompleter exactly when c does, so streaming is unchanged for the loop.
func WithRetry(c Completer, p RetryPolicy) Completer {
	r := &retrying{inner: c, policy: p.Resolved()}
	if sc, ok := c.(StreamCompleter); ok {
		return &retryingStream{retrying: r, stream: sc}
	}
	return r
}

type retrying struct {
	inner  Completer
	policy RetryPolicy
}

type retryingStream struct {
	*retrying
	stream StreamCompleter
}

// attempt runs call up to MaxAttempts times. failedAfterOutput reports whether the
// failed attempt had already shown output (so it must not be repeated).
func (r *retrying) run(ctx context.Context, call func() (*Response, error), shownOutput func() bool) (*Response, error) {
	var last error
	for attempt := 1; attempt <= r.policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := call()
		if err == nil {
			// Only the successful attempt's Response is returned, so a failed
			// attempt's usage block is never counted.
			return resp, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if shownOutput != nil && shownOutput() {
			return nil, &midStreamError{cause: err}
		}
		if !retryable(err) || attempt == r.policy.MaxAttempts {
			break
		}
		delay := BackoffDelay(r.policy, attempt)
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.HasRetryAfter {
			delay = min(httpErr.RetryAfter, r.policy.MaxDelay)
		}
		observeRetry(ctx, RetryInfo{Attempt: attempt, Max: r.policy.MaxAttempts, Delay: delay, Message: err.Error()})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if retryable(last) && r.policy.MaxAttempts > 1 {
		return nil, fmt.Errorf("%w (after %d attempts)", last, r.policy.MaxAttempts)
	}
	return nil, last
}

func (r *retrying) Complete(ctx context.Context, req Request) (*Response, error) {
	return r.run(ctx, func() (*Response, error) { return r.inner.Complete(ctx, req) }, nil)
}

func (r *retryingStream) CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	shown := false
	wrapped := func(d Delta) {
		if d.Text != "" || d.Reasoning != "" {
			shown = true
		}
		if onDelta != nil {
			onDelta(d)
		}
	}
	return r.run(ctx, func() (*Response, error) {
		shown = false
		return r.stream.CompleteStream(ctx, req, wrapped)
	}, func() bool { return shown })
}
