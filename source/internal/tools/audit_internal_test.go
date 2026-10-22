// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"encoding/json"
	"testing"
)

// audit #7: pathsOf returns nothing for rename_symbol, and it is not in the mutating
// set, so a rename that rewrites many files captures no pre-image and no path rule
// sees its targets.
func TestAuditRenameSymbolNamesItsPaths(t *testing.T) {
	t.Skip("audit: open, see audit.md #7")
	args := json.RawMessage(`{"path":"a.txt","line":1,"character":1,"new_name":"x"}`)
	if got := pathsOf("rename_symbol", args); len(got) == 0 {
		t.Fatalf("rename_symbol names no paths, so its pre-images are not captured and no path rule can see its targets")
	}
	if !mutatingTools["rename_symbol"] {
		t.Fatalf("rename_symbol is not in mutatingTools")
	}
}
