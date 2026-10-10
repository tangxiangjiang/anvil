// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package redact_test

import (
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/redact"
)

func TestRedactAPIKeyAndBearer(t *testing.T) {
	in := "token sk-abcdefghijklmnopqrstuvwxyz and Bearer abcdefghijklmnop"
	out := redact.String(in)
	if strings.Contains(out, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("sk- not redacted: %q", out)
	}
	if strings.Contains(out, "abcdefghijklmnop") {
		t.Fatalf("bearer token not redacted: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected placeholder: %q", out)
	}
}

func TestRedactPEM(t *testing.T) {
	in := "key:\n-----BEGIN PRIVATE KEY-----\nMIIKsecret\n-----END PRIVATE KEY-----\nok"
	out := redact.String(in)
	if strings.Contains(out, "MIIKsecret") {
		t.Fatalf("pem body leaked: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected placeholder: %q", out)
	}
}

func TestRedactPreservesNormalText(t *testing.T) {
	in := "hello world path=src/main.go"
	if redact.String(in) != in {
		t.Fatalf("unexpected change: %q", redact.String(in))
	}
}

func TestRedactImageDataURL(t *testing.T) {
	b64 := strings.Repeat("A", 48)
	in := "payload data:image/png;base64," + b64 + " end"
	out := redact.String(in)
	if strings.Contains(out, b64) {
		t.Fatalf("base64 leaked: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected placeholder: %q", out)
	}
}
