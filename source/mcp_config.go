// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/anvil-dev/anvil/internal/settings"
)

// This file writes the two Cursor-compatible mcp.json files. It edits raw
// JSON instead of decoding into a struct, because the file belongs to the user
// and to other tools: a server entry this milestone knows nothing about, or a
// field a future one adds, must come back out the way it went in. The parsed
// model stays in internal/settings; only the writer needs the raw shape.

// MCPConfigError is one problem with an mcp.json file. A parse failure carries
// the line and column the parser pointed at; a validation failure carries the
// path and the reason no write happened.
type MCPConfigError struct {
	Path    string
	Message string
	// Offset is the byte offset the JSON parser reported, when it reported one.
	Offset int
	Line   int
	Column int
}

func (e *MCPConfigError) Error() string {
	if e == nil {
		return ""
	}
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %s", e.Path, e.Line, e.Column, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// MCPServerDraft is one server as a caller wants it written.
//
// Env and Headers follow "nil means keep": Update leaves the file's values
// alone when the map is nil, and clears them when it is empty but non-nil. Add
// treats nil and empty alike (nothing to write).
type MCPServerDraft struct {
	Name      string
	Transport string // stdio | http | sse; streamable-http folds to http; empty infers
	Command   string
	Args      []string
	Env       map[string]string
	Headers   map[string]string
	URL       string
	// Enabled nil means enabled, which is the file's own default. A pointer
	// keeps an Add from meaning "disabled" just because the zero value is false.
	Enabled *bool
}

// MCPConfigPath names the file a scope writes: the project file for
// SettingSourceProject, the user file for SettingSourceUser. No other source
// has a file, so no other source can be written.
func MCPConfigPath(cwd string, source SettingSource) (string, error) {
	switch source {
	case SettingSourceProject:
		dir, err := resolveListCWD(cwd)
		if err != nil {
			return "", err
		}
		return settings.ProjectMCPPath(dir), nil
	case SettingSourceUser:
		path, err := settings.UserMCPPath()
		if err != nil {
			return "", newError("resolve user mcp path", false, err)
		}
		return path, nil
	default:
		return "", newError(fmt.Sprintf("mcp config: %q has no file", source), false, nil)
	}
}

// AddMCPServer writes a new entry and returns the listing a new session would
// read. It refuses a name that already exists in the file.
func AddMCPServer(cwd string, scope SettingSource, draft MCPServerDraft, sources []SettingSource) (MCPListing, error) {
	return mutateMCPConfig(cwd, scope, sources, func(doc *mcpDocument) error {
		if _, ok := doc.servers[draft.Name]; ok {
			return doc.errorf("server %q already exists", draft.Name)
		}
		entry, err := newMCPEntry(doc, draft)
		if err != nil {
			return err
		}
		doc.servers[draft.Name] = entry
		return nil
	})
}

// UpdateMCPServer replaces the fields a draft names on an existing entry. The
// entry's unknown fields survive, and so do env/header values the draft leaves
// nil.
func UpdateMCPServer(cwd string, scope SettingSource, draft MCPServerDraft, sources []SettingSource) (MCPListing, error) {
	return mutateMCPConfig(cwd, scope, sources, func(doc *mcpDocument) error {
		entry, ok := doc.servers[draft.Name]
		if !ok {
			return doc.errorf("server %q does not exist", draft.Name)
		}
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(entry, &fields); err != nil || fields == nil {
			fields = map[string]json.RawMessage{}
		}
		if err := applyMCPDraft(doc, fields, draft, false); err != nil {
			return err
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			return doc.errorf("encode server %q: %v", draft.Name, err)
		}
		doc.servers[draft.Name] = raw
		return nil
	})
}

// RemoveMCPServer deletes one entry. A same-named entry in the other file may
// become visible again; the returned listing says which file now wins.
func RemoveMCPServer(cwd string, scope SettingSource, name string, sources []SettingSource) (MCPListing, error) {
	return mutateMCPConfig(cwd, scope, sources, func(doc *mcpDocument) error {
		if _, ok := doc.servers[name]; !ok {
			return doc.errorf("server %q does not exist", name)
		}
		delete(doc.servers, name)
		return nil
	})
}

// SetMCPServerEnabled turns one entry off or on without deleting it. Disabling
// writes `enabled: false`; enabling deletes the key, because absence is how
// every other MCP client spells "on".
func SetMCPServerEnabled(cwd string, scope SettingSource, name string, enabled bool, sources []SettingSource) (MCPListing, error) {
	return mutateMCPConfig(cwd, scope, sources, func(doc *mcpDocument) error {
		entry, ok := doc.servers[name]
		if !ok {
			return doc.errorf("server %q does not exist", name)
		}
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(entry, &fields); err != nil || fields == nil {
			fields = map[string]json.RawMessage{}
		}
		if enabled {
			delete(fields, "enabled")
		} else {
			fields["enabled"] = json.RawMessage("false")
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			return doc.errorf("encode server %q: %v", name, err)
		}
		doc.servers[name] = raw
		return nil
	})
}

func mutateMCPConfig(cwd string, scope SettingSource, sources []SettingSource, mutate func(*mcpDocument) error) (MCPListing, error) {
	path, err := MCPConfigPath(cwd, scope)
	if err != nil {
		return MCPListing{}, err
	}
	doc, err := loadMCPDocument(path)
	if err != nil {
		return MCPListing{}, err
	}
	if err := mutate(doc); err != nil {
		return MCPListing{}, err
	}
	if err := doc.save(); err != nil {
		return MCPListing{}, err
	}
	listing, err := ListMCPServers(cwd, sources)
	if err != nil {
		// The write happened. Saying only "listing failed" would invite a retry
		// that then answers "already exists", so the message says which half
		// worked.
		return MCPListing{}, newError("mcp.json was written, but listing the servers failed", false, err)
	}
	return listing, nil
}

// mcpDocument is one file, held as raw pieces so unknown fields survive.
type mcpDocument struct {
	path    string
	top     map[string]json.RawMessage
	servers map[string]json.RawMessage
}

// loadMCPDocument reads a file without interpreting entries it will not touch.
// A missing file is an empty document; a file that does not parse is an error
// and the caller must not write over it.
func loadMCPDocument(path string) (*mcpDocument, error) {
	doc := &mcpDocument{path: path, top: map[string]json.RawMessage{}, servers: map[string]json.RawMessage{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return nil, newError("read mcp.json", false, err)
	}
	if err := json.Unmarshal(data, &doc.top); err != nil {
		return nil, mcpParseError(path, data, err)
	}
	if doc.top == nil {
		doc.top = map[string]json.RawMessage{}
	}
	// The same shape the loader reads. A raw object parse can succeed where the
	// server map cannot (a list instead of an object, a string where args
	// belongs), and writing over that would launder a broken file into a valid
	// one that silently lost what it said.
	var probe struct {
		MCPServers map[string]settings.MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, mcpParseError(path, data, err)
	}
	if raw, ok := doc.top["mcpServers"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &doc.servers); err != nil {
			return nil, mcpParseError(path, data, err)
		}
		if doc.servers == nil {
			doc.servers = map[string]json.RawMessage{}
		}
	}
	return doc, nil
}

// save replaces the file atomically: a temporary file in the same directory,
// then a rename. A crash leaves either the old file or the new one, never half
// of either.
func (d *mcpDocument) save() error {
	servers, err := json.Marshal(d.servers)
	if err != nil {
		return d.errorf("encode mcpServers: %v", err)
	}
	d.top["mcpServers"] = servers
	body, err := json.MarshalIndent(d.top, "", "  ")
	if err != nil {
		return d.errorf("encode mcp.json: %v", err)
	}
	body = append(body, '\n')

	dir := filepath.Dir(d.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return newError("create mcp.json directory", false, err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(d.path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".mcp.json-*")
	if err != nil {
		return newError("create temporary mcp.json", false, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return newError("write mcp.json", false, err)
	}
	// CreateTemp makes the file 0600; the configuration is not a secret, and a
	// file other MCP clients read should keep the mode it had.
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return newError("chmod mcp.json", false, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return newError("close mcp.json", false, err)
	}
	if err := os.Rename(tmpName, d.path); err != nil {
		cleanup()
		return newError("replace mcp.json", false, err)
	}
	return nil
}

func (d *mcpDocument) errorf(format string, args ...any) error {
	return newError("mcp.json", false, &MCPConfigError{Path: d.path, Message: fmt.Sprintf(format, args...)})
}

func mcpParseError(path string, data []byte, err error) error {
	return newError("mcp.json does not parse", false, mcpConfigError(path, data, err))
}

// mcpConfigError names the file a parse or validation failure came from, and the
// position the parser pointed at when it reported one. It is used by the reader
// as well as the writer: a listing that cannot parse a file still has to say
// which file and where, because that is what a client opens.
func mcpConfigError(path string, data []byte, err error) *MCPConfigError {
	ce := &MCPConfigError{Path: path, Message: err.Error()}
	var offset int
	var syntax *json.SyntaxError
	var typed *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		offset = int(syntax.Offset)
		ce.Message = syntax.Error()
	case errors.As(err, &typed):
		offset = int(typed.Offset)
		ce.Message = typed.Error()
	}
	ce.Offset = offset
	if offset > 0 {
		ce.Line, ce.Column = lineColumn(data, offset)
	}
	return ce
}

func lineColumn(data []byte, offset int) (int, int) {
	if offset > len(data) {
		offset = len(data)
	}
	if offset < 0 {
		offset = 0
	}
	line, column := 1, 1
	for _, b := range data[:offset] {
		if b == '\n' {
			line, column = line+1, 1
			continue
		}
		column++
	}
	return line, column
}

func newMCPEntry(doc *mcpDocument, draft MCPServerDraft) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := applyMCPDraft(doc, fields, draft, true); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, doc.errorf("encode server %q: %v", draft.Name, err)
	}
	return raw, nil
}

// applyMCPDraft validates a draft and writes its fields into a raw entry. When
// isNew is false, nil Env/Headers mean "keep what the entry already has".
func applyMCPDraft(doc *mcpDocument, fields map[string]json.RawMessage, draft MCPServerDraft, isNew bool) error {
	transport, err := validateMCPDraft(doc.path, draft)
	if err != nil {
		return err
	}

	setOrDelete(fields, "command", draft.Command)
	if len(draft.Args) > 0 {
		raw, err := json.Marshal(draft.Args)
		if err != nil {
			return doc.errorf("encode args: %v", err)
		}
		fields["args"] = raw
	} else {
		delete(fields, "args")
	}
	if draft.Env != nil || isNew {
		if err := setRawMap(fields, "env", draft.Env); err != nil {
			return doc.errorf("encode env: %v", err)
		}
	}
	setOrDelete(fields, "url", draft.URL)
	if draft.Headers != nil || isNew {
		if err := setRawMap(fields, "headers", draft.Headers); err != nil {
			return doc.errorf("encode headers: %v", err)
		}
	}

	// Keep the file's own spelling: `type` where the entry already used it,
	// `transport` everywhere else (new entries included).
	key := "transport"
	if _, hasTransport := fields["transport"]; !hasTransport {
		if _, hasType := fields["type"]; hasType {
			key = "type"
		}
	}
	delete(fields, "transport")
	delete(fields, "type")
	if transport != "" {
		fields[key] = json.RawMessage(fmt.Sprintf("%q", transport))
	}

	if draft.Enabled != nil && !*draft.Enabled {
		fields["enabled"] = json.RawMessage("false")
	} else {
		delete(fields, "enabled")
	}
	return nil
}

func setOrDelete(fields map[string]json.RawMessage, key, value string) {
	if strings.TrimSpace(value) == "" {
		delete(fields, key)
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		delete(fields, key)
		return
	}
	fields[key] = raw
}

func setRawMap(fields map[string]json.RawMessage, key string, value map[string]string) error {
	if len(value) == 0 {
		delete(fields, key)
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fields[key] = raw
	return nil
}

// validateMCPDraft is the gate every write goes through. It returns the
// transport to write ("http" for the streamable-http spelling).
func validateMCPDraft(path string, draft MCPServerDraft) (string, error) {
	fail := func(format string, args ...any) (string, error) {
		return "", newError("mcp.json", false, &MCPConfigError{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	name := draft.Name
	if strings.TrimSpace(name) == "" {
		return fail("a server needs a name")
	}
	if name != strings.TrimSpace(name) {
		return fail("server name %q has leading or trailing spaces", name)
	}
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return fail("server name %q contains a path separator or control character", name)
		}
	}

	transport := settings.MCPServer{Transport: draft.Transport}.TransportName()
	switch transport {
	case "", "stdio", "http", "sse":
	default:
		return fail("server %q has unknown transport %q", name, draft.Transport)
	}

	command := strings.TrimSpace(draft.Command) != ""
	if transport == "" {
		switch {
		case command && draft.URL != "":
			return fail("server %q sets both a command and a url", name)
		case !command && draft.URL == "":
			return fail("server %q needs a command (stdio) or a url (http/sse)", name)
		case command:
			transport = "stdio"
		default:
			transport = "http"
		}
	}
	switch transport {
	case "stdio":
		if !command {
			return fail("server %q is stdio and needs a command", name)
		}
		if draft.URL != "" {
			return fail("server %q is stdio and cannot also set a url", name)
		}
	default:
		if draft.URL == "" {
			return fail("server %q is %s and needs a url", name, transport)
		}
		if command {
			return fail("server %q is %s and cannot also set a command", name, transport)
		}
		parsed, err := url.Parse(draft.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fail("server %q needs an absolute http(s) url, got %q", name, draft.URL)
		}
	}

	for key := range draft.Env {
		if strings.TrimSpace(key) == "" {
			return fail("server %q has an empty env name", name)
		}
		if strings.ContainsRune(key, '=') || strings.ContainsRune(key, '\x00') {
			return fail("server %q has an env name %q that cannot be passed to a process", name, key)
		}
	}
	for key := range draft.Headers {
		if strings.TrimSpace(key) == "" {
			return fail("server %q has an empty header name", name)
		}
	}
	return transport, nil
}
