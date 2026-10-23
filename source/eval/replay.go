// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/anvil-dev/anvil/internal/llm"
)

const (
	RecordVersion = 1
	maxRecordLine = 16 * 1024 * 1024
)

// Record stores typed completion traffic, never provider configuration or
// headers. Provenance must explicitly be "synthetic" or "provider".
type Record struct {
	Version    int          `json:"version"`
	Provenance string       `json:"provenance"`
	Digest     string       `json:"digest"`
	Request    llm.Request  `json:"request"`
	Response   llm.Response `json:"response"`
}

type replayEntry struct {
	record    Record
	canonical []byte
}

type replayBucket struct {
	response llm.Response
	identity string
	count    int
	used     int
}

// Replay matches requests by digest with a finite number of recorded
// occurrences. It is safe for concurrent Complete, Err, and Verify calls.
type Replay struct {
	mu      sync.Mutex
	entries []replayEntry
	buckets map[string]*replayBucket
	err     error
}

var _ llm.Completer = (*Replay)(nil)

func validProvenance(provenance string) bool {
	return provenance == "synthetic" || provenance == "provider"
}

// NewReplay reads and validates JSONL completely before making it available.
// Blank lines, absent required fields, trailing JSON, and oversized lines are
// errors with line-number diagnostics. At least one record is required.
func NewReplay(reader io.Reader) (*Replay, error) {
	if reader == nil {
		return nil, errors.New("replay: nil reader")
	}
	replay := &Replay{buckets: make(map[string]*replayBucket)}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxRecordLine+2)
	line := 0
	for scanner.Scan() {
		line++
		b := scanner.Bytes()
		if len(b) > maxRecordLine {
			return nil, fmt.Errorf("replay line %d: exceeds %d-byte limit", line, maxRecordLine)
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			return nil, fmt.Errorf("replay line %d: empty record", line)
		}
		// Pointers distinguish missing/null fields from legitimately zero-valued
		// typed requests and responses.
		var wire struct {
			Version    *int          `json:"version"`
			Provenance *string       `json:"provenance"`
			Digest     *string       `json:"digest"`
			Request    *llm.Request  `json:"request"`
			Response   *llm.Response `json:"response"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(b)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&wire); err != nil {
			return nil, fmt.Errorf("replay line %d: decode record: %w", line, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("replay line %d: trailing JSON or malformed data", line)
		}
		if wire.Version == nil || wire.Provenance == nil || wire.Digest == nil || wire.Request == nil || wire.Response == nil {
			return nil, fmt.Errorf("replay line %d: missing or null required record field", line)
		}
		record := Record{*wire.Version, *wire.Provenance, *wire.Digest, *wire.Request, *wire.Response}
		if record.Version != RecordVersion {
			return nil, fmt.Errorf("replay line %d: unsupported version %d", line, record.Version)
		}
		if !validProvenance(record.Provenance) {
			return nil, fmt.Errorf("replay line %d: provenance must be synthetic or provider", line)
		}
		canonical, err := CanonicalRequest(record.Request)
		if err != nil {
			return nil, fmt.Errorf("replay line %d: %w", line, err)
		}
		if record.Digest != digestCanonical(canonical) {
			return nil, fmt.Errorf("replay line %d: stored digest does not match request", line)
		}
		responseJSON, err := canonicalJSON(record.Response)
		if err != nil {
			return nil, fmt.Errorf("replay line %d: %w", line, err)
		}
		if bucket := replay.buckets[record.Digest]; bucket != nil {
			if bucket.identity != string(responseJSON) {
				return nil, fmt.Errorf("replay line %d: conflicting duplicate response for digest %s", line, record.Digest)
			}
			bucket.count++
		} else {
			replay.buckets[record.Digest] = &replayBucket{response: record.Response, identity: string(responseJSON), count: 1}
		}
		replay.entries = append(replay.entries, replayEntry{record: record, canonical: canonical})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("replay line %d: read record (maximum %d bytes): %w", line+1, maxRecordLine, err)
	}
	if line == 0 {
		return nil, errors.New("replay line 1: empty recording")
	}
	return replay, nil
}

// LoadReplay loads an immutable recording from disk.
func LoadReplay(path string) (*Replay, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open replay: %w", err)
	}
	replay, readErr := NewReplay(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, err
	}
	return replay, nil
}

func (r *Replay) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	canonical, err := CanonicalRequest(req)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.fail(err)
	}
	digest := digestCanonical(canonical)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, r.fail(err)
	}
	bucket := r.buckets[digest]
	if bucket != nil && bucket.used < bucket.count {
		response, err := cloneResponse(bucket.response)
		if err != nil {
			return nil, r.fail(err)
		}
		bucket.used++
		return response, nil
	}
	kind := "miss"
	if bucket != nil {
		kind = "exhausted"
	}
	nearest := r.nearest(len(req.Messages), digest)
	err = fmt.Errorf("replay %s for digest %s\n%s", kind, digest, requestDiff(nearest.canonical, canonical))
	return nil, r.fail(err)
}

func (r *Replay) fail(err error) error {
	if r.err == nil {
		r.err = err
	}
	return err
}

// Err retains the first completion error, even after a later successful call.
func (r *Replay) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Verify reports both sticky completion errors and unconsumed occurrences.
func (r *Replay) Verify() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	remaining := 0
	var counts []string
	seen := make(map[string]bool)
	for _, entry := range r.entries {
		digest := entry.record.Digest
		if seen[digest] {
			continue
		}
		seen[digest] = true
		bucket := r.buckets[digest]
		if n := bucket.count - bucket.used; n > 0 {
			remaining += n
			counts = append(counts, fmt.Sprintf("%s: %d", digest, n))
		}
	}
	if remaining != 0 {
		return errors.Join(r.err, fmt.Errorf("replay has %d unconsumed occurrences (%s)", remaining, strings.Join(counts, "; ")))
	}
	return r.err
}

func (r *Replay) nearest(messageCount int, digest string) replayEntry {
	best := r.entries[0]
	bestDelta, bestPrefix := absCount(len(best.record.Request.Messages)-messageCount), sharedPrefix(best.record.Digest, digest)
	for _, entry := range r.entries[1:] {
		delta, prefix := absCount(len(entry.record.Request.Messages)-messageCount), sharedPrefix(entry.record.Digest, digest)
		if delta < bestDelta || (delta == bestDelta && prefix > bestPrefix) {
			best, bestDelta, bestPrefix = entry, delta, prefix
		}
	}
	return best
}

func absCount(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func sharedPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// A single genuine unified-diff hunk replaces the differing middle, retaining
// up to three context lines on either side. This avoids quadratic LCS memory
// for large multimodal requests while keeping the precise changed JSON visible.
func requestDiff(recorded, actual []byte) string {
	pretty := func(b []byte) []string {
		var output strings.Builder
		// CanonicalRequest has already validated the JSON.
		var tree any
		decoder := json.NewDecoder(strings.NewReader(string(b)))
		decoder.UseNumber()
		_ = decoder.Decode(&tree)
		formatted, _ := json.MarshalIndent(tree, "", "  ")
		output.Write(formatted)
		return strings.Split(output.String(), "\n")
	}
	old, new := pretty(recorded), pretty(actual)
	prefix := 0
	for prefix < len(old) && prefix < len(new) && old[prefix] == new[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(new)-prefix && old[len(old)-1-suffix] == new[len(new)-1-suffix] {
		suffix++
	}
	start := max(0, prefix-3)
	oldEnd, newEnd := len(old)-max(0, suffix-3), len(new)-max(0, suffix-3)
	var diff strings.Builder
	diff.WriteString("--- recorded\n+++ actual\n")
	fmt.Fprintf(&diff, "@@ -%d,%d +%d,%d @@\n", start+1, oldEnd-start, start+1, newEnd-start)
	for _, line := range old[start:prefix] {
		fmt.Fprintf(&diff, " %s\n", line)
	}
	for _, line := range old[prefix : len(old)-suffix] {
		fmt.Fprintf(&diff, "-%s\n", line)
	}
	for _, line := range new[prefix : len(new)-suffix] {
		fmt.Fprintf(&diff, "+%s\n", line)
	}
	for _, line := range old[len(old)-suffix : oldEnd] {
		fmt.Fprintf(&diff, " %s\n", line)
	}
	return diff.String()
}
