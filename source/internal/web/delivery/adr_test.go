// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"strings"
	"testing"
)

func TestComposeAndHydrateADR(t *testing.T) {
	d := Design{
		AdrContext:      "Need X",
		AdrDecision:     "Choose A",
		AdrArchitecture: "```mermaid\nflowchart LR\n  A-->B\n```",
	}
	got := composeADR(d)
	for _, part := range []string{"## Context", "## Decision", "## Architecture", "Need X", "Choose A"} {
		if !strings.Contains(got, part) {
			t.Fatalf("compose missing %q in %q", part, got)
		}
	}

	legacy := Design{ADR: "old blob only"}
	legacy.hydrateADRFallback()
	if legacy.AdrDecision != "old blob only" {
		t.Fatalf("fallback=%q", legacy.AdrDecision)
	}
	legacy2 := Design{ADR: "ignored", AdrContext: "already split"}
	legacy2.hydrateADRFallback()
	if legacy2.AdrDecision != "" {
		t.Fatalf("should not overwrite when sections present: %q", legacy2.AdrDecision)
	}
}
