// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func readMCPFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mcpObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return out
}

func mcpEntry(t *testing.T, top map[string]json.RawMessage, name string) map[string]json.RawMessage {
	t.Helper()
	servers := mcpObject(t, top["mcpServers"])
	raw, ok := servers[name]
	if !ok {
		t.Fatalf("mcpServers has no %q", name)
	}
	return mcpObject(t, raw)
}

// The file belongs to the user and to other tools. A write must leave every
// field this milestone knows nothing about exactly where it was, including the
// fields inside servers nobody asked to change.
func TestAddMCPServerPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, path, `{
  "$schema": "https://example.test/schema.json",
  "mcpServers": {
    "keep": {
      "command": "npx",
      "args": ["keep"],
      "env": {"KEEP_TOKEN": "secret-value"},
      "vendor": {"flag": true, "nested": [1, 2]}
    }
  }
}`)

	listing, err := AddMCPServer(dir, SettingSourceProject, MCPServerDraft{
		Name:    "added",
		Command: "node",
		Args:    []string{"server.js"},
		Env:     map[string]string{"ADD_TOKEN": "hunter2"},
	}, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	added, ok := serverNamed(listing, "added")
	if !ok || !added.Enabled || added.Transport != "stdio" {
		t.Fatalf("added=%+v want enabled stdio", added)
	}
	if got := strings.Join(added.EnvNames, ","); got != "ADD_TOKEN" {
		t.Fatalf("added env names=%q want ADD_TOKEN", got)
	}

	top := mcpObject(t, readMCPFile(t, path))
	if string(top["$schema"]) != `"https://example.test/schema.json"` {
		t.Fatalf("top-level unknown field=%s want it kept", top["$schema"])
	}
	keep := mcpEntry(t, top, "keep")
	vendor, ok := keep["vendor"]
	if !ok {
		t.Fatal("keep.vendor was dropped")
	}
	var got, want any
	if err := json.Unmarshal(vendor, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"flag": true, "nested": [1, 2]}`), &want); err != nil {
		t.Fatal(err)
	}
	if !deepEqualJSON(got, want) {
		t.Fatalf("keep.vendor=%s want %v", vendor, want)
	}
	env := mcpObject(t, keep["env"])
	if string(env["KEEP_TOKEN"]) != `"secret-value"` {
		t.Fatalf("keep.env=%s want the value untouched", keep["env"])
	}

	// A value already in the file is kept when the update does not name it.
	if _, err := UpdateMCPServer(dir, SettingSourceProject, MCPServerDraft{
		Name:    "keep",
		Command: "npx",
		Args:    []string{"keep", "--verbose"},
	}, []SettingSource{SettingSourceProject}); err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	top = mcpObject(t, readMCPFile(t, path))
	keep = mcpEntry(t, top, "keep")
	if _, ok := keep["vendor"]; !ok {
		t.Fatal("update dropped keep.vendor")
	}
	if string(mcpObject(t, keep["env"])["KEEP_TOKEN"]) != `"secret-value"` {
		t.Fatalf("update dropped the env value: %s", keep["env"])
	}
	var args []string
	if err := json.Unmarshal(keep["args"], &args); err != nil || strings.Join(args, ",") != "keep,--verbose" {
		t.Fatalf("args=%s want the new list", keep["args"])
	}
}

func deepEqualJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// A file that does not parse is read-only. Overwriting it would delete whatever
// hand edit broke it, so every writer refuses and every byte stays.
func TestMCPWritersRefuseAMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	body := []byte(`{"mcpServers": {"a": {"command": "x"},`)
	writeMCPFile(t, path, string(body))

	ops := map[string]func() (MCPListing, error){
		"add": func() (MCPListing, error) {
			return AddMCPServer(dir, SettingSourceProject, MCPServerDraft{Name: "b", Command: "y"}, []SettingSource{SettingSourceProject})
		},
		"update": func() (MCPListing, error) {
			return UpdateMCPServer(dir, SettingSourceProject, MCPServerDraft{Name: "a", Command: "y"}, []SettingSource{SettingSourceProject})
		},
		"remove": func() (MCPListing, error) {
			return RemoveMCPServer(dir, SettingSourceProject, "a", []SettingSource{SettingSourceProject})
		},
		"toggle": func() (MCPListing, error) {
			return SetMCPServerEnabled(dir, SettingSourceProject, "a", false, []SettingSource{SettingSourceProject})
		},
	}
	for name, op := range ops {
		_, err := op()
		if err == nil {
			t.Fatalf("%s accepted a malformed file", name)
		}
		var described *MCPConfigError
		if !errors.As(err, &described) || described.Path != path {
			t.Fatalf("%s error=%v want an MCPConfigError naming %s", name, err, path)
		}
		if !bytes.Equal(readMCPFile(t, path), body) {
			t.Fatalf("%s changed the bytes of a malformed file", name)
		}
	}
}

// Disabling writes the field; enabling deletes it, because absent is how every
// other MCP client spells "on".
func TestSetMCPServerEnabledWritesAndRemovesTheField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, path, `{"mcpServers": {"files": {"command": "npx", "args": ["files"]}}}`)

	listing, err := SetMCPServerEnabled(dir, SettingSourceProject, "files", false, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("SetMCPServerEnabled false: %v", err)
	}
	server, ok := serverNamed(listing, "files")
	if !ok || server.Enabled {
		t.Fatalf("listing=%+v want files disabled", listing.Servers)
	}
	entry := mcpEntry(t, mcpObject(t, readMCPFile(t, path)), "files")
	if string(entry["enabled"]) != "false" {
		t.Fatalf("enabled=%s want false", entry["enabled"])
	}

	listing, err = SetMCPServerEnabled(dir, SettingSourceProject, "files", true, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("SetMCPServerEnabled true: %v", err)
	}
	server, ok = serverNamed(listing, "files")
	if !ok || !server.Enabled {
		t.Fatalf("listing=%+v want files enabled", listing.Servers)
	}
	entry = mcpEntry(t, mcpObject(t, readMCPFile(t, path)), "files")
	if _, ok := entry["enabled"]; ok {
		t.Fatalf("enabled=%s want the key gone", entry["enabled"])
	}
}

// Adding to a workspace with no config directory creates it; the file appears
// where the loader will look for it.
func TestAddMCPServerCreatesTheProjectFile(t *testing.T) {
	dir := t.TempDir()
	listing, err := AddMCPServer(dir, SettingSourceProject, MCPServerDraft{
		Name: "docs", URL: "https://example.test/mcp", Transport: "streamable-http",
	}, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	server, ok := serverNamed(listing, "docs")
	if !ok || server.Transport != "http" {
		t.Fatalf("docs=%+v want transport http", server)
	}
	path := filepath.Join(dir, ".cursor", "mcp.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
}

// The listing feeds a manager form, so it carries the fields the form edits —
// and the names of the secrets, never the secrets.
func TestListMCPServersReportsLaunchFieldsAndNamesOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, path, `{"mcpServers": {
		"docs": {
			"url": "https://example.test/mcp",
			"headers": {"Authorization": "Bearer top-secret", "X-Tenant": "acme"},
			"enabled": false
		}
	}}`)

	listing, err := ListMCPServers(dir, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	server, ok := serverNamed(listing, "docs")
	if !ok {
		t.Fatal("docs missing")
	}
	if server.Enabled {
		t.Fatal("docs listed as enabled")
	}
	if server.URL != "https://example.test/mcp" || server.Command != "" {
		t.Fatalf("docs=%+v want the url and no command", server)
	}
	if got := strings.Join(server.HeaderNames, ","); got != "Authorization,X-Tenant" {
		t.Fatalf("header names=%q want sorted keys", got)
	}
	for _, name := range server.HeaderNames {
		if strings.Contains(name, "top-secret") || strings.Contains(name, "acme") {
			t.Fatalf("header names leaked a value: %q", name)
		}
	}
	if server.EnvNames != nil {
		t.Fatalf("env names=%v want none", server.EnvNames)
	}
}

// Validation is the same gate the form and the host pass through, so it has to
// reject the shapes a mis-typed entry can take.
func TestMCPServerDraftValidation(t *testing.T) {
	cases := map[string]MCPServerDraft{
		"no name":          {Command: "npx"},
		"padded name":      {Name: " files", Command: "npx"},
		"separator in name": {Name: "a/b", Command: "npx"},
		"unknown transport": {Name: "a", Command: "npx", Transport: "websocket"},
		"stdio no command": {Name: "a", Transport: "stdio"},
		"http no url":      {Name: "a", Transport: "http", Command: "npx"},
		"command and url":  {Name: "a", Command: "npx", URL: "https://example.test"},
		"relative url":     {Name: "a", URL: "/mcp"},
		"ftp url":          {Name: "a", URL: "ftp://example.test/mcp"},
		"empty env name":   {Name: "a", Command: "npx", Env: map[string]string{"": "x"}},
		"named env key":    {Name: "a", Command: "npx", Env: map[string]string{"A=B": "x"}},
		"empty header":     {Name: "a", URL: "https://example.test", Headers: map[string]string{"": "x"}},
	}
	for label, draft := range cases {
		dir := t.TempDir()
		_, err := AddMCPServer(dir, SettingSourceProject, draft, []SettingSource{SettingSourceProject})
		if err == nil {
			t.Fatalf("%s: accepted %+v", label, draft)
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".cursor", "mcp.json")); statErr == nil {
			t.Fatalf("%s: wrote a file for an invalid draft", label)
		}
	}
}

// transport and type are two spellings of one field; a file that says different
// things with them has no answer the client could trust.
func TestMCPJSONRejectsConflictingTransportSpellings(t *testing.T) {
	dir := t.TempDir()
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"),
		`{"mcpServers": {"a": {"url": "https://example.test", "transport": "http", "type": "sse"}}}`)

	_, err := ListMCPServers(dir, []SettingSource{SettingSourceProject})
	if err == nil || !strings.Contains(err.Error(), `transport "http" and type "sse"`) {
		t.Fatalf("error=%v want the conflicting spellings named", err)
	}

	// Agreeing spellings (including the streamable-http alias) are not a
	// conflict, and the listing reports the name the client uses.
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"),
		`{"mcpServers": {"a": {"url": "https://example.test", "type": "streamable-http"}}}`)
	listing, err := ListMCPServers(dir, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if server, _ := serverNamed(listing, "a"); server.Transport != "http" {
		t.Fatalf("transport=%q want http", server.Transport)
	}
}

// The non-runtime path starts what the files enable. A disabled project entry
// wins over an enabled user entry of the same name: off means off, not "fall
// back to the other file".
func TestMergeMCPFromSourcesSkipsDisabledAndKeepsTheShadow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeMCPFile(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers": {
		"shared": {"command": "user-command"},
		"user-only": {"command": "user-only"}
	}}`)
	dir := t.TempDir()
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"), `{"mcpServers": {
		"shared": {"command": "project-command", "enabled": false},
		"project-only": {"command": "project-only"}
	}}`)

	opts := Options{Local: LocalOptions{SettingSources: []SettingSource{SettingSourceUser, SettingSourceProject}}}
	merged, err := mergeMCPFromSources(dir, opts)
	if err != nil {
		t.Fatalf("mergeMCPFromSources: %v", err)
	}
	if _, ok := merged.MCPServers["shared"]; ok {
		t.Fatal("a disabled project entry fell back to the user entry")
	}
	if _, ok := merged.MCPServers["user-only"]; !ok {
		t.Fatal("user-only was dropped")
	}
	if _, ok := merged.MCPServers["project-only"]; !ok {
		t.Fatal("project-only was dropped")
	}

	// Inline options are the caller's direct intent and are not filtered.
	opts.MCPServers = map[string]MCPServerConfig{"shared": {Command: "inline"}}
	merged, err = mergeMCPFromSources(dir, opts)
	if err != nil {
		t.Fatalf("mergeMCPFromSources with inline: %v", err)
	}
	if cfg, ok := merged.MCPServers["shared"]; !ok || cfg.Command != "inline" {
		t.Fatalf("shared=%+v want the inline entry", cfg)
	}
}

// Env nil means keep on update; an explicitly empty map clears it. The
// distinction is what lets a form send only the fields the person changed.
func TestUpdateMCPServerEnvNilKeepsAndEmptyClears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, path, `{"mcpServers": {"a": {"command": "x", "env": {"TOKEN": "one"}}}}`)

	if _, err := UpdateMCPServer(dir, SettingSourceProject, MCPServerDraft{Name: "a", Command: "x"}, []SettingSource{SettingSourceProject}); err != nil {
		t.Fatalf("update with nil env: %v", err)
	}
	entry := mcpEntry(t, mcpObject(t, readMCPFile(t, path)), "a")
	if string(mcpObject(t, entry["env"])["TOKEN"]) != `"one"` {
		t.Fatalf("env=%s want it kept", entry["env"])
	}

	if _, err := UpdateMCPServer(dir, SettingSourceProject, MCPServerDraft{Name: "a", Command: "x", Env: map[string]string{}}, []SettingSource{SettingSourceProject}); err != nil {
		t.Fatalf("update with empty env: %v", err)
	}
	entry = mcpEntry(t, mcpObject(t, readMCPFile(t, path)), "a")
	if _, ok := entry["env"]; ok {
		t.Fatalf("env=%s want it cleared", entry["env"])
	}
}

// Removing a project entry reveals the same-named user entry again; the
// listing is what tells a manager which file now owns the name.
func TestRemoveMCPServerRevealsTheUserEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeMCPFile(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers": {"shared": {"command": "user-command"}}}`)
	dir := t.TempDir()
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"), `{"mcpServers": {"shared": {"command": "project-command"}}}`)

	sources := []SettingSource{SettingSourceUser, SettingSourceProject}
	listing, err := RemoveMCPServer(dir, SettingSourceProject, "shared", sources)
	if err != nil {
		t.Fatalf("RemoveMCPServer: %v", err)
	}
	server, ok := serverNamed(listing, "shared")
	if !ok || server.Command != "user-command" {
		t.Fatalf("shared=%+v want the user entry", server)
	}
	if server.Source != filepath.Join(home, ".cursor", "mcp.json") {
		t.Fatalf("source=%q want the user file", server.Source)
	}
}

