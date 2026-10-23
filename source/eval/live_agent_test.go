//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	anvil "github.com/anvil-dev/anvil"
)

var liveEval = flag.Bool("live", false, "report live evaluations (also requires ANVIL_EVAL_LIVE=1)")

func TestLiveReport(t *testing.T) {
	if !*liveEval || os.Getenv("ANVIL_EVAL_LIVE") != "1" {
		t.Skip("live report requires -live and ANVIL_EVAL_LIVE=1")
	}
	if os.Getenv("ANVIL_MODEL") == "" || os.Getenv("ANVIL_API_KEY") == "" {
		t.Skip("live report requires configured ANVIL_MODEL and ANVIL_API_KEY")
	}
	for _, fixture := range agentFixtures() {
		t.Run(fixture.task.Name, func(t *testing.T) {
			if len(fixture.options.Test.Command) != 0 {
				t.Skip("live test-command execution needs a separately authorized OS sandbox")
			}
			// Separate paths, exclusively created; committed data is never replaced.
			if err := os.MkdirAll("recordings", 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("recordings", fixture.task.Name+".live.jsonl")
			out, err := RunLive(t.Context(), t.TempDir(), fixture.task, path, fixture.options)
			// Remote model performance is a report: no remote-dependent red gate.
			t.Logf("%s live score=%+v error=%v", fixture.task.Name, out.Score, err)
		})
	}
}

func TestLiveRejectsUnsafeSettings(t *testing.T) {
	t.Setenv("ANVIL_EVAL_LIVE", "1")
	t.Setenv("ANVIL_PROVIDER", "openai")
	t.Setenv("ANVIL_MODEL", "eval-unused-model")
	t.Setenv("ANVIL_API_KEY", "eval-unused-key")
	for _, test := range []struct {
		name     string
		settings anvil.Options
		want     string
	}{
		{"test_command", anvil.Options{Test: anvil.TestOptions{Command: []string{"unused"}}}, "OS sandbox"},
		{"build_command", anvil.Options{Build: anvil.BuildOptions{Command: []string{"unused"}}}, "OS sandbox"},
		{"model_test_command", anvil.Options{Test: anvil.TestOptions{Command: []string{"go", "test", "./..."}}}, "only the eval-built verifier"},
		{"shell_only", anvil.Options{BuiltinTools: []string{"shell"}}, "no safe selected tools"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsafe.live.jsonl")
			_, err := RunLive(t.Context(), t.TempDir(), Task{Name: test.name}, path, test.settings)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("rejected configuration created a recording: %v", err)
			}
		})
	}
}

func TestLiveRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("ANVIL_EVAL_LIVE", "")
	_, err := RunLive(t.Context(), t.TempDir(), Task{Name: "live"}, filepath.Join(t.TempDir(), "live.live.jsonl"), anvil.Options{})
	if err == nil || !strings.Contains(err.Error(), "ANVIL_EVAL_LIVE=1") {
		t.Fatalf("error=%v", err)
	}
	t.Setenv("ANVIL_EVAL_LIVE", "1")
	t.Setenv("ANVIL_MODEL", "")
	t.Setenv("ANVIL_API_KEY", "")
	_, err = RunLive(t.Context(), t.TempDir(), Task{Name: "live"}, filepath.Join(t.TempDir(), "live.live.jsonl"), anvil.Options{})
	if err == nil || !strings.Contains(err.Error(), "ANVIL_MODEL and ANVIL_API_KEY") {
		t.Fatalf("error=%v", err)
	}
}

func TestLiveAcceptsOnlyBuiltVerifier(t *testing.T) {
	verifier, err := BuildVerifier(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !trustedVerifierCommand([]string{verifier}) {
		t.Fatal("built verifier was not trusted")
	}
	for _, command := range [][]string{{verifier, "-test.cpuprofile=x"}, {"go", "test"}, {verifier + "-other"}, nil} {
		if trustedVerifierCommand(command) {
			t.Fatalf("untrusted command accepted: %v", command)
		}
	}
	// The verifier ignores arguments the model passes through extra_args.
	dir := t.TempDir()
	for name, body := range map[string]string{"a.go": "return 3", "b.go": "return 4"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(verifier, "-test.cpuprofile="+filepath.Join(dir, "leak"), "--anything")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verifier: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dir, "leak")); !os.IsNotExist(err) {
		t.Fatal("a model-supplied argument made the verifier write a file")
	}
}
