//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// The only test command a live run accepts is a verifier built here. The model
// controls run_tests' extra_args, so the program must never read os.Args, and it
// only reads two named workspace files. That keeps the live tier from running
// model-written code or a model-chosen flag such as -test.cpuprofile=<path>.
const verifierSource = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	for name, want := range map[string]string{"a.go": "return 3", "b.go": "return 4"} {
		data, err := os.ReadFile(name)
		if err != nil || !strings.Contains(string(data), want) {
			fmt.Printf("FAIL %s: want %q\n", name, want)
			os.Exit(1)
		}
	}
	fmt.Println("ok  evalfixture")
}
`

var trustedVerifiers sync.Map

// BuildVerifier compiles the fixed verifier into dir and registers it as the
// single command RunLive may run as a task's test command.
func BuildVerifier(ctx context.Context, dir string) (string, error) {
	source := filepath.Join(dir, "verifier_main.go")
	if err := os.WriteFile(source, []byte(verifierSource), 0o644); err != nil {
		return "", err
	}
	binary := filepath.Join(dir, "verifier")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build verifier: %w\n%s", err, output)
	}
	trustedVerifiers.Store(binary, true)
	return binary, nil
}

func trustedVerifierCommand(command []string) bool {
	if len(command) != 1 {
		return false
	}
	_, ok := trustedVerifiers.Load(command[0])
	return ok
}
