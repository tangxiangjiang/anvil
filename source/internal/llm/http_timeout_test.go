// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm

import (
	"strings"
	"testing"
	"time"
)

func TestProviderResponseTimeoutAllowsLongCodingTurns(t *testing.T) {
	if providerResponseTimeout <= 2*time.Minute {
		t.Fatalf("provider timeout=%s still cuts off long streamed responses", providerResponseTimeout)
	}
	if got := NewAnthropic(Config{}).client.Timeout; got != providerResponseTimeout {
		t.Fatalf("Anthropic timeout=%s, want %s", got, providerResponseTimeout)
	}
	if got := NewOpenAI(Config{}).client.Timeout; got != providerResponseTimeout {
		t.Fatalf("OpenAI timeout=%s, want %s", got, providerResponseTimeout)
	}
}

func TestStreamIncompleteIsTransientAndKeepsItsType(t *testing.T) {
	err := newEmbeddedProviderError(200, "", "", "stream_incomplete")
	if err.Kind != KindServer || !err.Retryable {
		t.Fatalf("stream_incomplete kind=%s retryable=%v", err.Kind, err.Retryable)
	}
	if !strings.Contains(err.Error(), "stream_incomplete") {
		t.Fatalf("error lost provider type: %v", err)
	}
}
