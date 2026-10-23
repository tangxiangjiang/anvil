// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckTaskRules(t *testing.T) {
	base := Baseline{"t": {
		Gated: []string{"completed", "steps", "refused", "tool_errors="},
		Score: Score{Completed: true, Steps: 4, Refused: 1, ToolErrors: 1},
	}}
	gated := base["t"].Gated
	good := base["t"].Score
	with := func(change func(*Score)) Score { s := good; change(&s); return s }

	if notes, err := CheckTask(base, "t", gated, good); err != nil || len(notes) != 0 {
		t.Fatalf("equal score: notes=%v err=%v", notes, err)
	}
	if notes, err := CheckTask(base, "t", gated, with(func(s *Score) { s.Steps = 3 })); err != nil || len(notes) != 1 {
		t.Fatalf("improvement must pass with a note: notes=%v err=%v", notes, err)
	}
	for name, change := range map[string]func(*Score){
		"steps regress":     func(s *Score) { s.Steps = 5 },
		"not completed":     func(s *Score) { s.Completed = false },
		"one refusal more":  func(s *Score) { s.Refused = 2 },
		"one refusal less":  func(s *Score) { s.Refused = 0 },
		"exact tool errors": func(s *Score) { s.ToolErrors = 0 },
	} {
		if _, err := CheckTask(base, "t", gated, with(change)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// A field no task gates is recorded but never fails.
	if _, err := CheckTask(base, "t", gated, with(func(s *Score) { s.RequestBytes = 1 << 30 })); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckTask(base, "new", gated, good); err == nil || !strings.Contains(err.Error(), "record it first") {
		t.Fatalf("missing entry: %v", err)
	}
	if _, err := CheckTask(base, "t", []string{"completed"}, good); err == nil {
		t.Fatal("changed gating was accepted")
	}
	if err := Stale(base, []string{"other"}); err == nil || !strings.Contains(err.Error(), "t") {
		t.Fatalf("stale entry: %v", err)
	}
	if err := Stale(base, []string{"t"}); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	want := Baseline{"a": {Gated: []string{"steps"}, Score: Score{Completed: true, Steps: 2}}}
	if err := WriteBaseline(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBaseline(path)
	if err != nil || got["a"].Score.Steps != 2 || got["a"].Gated[0] != "steps" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if _, err := LoadBaseline(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing baseline accepted")
	}
}
