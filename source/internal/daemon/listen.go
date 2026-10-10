// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Listen opens a local listener.
// Supported:
//   - unix path (also works on Windows 10+ AF_UNIX): "/tmp/anvil.sock"
//   - tcp://127.0.0.1:PORT (loopback only recommended)
func Listen(addr string) (net.Listener, error) {
	if strings.HasPrefix(addr, "tcp://") {
		hostport := strings.TrimPrefix(addr, "tcp://")
		return net.Listen("tcp", hostport)
	}
	// Unix domain socket: remove stale file.
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", addr, err)
	}
	return ln, nil
}
