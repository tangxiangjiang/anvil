// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil"
)

func TestCmdListNoNetwork(t *testing.T) {
	dir := t.TempDir()
	a, err := anvil.Create(context.Background(), anvil.Options{
		APIKey: "k",
		Model:  anvil.Model{ID: "m"},
		Local:  anvil.LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()
	_ = a.Close()

	if err := cmdList([]string{dir}); err != nil {
		t.Fatal(err)
	}
	_ = id
}

func TestCmdGetJSON(t *testing.T) {
	dir := t.TempDir()
	a, err := anvil.Create(context.Background(), anvil.Options{
		APIKey: "k",
		Model:  anvil.Model{ID: "cli-model"},
		Local:  anvil.LocalOptions{CWD: dir, Store: "sqlite"},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()
	_ = a.Close()

	if err := cmdGet([]string{"--store", "sqlite", dir, id}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildOptionsFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".anvil")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config"), []byte(`{"model":"from-config","store":"sqlite","max_steps":64}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, err := buildOptions(dir, cliFlags{maxSteps: -1})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Model.ID != "from-config" || opts.Local.Store != "sqlite" || opts.MaxSteps != 64 {
		t.Fatalf("%+v", opts)
	}
}