func TestMCPConfigPathRefusesASourceWithoutAFile(t *testing.T) {
	if _, err := MCPConfigPath(t.TempDir(), SettingSource("elsewhere")); err == nil {
		t.Fatal("a non-file source was accepted")
	}
}

func TestAddMCPServerRefusesAnExistingName(t *testing.T) {
	dir := t.TempDir()
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"), `{"mcpServers": {"a": {"command": "x"}}}`)
	if _, err := AddMCPServer(dir, SettingSourceProject, MCPServerDraft{Name: "a", Command: "y"}, []SettingSource{SettingSourceProject}); err == nil {
		t.Fatal("add accepted an existing name")
	}
	if _, err := UpdateMCPServer(dir, SettingSourceProject, MCPServerDraft{Name: "missing", Command: "y"}, []SettingSource{SettingSourceProject}); err == nil {
		t.Fatal("update accepted a missing name")
	}
	if _, err := RemoveMCPServer(dir, SettingSourceProject, "missing", []SettingSource{SettingSourceProject}); err == nil {
		t.Fatal("remove accepted a missing name")
	}
	if _, err := SetMCPServerEnabled(dir, SettingSourceProject, "missing", false, []SettingSource{SettingSourceProject}); err == nil {
		t.Fatal("toggle accepted a missing name")
	}
}
