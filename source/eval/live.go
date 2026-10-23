// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/anvil-dev/anvil/internal/llm"
)

// Recorder records plain Complete calls only. Streaming deltas and transient
// reasoning are outside this boundary and are not supported or reconstructed.
// Only typed requests/responses are serialized, never configs or headers.
type Recorder struct {
	mu         sync.Mutex
	cond       *sync.Cond
	file       *os.File
	upstream   llm.Completer
	provenance string
	identities map[string]string
	active     int
	closing    bool
	closed     bool
	writeErr   error
	err        error
	bytes      atomic.Int64
}

var _ llm.Completer = (*Recorder)(nil)

// NewRecorder exclusively creates a private .live.jsonl file. It never
// truncates, replaces, or removes an existing recording, even on failure.
func NewRecorder(path string, upstream llm.Completer, provenance string) (*Recorder, error) {
	if !strings.HasSuffix(path, ".live.jsonl") {
		return nil, errors.New("recorder path must end in .live.jsonl")
	}
	if upstream == nil {
		return nil, errors.New("recorder: nil upstream")
	}
	if !validProvenance(provenance) {
		return nil, errors.New("recorder provenance must be synthetic or provider")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create recording: %w", err)
	}
	r := &Recorder{file: file, upstream: upstream, provenance: provenance, identities: make(map[string]string)}
	r.cond = sync.NewCond(&r.mu)
	return r, nil
}

// Bytes counts original llm.Request JSON bytes for every attempted Complete,
// including cancellation, provider failures, and calls after Close. Requests
// which cannot be marshaled as JSON contribute zero bytes and report an error.
func (r *Recorder) Bytes() int64 { return r.bytes.Load() }

func (r *Recorder) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	// Marshal at entry, before the provider can mutate slices or RawMessages.
	// Independent copies protect both the caller and the recorded snapshot.
	original, err := json.Marshal(req)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(fmt.Errorf("snapshot request: %w", err))
	}
	r.bytes.Add(int64(len(original)))
	var snapshot, upstreamRequest llm.Request
	if err := json.Unmarshal(original, &snapshot); err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(fmt.Errorf("snapshot request: %w", err))
	}
	if err := json.Unmarshal(original, &upstreamRequest); err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(fmt.Errorf("copy upstream request: %w", err))
	}
	r.mu.Lock()
	if r.closing || r.closed {
		err := r.fail(errors.New("recorder is closed"))
		r.mu.Unlock()
		return nil, err
	}
	if r.writeErr != nil {
		err := r.writeErr
		r.mu.Unlock()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		err = r.fail(err)
		r.mu.Unlock()
		return nil, err
	}
	r.active++
	r.mu.Unlock()
	// No recorder lock is held while the provider runs.
	defer func() {
		r.mu.Lock()
		r.active--
		r.cond.Broadcast()
		r.mu.Unlock()
	}()
	response, err := r.upstream.Complete(ctx, upstreamRequest)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(err)
	}
	if response == nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(errors.New("recorder: upstream returned nil response without error"))
	}
	copied, err := cloneResponse(*response)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(err)
	}
	canonical, err := CanonicalRequest(snapshot)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(err)
	}
	identity, err := canonicalJSON(*copied)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(fmt.Errorf("response identity: %w", err))
	}
	// The line is complete before taking the serialization lock. Size limits
	// and duplicate-response checks match the replay reader.
	record := Record{Version: RecordVersion, Provenance: r.provenance, Digest: digestCanonical(canonical), Request: snapshot, Response: *copied}
	line, err := json.Marshal(record)
	if err == nil && len(line) > maxRecordLine {
		err = fmt.Errorf("record exceeds %d-byte line limit", maxRecordLine)
	}
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(fmt.Errorf("encode recording: %w", err))
	}
	line = append(line, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr != nil {
		return nil, r.writeErr
	}
	if previous, ok := r.identities[record.Digest]; ok && previous != string(identity) {
		return nil, r.fail(fmt.Errorf("conflicting duplicate response for digest %s", record.Digest))
	}
	n, err := r.file.Write(line)
	if err == nil && n != len(line) {
		err = errors.New("short recording write")
	}
	if err != nil {
		r.writeErr = fmt.Errorf("write recording: %w", err)
		return nil, r.fail(r.writeErr)
	}
	r.identities[record.Digest] = string(identity)
	return copied, nil
}

func (r *Recorder) fail(err error) error {
	r.err = errors.Join(r.err, err)
	return err
}

// Err includes sticky provider, cancellation, serialization, and file errors.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close refuses new calls, waits for calls already admitted, then syncs and
// closes the file. Concurrent/repeated Close calls see the same sticky result.
// No error path removes a recording, including a partial write.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		for !r.closed {
			r.cond.Wait()
		}
		return r.err
	}
	r.closing = true
	for r.active != 0 {
		r.cond.Wait()
	}
	if err := r.file.Sync(); err != nil {
		r.fail(fmt.Errorf("sync recording: %w", err))
	}
	if err := r.file.Close(); err != nil {
		r.fail(fmt.Errorf("close recording: %w", err))
	}
	r.closed = true
	r.cond.Broadcast()
	return r.err
}
