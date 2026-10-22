// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"errors"
	"io/fs"
	"testing"
)

func TestStaticFSExcludesFrontendTestInputs(t *testing.T) {
	static, err := StaticFS()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(static, "index.html"); err != nil {
		t.Fatalf("browser entry point is not embedded: %v", err)
	}
	for _, name := range []string{"test/pure.test.js", "package.json", "package-lock.json", "node_modules"} {
		if _, err := fs.Stat(static, name); err == nil {
			t.Errorf("%q is embedded but is not a browser asset", name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %q: %v", name, err)
		}
	}
}
