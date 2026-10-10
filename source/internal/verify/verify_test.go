// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/verify"
)

func TestValidateTokenRejectsInjection(t *testing.T) {
	bad := []string{";rm", "a&&b", "$(x)", "`id`", "a|b", "x\ny"}
	for _, s := range bad {
		if err := verify.ValidateToken(s); err == nil {
			t.Fatalf("expected reject %q", s)
		}
	}
	if err := verify.ValidateToken("./pkg"); err != nil {
		t.Fatal(err)
	}
}

func TestRunFakeFailingBuild(t *testing.T) {
	dir := t.TempDir()
	script := writeExitScript(t, dir, 1, "build failed: oops.go:1:1: undefined")
	res, err := verify.Run(context.Background(), dir, verify.CommandSpec{
		Command: []string{script},
	}, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.ExitCode == 0 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.LogTail, "oops") {
		t.Fatalf("%q", res.LogTail)
	}
	if len(res.Errors) == 0 {
		t.Fatalf("expected parsed errors: %+v", res)
	}
}

func TestRunTimeout(t *testing.T) {
	dir := t.TempDir()
	script := writeSleepScript(t, dir)
	res, err := verify.Run(context.Background(), dir, verify.CommandSpec{
		Command: []string{script},
	}, nil, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Summary != "timeout" {
		t.Fatalf("%+v", res)
	}
}

func TestParseFailedTestsGo(t *testing.T) {
	log := "=== RUN   TestOK\n--- PASS: TestOK (0.00s)\n=== RUN   TestBoom\n--- FAIL: TestBoom (0.01s)\nFAIL\n"
	failed := verify.ParseFailedTests(log)
	if len(failed) != 1 || failed[0] != "TestBoom" {
		t.Fatalf("%v", failed)
	}
}

func TestUnconfigured(t *testing.T) {
	_, err := verify.Run(context.Background(), t.TempDir(), verify.CommandSpec{}, nil, time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
}

func writeExitScript(t *testing.T, dir string, code int, msg string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "fail.bat")
		body := "@echo off\r\necho " + msg + "\r\nexit /b " + strconv.Itoa(code) + "\r\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(dir, "fail.sh")
	body := "#!/bin/sh\necho '" + msg + "'\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSleepScript(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "sleep.bat")
		body := "@echo off\r\nping -n 5 127.0.0.1 >nul\r\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(dir, "sleep.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
