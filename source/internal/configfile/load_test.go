// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package configfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/configfile"
)

type sample struct {
	Listen string            `json:"listen" yaml:"listen"`
	Meta   map[string]string `json:"meta" yaml:"meta"`
	Count  int               `json:"count" yaml:"count"`
}

func TestLoadJSONAndYAMLParity(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "c.json")
	yamlPath := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(jsonPath, []byte(`{"listen":"127.0.0.1:9","meta":{"a":"b"},"count":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, []byte("listen: 127.0.0.1:9\nmeta:\n  a: b\ncount: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var j, y sample
	if err := configfile.Load(jsonPath, &j); err != nil {
		t.Fatal(err)
	}
	if err := configfile.Load(yamlPath, &y); err != nil {
		t.Fatal(err)
	}
	if j.Listen != y.Listen || j.Count != y.Count || j.Meta["a"] != y.Meta["a"] {
		t.Fatalf("mismatch json=%+v yaml=%+v", j, y)
	}
}

func TestLoadUnsupportedExt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	_ = os.WriteFile(path, []byte("x=1"), 0o644)
	var s sample
	if err := configfile.Load(path, &s); err == nil {
		t.Fatal("expected error")
	}
}
