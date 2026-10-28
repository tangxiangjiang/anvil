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
	// seq is the recent call sequence, one entry per call. A cycle is judged on
	// the fingerprint (name and arguments), not the name alone: reading a file
	// and then searching for what it mentioned, with a new path and a new
	// pattern each time, is how a person reads a codebase, and a name-only
	// window called that a loop.
	seq []seqEntry

	warned bool
}

// seqEntry is one tool call as the cycle detector remembers it: the name for
// the message it prints, and the fingerprint it actually compares.
type seqEntry struct {
	name string
	fp   string
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
		var content string
		if i < len(results) {
			content = results[i].Content
		}
		if isRunningTaskPoll(tc, content) {
			// Re-reading a running background task is an explicit wait state,
			// not evidence that the model learned nothing. Break every generic
			// streak so a legitimate join cannot inherit an earlier warning.
			g.lastCallFP = ""
			g.callStreak = 0
			g.lastResultFP = ""
			g.resultStreak = 0
			g.seq = nil
			g.warned = false
			continue
		}
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

		g.seq = append(g.seq, seqEntry{name: tc.Name, fp: cfp})
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

func isRunningTaskPoll(call llm.ToolCall, content string) bool {
	if call.Name != "task_output" {
		return false
	}
	var in struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal(call.Args, &in) != nil || strings.TrimSpace(in.TaskID) == "" {
		return false
	}
	fields := strings.Fields(content)
	return len(fields) >= 2 && fields[0] == in.TaskID && fields[1] == "running"
}

// detectCycle reports a short sequence of identical calls repeated in a row.
//
// Identity is the call fingerprint, not the tool name: `read_file A` then
// `grep X` then `read_file B` then `grep Y` is progress even though the names
// repeat, and only a second pass over the *same* calls is a loop. Identical
// consecutive calls are left to the repeat detector, which owns that case.
// The message still prints names, because they are what a reader recognises.
func (g *loopGuard) detectCycle() string {
	need := g.cycleLen * g.cycleRepeats
	if len(g.seq) < need || g.cycleLen < 2 {
		return ""
	}
	window := g.seq[len(g.seq)-need:]
	pattern := window[:g.cycleLen]
	distinct := map[string]struct{}{}
	for _, e := range pattern {
		distinct[e.fp] = struct{}{}
	}
	if len(distinct) < 2 {
		return ""
	}
	for r := 1; r < g.cycleRepeats; r++ {
		off := r * g.cycleLen
		for i := 0; i < g.cycleLen; i++ {
			if window[off+i].fp != pattern[i].fp {
				return ""
			}
		}
	}
	names := make([]string, len(pattern))
	for i, e := range pattern {
		names[i] = e.name
	}
	return fmt.Sprintf("tool cycle %v repeated ×%d", names, g.cycleRepeats)
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
