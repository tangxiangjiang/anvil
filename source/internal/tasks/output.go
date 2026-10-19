// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tasks

import "sync"

const (
	// MaxOutputBytes is how much of a task's output is kept. Older bytes are
	// dropped; a reader learns the oldest offset it can still get.
	MaxOutputBytes = 1 << 20
	// MaxChunkBytes caps one Output call, so a single read cannot carry the
	// whole ring into a model's context.
	MaxChunkBytes = 64 << 10
)

// Output is one read of a task's output ring.
type Output struct {
	Text string
	// NextOffset is where the next read should start.
	NextOffset int64
	// FirstOffset is the oldest byte still held. It is greater than the
	// requested offset only when the ring dropped bytes the reader wanted.
	FirstOffset int64
	// Truncated reports that the requested offset was older than FirstOffset,
	// so Text begins later than asked rather than pretending nothing is missing.
	Truncated bool
	// More reports that the ring holds bytes after Text. A running task may
	// also produce more later; a reader that needs the current end can use
	// NextOffset for either case.
	More bool
}

// ring is a byte buffer with a monotonic write offset. It keeps the newest
// MaxOutputBytes and reports where the retained window starts.
type ring struct {
	mu      sync.Mutex
	buf     []byte
	first   int64 // absolute offset of buf[0]
	written int64 // absolute offset just past the newest byte
}

func newRing() *ring { return &ring{} }

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.written += int64(len(p))
	if len(p) >= MaxOutputBytes {
		r.buf = append(r.buf[:0], p[len(p)-MaxOutputBytes:]...)
		r.first = r.written - int64(len(r.buf))
		return len(p), nil
	}
	r.buf = append(r.buf, p...)
	if over := len(r.buf) - MaxOutputBytes; over > 0 {
		r.buf = append(r.buf[:0], r.buf[over:]...)
		r.first += int64(over)
	}
	return len(p), nil
}

// appendText is Write for a string; the manager uses it for the final message
// of an agent task and for the timeout note.
func (r *ring) appendText(s string) {
	if s == "" {
		return
	}
	_, _ = r.Write([]byte(s))
}

func (r *ring) snapshot(offset, limit int) Output {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > MaxChunkBytes {
		limit = MaxChunkBytes
	}
	out := Output{FirstOffset: r.first, Truncated: int64(offset) < r.first}
	start := offset
	if start < int(r.first) {
		start = int(r.first)
	}
	if start > int(r.written) {
		start = int(r.written)
	}
	rel := start - int(r.first)
	end := len(r.buf)
	if rel+limit < end {
		end = rel + limit
	}
	if rel > end {
		rel = end
	}
	out.Text = string(r.buf[rel:end])
	out.NextOffset = int64(start + (end - rel))
	out.More = end < len(r.buf)
	return out
}
