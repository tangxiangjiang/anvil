// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"os"
	"testing"
)

// TestMain keeps the developer's own user rule file out of the test run. A
// session reads $ANVIL_HOME/permissions.json, and the host's tests must not
// depend on the machine-wide policy of whoever runs them.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "anvil-host-home-*")
	if err != nil {
		panic("test home: " + err.Error())
	}
	old, had := os.LookupEnv("ANVIL_HOME")
	if err := os.Setenv("ANVIL_HOME", dir); err != nil {
		panic("test home: " + err.Error())
	}
	code := m.Run()
	if had {
		_ = os.Setenv("ANVIL_HOME", old)
	} else {
		_ = os.Unsetenv("ANVIL_HOME")
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
