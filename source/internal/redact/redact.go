// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package redact provides best-effort secret scrubbing for tool outputs and events.
// It is not a DLP product — patterns are heuristic and may false-positive or miss secrets.
package redact

import "regexp"

const placeholder = "[REDACTED]"

type rule struct {
	re   *regexp.Regexp
	repl string
}

var rules = []rule{
	{regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{10,}\b`), placeholder},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{10,}\b`), placeholder},
	{regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._\-+=/]{8,}`), "${1}" + placeholder},
	{regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`), placeholder},
	{regexp.MustCompile(`(?i)(api[_-]?key\s*[=:]\s*)(["']?)[A-Za-z0-9_\-/.+=]{8,}(["']?)`), "${1}${2}" + placeholder + "${3}"},
	// Multimodal payloads must not land in logs (0.19+).
	{regexp.MustCompile(`data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]{40,}`), "data:image/…;base64," + placeholder},
}

// String replaces obvious secret material with [REDACTED].
func String(s string) string {
	if s == "" {
		return s
	}
	out := s
	for _, r := range rules {
		out = r.re.ReplaceAllString(out, r.repl)
	}
	return out
}
