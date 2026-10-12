// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"net"
	"net/http"
	"strings"
)

// clientIP returns the TCP peer IP from RemoteAddr (no X-Forwarded-For).
func clientIP(r *http.Request) string {
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// allowSetupIP reports whether ip may run the install wizard.
// Allowed: loopback (localhost) and 192.168.0.0/16.
func allowSetupIP(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 192 && ip4[1] == 168
}

// setupAllowed reports whether this request may enter the install flow.
func setupAllowed(r *http.Request) bool {
	return allowSetupIP(clientIP(r))
}
