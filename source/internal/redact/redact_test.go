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
