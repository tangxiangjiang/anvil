package anvil

import (
	"context"
	"sync"
)

// Run is one Send execution.
type Run struct {
	id     string
	events chan Event
	done   chan struct{}

	mu     sync.Mutex
	result *RunResult
	err    error
}

func newRun(id string) *Run {
	return &Run{
		id:     id,
		events: make(chan Event, 64),
		done:   make(chan struct{}),
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

// Cancel is available in milestone 0.2.
func (r *Run) Cancel(ctx context.Context) error {
	_ = ctx
	return newError("Cancel is not available until milestone 0.2", false, nil)
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
		// Drop if consumer is slow; Wait still authoritative.
		select {
		case r.events <- ev:
		default:
		}
	}
}
