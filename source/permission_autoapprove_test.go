// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"encoding/json"
	"testing"
)

// TestAutoApproveCall pins the classifier's three approvals and, more
// importantly, what it refuses. Every false row is a call the approval card
// will still show, which is the direction the mode can afford to be wrong in.
func TestAutoApproveCall(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want bool
	}{
		// Shell: only a whole line the analyzer proves non-mutating.
		{"read-only command", "shell", `{"command":"git status"}`, true},
		{"read-only compound", "shell", `{"command":"cd sub && git status"}`, true},
		{"provably mutating", "shell", `{"command":"rm -rf build"}`, false},
		{"one mutating segment", "shell", `{"command":"git status && rm -rf build"}`, false},
		{"redirection", "shell", `{"command":"git status > out.txt"}`, false},
		{"expansion", "shell", `{"command":"echo $(date)"}`, false},
		{"unclassifiable command", "shell", `{"command":"git frobnicate"}`, false},
		{"empty command", "shell", `{"command":""}`, false},
		{"missing command", "shell", `{}`, false},

		// Edits: confined to the workspace by the sandbox.
		{"write", "write_file", `{"path":"a.txt","content":"x"}`, true},
		{"edit", "edit_file", `{"path":"a.txt","old_string":"a","new_string":"b"}`, true},
		{"patch", "apply_patch", `{"patch":"*** Begin Patch\n*** Add File: n.txt\n+hi\n*** End Patch\n"}`, true},
		{"unparsable patch", "apply_patch", `{"patch":"not a patch"}`, false},
		{"write without a path", "write_file", `{"content":"x"}`, false},

		// Verify: the argv is the session owner's configuration.
		{"tests", "run_tests", `{}`, true},
		{"build", "build", `{"target":"all"}`, true},

		// Everything else asks.
		{"web search", "web_search", `{"query":"x"}`, false},
		{"fetch", "fetch_url", `{"url":"https://example.com"}`, false},
		{"mcp tool", "mcp_fetch", `{"url":"https://example.com"}`, false},
		{"unknown tool", "something_new", `{"path":"a.txt"}`, false},
		{"no arguments", "shell", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AutoApproveCall(tc.tool, json.RawMessage(tc.args)); got != tc.want {
				t.Fatalf("AutoApproveCall(%q, %s) = %v, want %v", tc.tool, tc.args, got, tc.want)
			}
		})
	}
}
