// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"encoding/json"
	"io"
	"sync"
)

// outbound serializes every host-to-client message on one writer goroutine fed
// by an unbounded queue.
//
// The queue is the point: a run's event channel holds 64 slots and Run.emit
// drops silently when it is full (see anvil/run.go). If the protocol reader
// wrote straight to stdout, a slow or blocked client would stall the drain
// loop, back-pressure that channel, and cost the client events. Buffering here
// means the drain loop always keeps pace with the agent, and the client's
// slowness only costs memory.
type outbound struct {
	enc *json.Encoder

	mu     sync.Mutex
	cond   *sync.Cond
	items  []any
	closed bool
	err    error

	done chan struct{}
}

func newOutbound(w io.Writer) *outbound {
	o := &outbound{enc: json.NewEncoder(w), done: make(chan struct{})}
	o.cond = sync.NewCond(&o.mu)
	return o
}

// push enqueues one message. It never blocks.
func (o *outbound) push(v any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.items = append(o.items, v)
	o.cond.Signal()
}

// run writes queued messages until flush closes the queue. Call it in its own
// goroutine.
func (o *outbound) run() {
	defer close(o.done)
	for {
		o.mu.Lock()
		for len(o.items) == 0 && !o.closed {
			o.cond.Wait()
		}
		if len(o.items) == 0 {
			o.mu.Unlock()
			return
		}
		item := o.items[0]
		o.items[0] = nil
		o.items = o.items[1:]
		o.mu.Unlock()

		if err := o.enc.Encode(item); err != nil {
			o.mu.Lock()
			if o.err == nil {
				o.err = err
			}
			o.mu.Unlock()
		}
	}
}

// flush stops accepting messages and waits until everything queued has been
// written, so a host that is exiting never truncates its output.
func (o *outbound) flush() {
	o.mu.Lock()
	o.closed = true
	o.cond.Broadcast()
	o.mu.Unlock()
	<-o.done
}

// firstErr returns the first write error, if any.
func (o *outbound) firstErr() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}
