// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"testing"
)

func TestAllowSetupIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"192.168.0.1", true},
		{"192.168.255.20", true},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"8.8.8.8", false},
		{"", false},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		if got := allowSetupIP(tc.ip); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.ip, got, tc.want)
		}
	}
}

func TestClientIP(t *testing.T) {
	r := &http.Request{RemoteAddr: "192.168.1.5:54321"}
	if got := clientIP(r); got != "192.168.1.5" {
		t.Fatalf("got %q", got)
	}
	r = &http.Request{RemoteAddr: "127.0.0.1:9"}
	if got := clientIP(r); got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
}
