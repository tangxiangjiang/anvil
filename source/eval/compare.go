// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// BaselineEntry is the last accepted Score of one task and the fields that
// guard it. A gated name ending in "=" demands exact equality; otherwise
// completed must hold, refused and loop_stops are always exact, and every other
// field may only stay or go down.
type BaselineEntry struct {
	Gated []string `json:"gated"`
	Score Score    `json:"score"`
}

// Baseline maps task name to its entry. JSON encoding sorts the keys, so an
// -update shows up as a minimal diff.
type Baseline map[string]BaselineEntry

func LoadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline: %w (record it with -update)", err)
	}
	var base Baseline
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	return base, nil
}

func WriteBaseline(path string, base Baseline) error {
	data, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func scoreField(score Score, name string) (int64, bool) {
	switch name {
	case "completed":
		if score.Completed {
			return 1, true
		}
		return 0, true
	case "steps":
		return int64(score.Steps), true
	case "tool_errors":
		return int64(score.ToolErrors), true
	case "refused":
		return int64(score.Refused), true
	case "loop_stops":
		return int64(score.LoopStops), true
	case "retries":
		return int64(score.Retries), true
	case "request_bytes":
		return score.RequestBytes, true
	case "agents":
		return int64(score.Agents), true
	}
	return 0, false
}

// CheckTask compares one task with its baseline entry. A task absent from the
// baseline, a gating list that differs from the recorded one, or a gated field
// that moved the wrong way is an error; an improvement is returned as a note.
func CheckTask(base Baseline, name string, gated []string, got Score) (notes []string, err error) {
	entry, ok := base[name]
	if !ok {
		return nil, fmt.Errorf("task %s is not in the baseline: record it first with -update", name)
	}
	if strings.Join(entry.Gated, ",") != strings.Join(gated, ",") {
		return nil, fmt.Errorf("task %s: gated fields changed (baseline %v, task %v); run -update", name, entry.Gated, gated)
	}
	var failures []string
	for _, spec := range gated {
		field := strings.TrimSuffix(spec, "=")
		want, okWant := scoreField(entry.Score, field)
		have, okHave := scoreField(got, field)
		if !okWant || !okHave {
			failures = append(failures, fmt.Sprintf("unknown gated field %q", spec))
			continue
		}
		exact := spec != field || field == "refused" || field == "loop_stops"
		switch {
		case have == want:
		case exact:
			failures = append(failures, fmt.Sprintf("%s = %d, baseline %d (exact)", field, have, want))
		case field == "completed" && have < want, field != "completed" && have > want:
			failures = append(failures, fmt.Sprintf("%s regressed: %d, baseline %d", field, have, want))
		default:
			notes = append(notes, fmt.Sprintf("%s improved: %d, baseline %d", field, have, want))
		}
	}
	if len(failures) != 0 {
		return notes, fmt.Errorf("task %s: %s", name, strings.Join(failures, "; "))
	}
	return notes, nil
}

// Stale reports baseline entries that no task produces.
func Stale(base Baseline, names []string) error {
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	var stale []string
	for name := range base {
		if !known[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return fmt.Errorf("stale baseline entries with no task: %s", strings.Join(stale, ", "))
}
