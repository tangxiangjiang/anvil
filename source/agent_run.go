// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/anvil-dev/anvil/internal/runtime"
)

// Run is one Send execution.
type Run struct {
	id     string
	events chan Event
	done   chan struct{}

	cancelCause context.CancelCauseFunc
	journal     func(seq uint64, event json.RawMessage) error

	mu      sync.Mutex
	result  *RunResult
	err     error
	lastSeq uint64
}

func newRun(
	id string,
	cancelCause context.CancelCauseFunc,
	journal func(seq uint64, event json.RawMessage) error,
) *Run {
	return &Run{
		id:          id,
		events:      make(chan Event, 64),
		done:        make(chan struct{}),
		cancelCause: cancelCause,
		journal:     journal,
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
	if r.journal != nil && durableRunEvent(ev.Type) {
		r.mu.Lock()
		next := r.lastSeq + 1
		ev.Seq = next
		raw, err := json.Marshal(ev)
		if err == nil {
			err = r.journal(next, raw)
			if err == nil {
				r.lastSeq = next
			}
		}
		r.mu.Unlock()
		if err != nil {
			if r.cancelCause != nil {
				r.cancelCause(err)
			}
			return
		}
	}
	select {
	case r.events <- ev:
	default:
		select {
		case r.events <- ev:
		default:
		}
	}
}

func durableRunEvent(eventType EventType) bool {
	switch eventType {
	case EventReasoning, EventProgress:
		return false
	default:
		return true
	}
}
