// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package plugin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anvil-dev/anvil/internal/plugin"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestParseManifestInvalid(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", `{}`},
		{"bad api", `{"id":"x","version":"1","anvil_api":99}`},
		{"unknown perm", `{"id":"x","version":"1","anvil_api":1,"permissions":["root.all"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := plugin.ParseManifest([]byte(tc.raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseManifestOK(t *testing.T) {
	m, err := plugin.ParseManifest([]byte(`{
		"id":"sample","version":"0.1.0","anvil_api":1,
		"capabilities":["tools.plugin_ping"],
		"permissions":["fs.read","net.none"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "sample" || m.AnvilAPI != 1 {
		t.Fatalf("%+v", m)
	}
}

func TestHostRejectsDuplicateTool(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	host := plugin.NewHost(dir, reg, nil)
	tool := &tools.FuncTool{Name: "dup", Description: "d", Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "ok", nil
	}}
	if err := host.RegisterTool(tool); err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterTool(tool); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestPolicyDenyAndResolve(t *testing.T) {
	p := &plugin.Policy{Available: []string{"sample", "other"}, Deny: []string{"other"}}
	ids, err := p.ResolveIDs([]string{"sample"})
	if err != nil || len(ids) != 1 || ids[0] != "sample" {
		t.Fatalf("%v %v", ids, err)
	}
	if _, err := p.ResolveIDs([]string{"other"}); err == nil {
		t.Fatal("denied should fail")
	}
	if _, err := p.ResolveIDs([]string{"missing"}); err == nil {
		t.Fatal("missing should fail")
	}
	defs, err := p.ResolveIDs(nil)
	if err != nil || len(defs) != 1 || defs[0] != "sample" {
		t.Fatalf("default available: %v %v", defs, err)
	}
}
