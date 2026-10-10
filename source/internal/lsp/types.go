// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp

// Position is 0-based line/character (LSP).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a file location.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Diagnostic is a publishDiagnostics item.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"` // 1 err 2 warn 3 info 4 hint
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// TextEdit is an LSP text edit.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// WorkspaceEdit from rename / codeAction.
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []DocumentChange      `json:"documentChanges,omitempty"`
}

// DocumentChange is TextDocumentEdit or create/rename/delete (we handle text only).
type DocumentChange struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version *int   `json:"version"`
	} `json:"textDocument"`
	Edits []TextEdit `json:"edits"`
}

// ServerConfig maps a language to a stdio language server command.
type ServerConfig struct {
	Language string   `json:"language"`
	Pattern  string   `json:"pattern"` // e.g. **/*.go
	Command  string   `json:"command"`
	Args     []string `json:"args"`
}

// Config is Options.LSP / .anvil/lsp.json.
type Config struct {
	Enabled         bool           `json:"enabled"`
	Required        bool           `json:"required"`
	Servers         []ServerConfig `json:"servers"`
	InitTimeoutMs   int            `json:"init_timeout_ms"`
	RequestTimeoutMs int           `json:"request_timeout_ms"`
}
