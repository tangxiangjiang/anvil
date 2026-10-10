// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const maxFrameBytes = 16 << 20 // 16 MiB

// WriteEnvelope writes a length-prefixed JSON envelope.
func WriteEnvelope(w io.Writer, env Envelope) error {
	if env.Proto == 0 {
		env.Proto = ProtoVersion
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("daemon: frame too large: %d", len(payload))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// ReadEnvelope reads one length-prefixed JSON envelope.
func ReadEnvelope(r io.Reader) (Envelope, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Envelope{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxFrameBytes {
		return Envelope{}, fmt.Errorf("daemon: invalid frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}
