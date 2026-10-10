// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"net"
	"sync"
)

// connWriter serializes writes on a shared connection.
type connWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (c *connWriter) WriteEnvelope(env Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteEnvelope(c.conn, env)
}
