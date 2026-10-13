// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/tools"
)

const (
	defaultRepeatThreshold = 3
	defaultStallThreshold  = 3
	defaultCycleLen        = 2
	defaultCycleRepeats    = 3
)

// LoopDetectedPrefix is the stable prefix of Result.Err when a tool loop is stopped.
const LoopDetectedPrefix = "loop detected"

type loopAction int

const (
	loopOK loopAction = iota
	loopWarn
	loopStop
)

// loopGuard detects identical tool calls, result stalls, and short ping-pong cycles.
type loopGuard struct {
	repeatThreshold int
	stallThreshold  int
	cycleLen        int
	cycleRepeats    int

	lastCallFP   string
	callStreak   int
	lastResultFP string
	resultStreak int
	nameSeq      []string

	warned bool
}

func newLoopGuard() *loopGuard {
	return &loopGuard{
		repeatThreshold: defaultRepeatThreshold,
		stallThreshold:  defaultStallThreshold,
		cycleLen:        defaultCycleLen,
		cycleRepeats:    defaultCycleRepeats,
	}
}

func (g *loopGuard) Observe(calls []llm.ToolCall, results []tools.Result) (loopAction, string) {
	if g == nil || len(calls) == 0 {
		return loopOK, ""
	}
	reason := ""
	for i, tc := range calls {
		cfp := callFingerprint(tc)
		if cfp == g.lastCallFP {
			g.callStreak++
		} else {
			g.lastCallFP = cfp
			g.callStreak = 1
		}
		if g.callStreak >= g.repeatThreshold {
			reason = fmt.Sprintf("repeated tool call %q ×%d", tc.Name, g.callStreak)
			break
		}

		var content string
		if i < len(results) {
			content = results[i].Content
		}
		rfp := resultFingerprint(tc.Name, content)
		if rfp == g.lastResultFP {
			g.resultStreak++
		} else {
			g.lastResultFP = rfp
			g.resultStreak = 1
		}
		if g.resultStreak >= g.stallThreshold {
			reason = fmt.Sprintf("stalled tool %q (identical result ×%d)", tc.Name, g.resultStreak)
			break
		}

		g.nameSeq = append(g.nameSeq, tc.Name)
		if msg := g.detectCycle(); msg != "" {
			reason = msg
			break
		}
	}
	if reason == "" {
		return loopOK, ""
	}
	if !g.warned {
		g.warned = true
		return loopWarn, reason
	}
	return loopStop, reason
}

func (g *loopGuard) detectCycle() string {
	need := g.cycleLen * g.cycleRepeats
	if len(g.nameSeq) < need || g.cycleLen < 2 {
		return ""
	}
	window := g.nameSeq[len(g.nameSeq)-need:]
	pattern := window[:g.cycleLen]
	// Ping-pong only: ignore degenerate [A A]… (same tool reused for different args;
	// identical-call / stall fingerprints already cover true no-progress).
	distinct := map[string]struct{}{}
	for _, n := range pattern {
		distinct[n] = struct{}{}
	}
	if len(distinct) < 2 {
		return ""
	}
	for r := 1; r < g.cycleRepeats; r++ {
		off := r * g.cycleLen
		for i := 0; i < g.cycleLen; i++ {
			if window[off+i] != pattern[i] {
				return ""
			}
		}
	}
	return fmt.Sprintf("tool cycle %v repeated ×%d", pattern, g.cycleRepeats)
}

func callFingerprint(tc llm.ToolCall) string {
	return tc.Name + "\x00" + compactJSON(tc.Args)
}

func resultFingerprint(name, content string) string {
	sum := sha256.Sum256([]byte(content))
	return name + "\x00" + hex.EncodeToString(sum[:8])
}

func compactJSON(raw []byte) string {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

func loopWarnMessage(reason string) string {
	return "Loop guard: " + reason +
		". Do not repeat the same tool call with the same arguments. " +
		"Change strategy, use different inputs, or finish with a final answer explaining the blocker."
}

// IsLoopDetected reports whether errText is a loop-guard stop.
func IsLoopDetected(errText string) bool {
	return strings.HasPrefix(errText, LoopDetectedPrefix)
}
