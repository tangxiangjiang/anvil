// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"os"
	"testing"
)

// TestMain keeps the developer's own user rule file out of the test run.
//
// Sessions created with LoadPermissions read $ANVIL_HOME/permissions.json, and a
// test must not start depending on the policy of whoever happens to run it. The
// override is the same one the package documents, so there is no second way of
// choosing the file.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "anvil-home-*")
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
