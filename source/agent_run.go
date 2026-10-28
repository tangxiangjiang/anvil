// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"sync"

	"github.com/anvil-dev/anvil/internal/runtime"
)

// Run is one Send execution.
type Run struct {
	id     string
	events chan Event
	done   chan struct{}

	cancelCause context.CancelCauseFunc

	mu     sync.Mutex
	result *RunResult
	err    error
}

func newRun(id string, cancelCause context.CancelCauseFunc) *Run {
	return &Run{
		id:          id,
		events:      make(chan Event, 64),
		done:        make(chan struct{}),
		cancelCause: cancelCause,
	}
}

// ID returns the run identifier.
func (r *Run) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

// Stream returns events until the channel is closed.
func (r *Run) Stream(ctx context.Context) <-chan Event {
	_ = ctx
	if r == nil {
		ch := make(chan Event)
		close(ch)
		return ch
	}
	return r.events
}

// Wait blocks until the run finishes and returns the result.
func (r *Run) Wait(ctx context.Context) (*RunResult, error) {
	if r == nil {
		return nil, newError("nil run", false, nil)
	}
	select {
	case <-ctx.Done():
		return nil, newError("wait cancelled", true, ctx.Err())
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.result, r.err
	}
}

// Cancel requests the run to stop. In-flight LLM/shell calls observing the
// run context should abort; Wait then returns StatusCancelled with CodeCancelled.
func (r *Run) Cancel(ctx context.Context) error {
	_ = ctx
	if r == nil {
		return newError("nil run", false, nil)
	}
	if r.cancelCause != nil {
		r.cancelCause(runtime.ErrCancelled)
	}
	return nil
}

func (r *Run) finish(result *RunResult, err error) {
	r.mu.Lock()
	r.result = result
	r.err = err
	r.mu.Unlock()
	close(r.events)
	close(r.done)
}

func (r *Run) emit(ev Event) {
	select {
	case r.events <- ev:
	default:
		select {
		case r.events <- ev:
		default:
		}
	}
}
