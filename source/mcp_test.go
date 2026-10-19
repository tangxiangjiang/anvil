// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMCPFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serverNamed(listing MCPListing, name string) (MCPServerInfo, bool) {
	for _, server := range listing.Servers {
		if server.Name == name {
			return server, true
		}
	}
	return MCPServerInfo{}, false
}

// The listing is what a person reads before a turn runs, so it has to name the
// server, the transport the SDK would pick, and the file that declared it.
func TestListMCPServersReadsTheProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, path, `{"mcpServers": {
		"files": {"command": "npx", "args": ["server-files"]},
		"docs": {"url": "https://example.test/mcp"},
		"both": {"command": "node", "url": "http://localhost:1234"},
		"empty": {}
	}}`)

	listing, err := ListMCPServers(dir, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(listing.Sources) != 1 {
		t.Fatalf("sources=%v want one project layer", listing.Sources)
	}
	if got := listing.Sources[0]; got.Source != SettingSourceProject || !got.Exists || got.Path != path {
		t.Fatalf("project source=%+v want %s (exists)", got, path)
	}

	names := make([]string, 0, len(listing.Servers))
	for _, server := range listing.Servers {
		names = append(names, server.Name)
	}
	// Sorted by name: a listing that reordered between calls would look like a
	// different set of servers to a reader.
	if want := []string{"both", "docs", "empty", "files"}; strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("servers=%v want %v", names, want)
	}

	cases := map[string]string{"files": "stdio", "docs": "http", "both": "stdio", "empty": ""}
	for name, transport := range cases {
		server, ok := serverNamed(listing, name)
		if !ok {
			t.Fatalf("missing server %s", name)
		}
		if server.Transport != transport {
			t.Fatalf("%s transport=%q want %q", name, server.Transport, transport)
		}
		if server.Source != path {
			t.Fatalf("%s source=%q want %q", name, server.Source, path)
		}
	}
}

// Off means off: no layer is inspected, so the listing cannot imply that a
// session would read a file it was never told to read.
func TestListMCPServersNamesNothingWithoutASource(t *testing.T) {
	dir := t.TempDir()
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"), `{"mcpServers": {"files": {"command": "npx"}}}`)

	listing, err := ListMCPServers(dir, nil)
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(listing.Sources) != 0 || len(listing.Servers) != 0 {
		t.Fatalf("listing=%+v want nothing", listing)
	}
}

// "You turned this on and there is no file" is not the same answer as "there
// is a file and it is empty", so the absent layer is reported, not dropped.
func TestListMCPServersReportsAMissingFile(t *testing.T) {
	dir := t.TempDir()
	listing, err := ListMCPServers(dir, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(listing.Sources) != 1 || listing.Sources[0].Exists {
		t.Fatalf("sources=%+v want one absent project layer", listing.Sources)
	}
	if len(listing.Servers) != 0 {
		t.Fatalf("servers=%v want none", listing.Servers)
	}
}

// A file that will not parse must not read as "nothing configured": the error
// is the only thing that tells the user which file to fix.
func TestListMCPServersFailsOnAMalformedFile(t *testing.T) {
	dir := t.TempDir()
	writeMCPFile(t, filepath.Join(dir, ".cursor", "mcp.json"), `{"mcpServers": {`)

	_, err := ListMCPServers(dir, []SettingSource{SettingSourceProject})
	if err == nil {
		t.Fatal("a malformed file was accepted")
	}
	if !strings.Contains(err.Error(), "parse") || !strings.Contains(err.Error(), "mcp.json") {
		t.Fatalf("error=%q want it to name the parse failure and the file", err)
	}
}

// The precedence a session applies: user first, then project overwrites a name
// both files declare, so the listing and the loaded config agree.
func TestListMCPServersLetsTheProjectOverrideTheUserFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // how os.UserHomeDir resolves on Windows
	userPath := filepath.Join(home, ".cursor", "mcp.json")
	writeMCPFile(t, userPath, `{"mcpServers": {
		"shared": {"command": "user-command"},
		"user-only": {"url": "https://example.test/user"}
	}}`)

	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".cursor", "mcp.json")
	writeMCPFile(t, projectPath, `{"mcpServers": {
		"shared": {"command": "project-command"},
		"project-only": {"command": "project-command"}
	}}`)

	listing, err := ListMCPServers(dir, []SettingSource{SettingSourceUser, SettingSourceProject})
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(listing.Sources) != 2 ||
		listing.Sources[0].Source != SettingSourceUser ||
		listing.Sources[1].Source != SettingSourceProject {
		t.Fatalf("sources=%+v want user then project", listing.Sources)
	}
	if len(listing.Servers) != 3 {
		t.Fatalf("servers=%+v want three", listing.Servers)
	}
	shared, ok := serverNamed(listing, "shared")
	if !ok || shared.Source != projectPath {
		t.Fatalf("shared=%+v want the project entry", shared)
	}
	if userOnly, ok := serverNamed(listing, "user-only"); !ok || userOnly.Source != userPath {
		t.Fatalf("user-only=%+v want the user entry", userOnly)
	}
}
