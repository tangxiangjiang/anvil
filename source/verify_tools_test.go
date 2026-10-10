// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeVerifyScript(t *testing.T, dir, name string, code int, msg string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, name+".bat")
		body := "@echo off\r\necho " + msg + "\r\nexit /b " + strconv.Itoa(code) + "\r\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(dir, name+".sh")
	body := "#!/bin/sh\necho '" + msg + "'\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyToolsAbsentWhenUnconfigured(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if toolPresent(a, "run_tests") || toolPresent(a, "build") {
		t.Fatal("tools should be absent")
	}
}

func TestBuildAndRunTestsConfigured(t *testing.T) {
	dir := t.TempDir()
	fail := writeVerifyScript(t, dir, "fail", 1, "pkg.go:2:3: undefined: X")
	ok := writeVerifyScript(t, dir, "ok", 0, "ok")
	failTest := writeVerifyScript(t, dir, "failtest", 1, "--- FAIL: TestBoom (0.01s)\nFAIL")

	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		Build: BuildOptions{Command: []string{fail}, Timeout: 5 * time.Second},
		Test:  TestOptions{Command: []string{failTest}, Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	bres := a.registry.Execute(context.Background(), "build", mustJSON(map[string]any{}))
	if !bres.IsError || !strings.Contains(bres.Content, "exit=") {
		t.Fatalf("%+v", bres)
	}

	tres := a.registry.Execute(context.Background(), "run_tests", mustJSON(map[string]any{}))
	if !tres.IsError || !strings.Contains(tres.Content, "TestBoom") {
		t.Fatalf("%+v", tres)
	}

	a2, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		Test:  TestOptions{Command: []string{ok}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	okres := a2.registry.Execute(context.Background(), "run_tests", mustJSON(map[string]any{}))
	if okres.IsError {
		t.Fatalf("%+v", okres)
	}
}

func TestRunTestsRejectsInjection(t *testing.T) {
	dir := t.TempDir()
	ok := writeVerifyScript(t, dir, "ok", 0, "ok")
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		Test:  TestOptions{Command: []string{ok}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res := a.registry.Execute(context.Background(), "run_tests", mustJSON(map[string]any{
		"selector": "Foo;rm -rf",
	}))
	if !res.IsError || !strings.Contains(res.Content, "rejected") {
		t.Fatalf("%+v", res)
	}
}

func TestVerifyDisallowedGroup(t *testing.T) {
	dir := t.TempDir()
	ok := writeVerifyScript(t, dir, "ok", 0, "ok")
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:           LocalOptions{CWD: dir},
		Test:            TestOptions{Command: []string{ok}},
		Build:           BuildOptions{Command: []string{ok}},
		DisallowedTools: []string{"verify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if toolPresent(a, "run_tests") || toolPresent(a, "build") {
		t.Fatal("verify group should strip")
	}
}

func TestProjectJSONConfig(t *testing.T) {
	dir := t.TempDir()
	ok := writeVerifyScript(t, dir, "ok", 0, "ok")
	_ = os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755)
	body, err := json.Marshal(map[string]any{
		"test":  map[string]any{"command": []string{ok}},
		"build": map[string]any{"command": []string{ok}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "project.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !toolPresent(a, "run_tests") || !toolPresent(a, "build") {
		t.Fatal("expected tools from project.json")
	}
}
