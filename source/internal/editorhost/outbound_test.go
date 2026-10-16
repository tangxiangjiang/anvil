// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// TestOutboundNeverBlocks covers the property the queue exists for: a client
// that has stopped reading must not stall the caller. Without it, the drain
// loop would block here, the run's 64-slot event channel would fill, and
// Run.emit would start dropping events.
func TestOutboundNeverBlocks(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	var written [][]byte
	w := writerFunc(func(p []byte) (int, error) {
		<-gate // hold every write until the test releases it
		mu.Lock()
		written = append(written, append([]byte(nil), p...))
		mu.Unlock()
		return len(p), nil
	})

	o := newOutbound(w)
	go o.run()

	const n = 5000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			o.push(map[string]any{"n": i})
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("push blocked while the client was stalled")
	}

	close(gate)
	o.flush()

	mu.Lock()
	got := len(written)
	mu.Unlock()
	if got != n {
		t.Fatalf("writer saw %d messages want %d", got, n)
	}
	if err := o.firstErr(); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
}

// TestOutboundFlushesAndStopsAccepting checks that flush drains the queue and
// that later pushes are dropped rather than written after close.
func TestOutboundFlushesAndStopsAccepting(t *testing.T) {
	var buf bytes.Buffer
	o := newOutbound(&buf)
	go o.run()

	o.push(map[string]any{"id": 1})
	o.push(map[string]any{"id": 2})
	o.flush()

	o.push(map[string]any{"id": 3}) // must be ignored

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line %d is not JSON: %v", i, err)
		}
		if m["id"] != float64(i+1) {
			t.Fatalf("line %d has id %v, order was not preserved", i, m["id"])
		}
	}
}

// TestOutboundRecordsWriteError keeps a broken pipe from being silent.
func TestOutboundRecordsWriteError(t *testing.T) {
	o := newOutbound(writerFunc(func([]byte) (int, error) {
		return 0, errShutdown
	}))
	go o.run()
	o.push(map[string]any{"x": 1})
	o.flush()
	if o.firstErr() == nil {
		t.Fatal("write error was swallowed")
	}
}
