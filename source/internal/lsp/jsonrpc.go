// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Conn is a stdio JSON-RPC LSP connection (Content-Length framing).
type Conn struct {
	w   io.Writer
	r   *bufio.Reader
	mu  sync.Mutex // write
	id  atomic.Int64
	pending map[int64]chan rpcResult
	pendMu  sync.Mutex
	notify  func(method string, params json.RawMessage)
	closed  atomic.Bool
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *flexibleID     `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type flexibleID struct {
	n int64
	s string
	isString bool
}

func (f *flexibleID) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		f.isString = true
		return json.Unmarshal(b, &f.s)
	}
	f.isString = false
	return json.Unmarshal(b, &f.n)
}

func (f flexibleID) MarshalJSON() ([]byte, error) {
	if f.isString {
		return json.Marshal(f.s)
	}
	return json.Marshal(f.n)
}

// NewConn wraps reader/writer. Call Serve to process inbound messages.
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{
		w:       w,
		r:       bufio.NewReader(r),
		pending: map[int64]chan rpcResult{},
	}
}

// OnNotification registers a handler for server→client notifications.
func (c *Conn) OnNotification(fn func(method string, params json.RawMessage)) {
	c.notify = fn
}

// Serve reads messages until EOF or context cancel.
func (c *Conn) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg, err := c.readMessage()
		if err != nil {
			c.failAll(err)
			if err == io.EOF || c.closed.Load() {
				return nil
			}
			return err
		}
		if msg.ID != nil && msg.Method == "" {
			// response
			id := msg.ID.n
			c.pendMu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.pendMu.Unlock()
			if ch != nil {
				if msg.Error != nil {
					ch <- rpcResult{err: fmt.Errorf("lsp %d: %s", msg.Error.Code, msg.Error.Message)}
				} else {
					ch <- rpcResult{result: msg.Result}
				}
			}
			continue
		}
		if msg.Method != "" && msg.ID == nil {
			if c.notify != nil {
				c.notify(msg.Method, msg.Params)
			}
		}
		// Ignore server requests for MVP (unhandled).
	}
}

// Close marks the connection closed.
func (c *Conn) Close() {
	c.closed.Store(true)
	c.failAll(io.EOF)
}

func (c *Conn) failAll(err error) {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcResult{err: err}
		delete(c.pending, id)
	}
}

// Request sends a JSON-RPC request and waits for the result.
func (c *Conn) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("lsp connection closed")
	}
	id := c.id.Add(1)
	ch := make(chan rpcResult, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	fid := flexibleID{n: id}
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeJSON(body); err != nil {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, err
	}
	_ = fid
	select {
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, ctx.Err()
	case res := <-ch:
		return res.result, res.err
	}
}

// Notify sends a notification (no response).
func (c *Conn) Notify(method string, params any) error {
	return c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *Conn) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(raw))
	if _, err := io.WriteString(c.w, header); err != nil {
		return err
	}
	_, err = c.w.Write(raw)
	return err
}

func (c *Conn) readMessage() (*rpcMessage, error) {
	var contentLen int
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n := strings.TrimSpace(line[len("Content-Length:"):])
			contentLen, err = strconv.Atoi(n)
			if err != nil || contentLen < 0 {
				return nil, fmt.Errorf("bad Content-Length")
			}
		}
	}
	if contentLen == 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	buf := make([]byte, contentLen)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Decode is a tiny helper for tests.
func Decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	return json.Unmarshal(raw, v)
}
