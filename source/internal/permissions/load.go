// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the rule file, workspace-relative. It is meant to be committed
// with the workspace: a policy that only one machine knows about is a policy
// nobody can review.
const FileName = "permissions.json"

// Error is a rule-file problem, with the place in the text when the reader
// knows one. A client that shows the file turns Line and Column into a
// squiggle; a client that only prints it uses Error.
type Error struct {
	Message string
	// Line and Column are one-based, and both zero when the problem belongs to
	// no particular place — a version this build does not read, or a rule whose
	// effect is misspelled.
	Line   int
	Column int
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Message)
	}
	return e.Message
}

// Position reports the line and column an error from Load or Parse points at,
// or (0, 0) when it points at no particular place.
func Position(err error) (line, col int) {
	var e *Error
	if errors.As(err, &e) {
		return e.Line, e.Column
	}
	return 0, 0
}

// Load reads the rule file from a workspace.
//
// A missing file is not an error and not an empty policy: it returns (nil, nil),
// and the caller carries nil to mean "no rules" — behaviour identical to before
// there were rules at all. A file that exists but cannot be understood is an
// error, and it is meant to be fatal: silently ignoring a policy somebody wrote
// is worse than refusing to start.
func Load(cwd string) (*Set, error) {
	p := filepath.Join(cwd, ".anvil", FileName)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	set, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return set, nil
}

// HomeEnv names the directory that replaces ~/.anvil. It is what tests use to
// keep a machine-wide policy out of a test run, and what a portable install can
// use to keep everything under one directory.
const HomeEnv = "ANVIL_HOME"

// AnvilHome is the directory this product keeps user-level state in:
// $ANVIL_HOME when set, else ~/.anvil.
func AnvilHome() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(HomeEnv)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".anvil"), nil
}

// UserPath is the user-level rule file: $ANVIL_HOME/permissions.json, else
// ~/.anvil/permissions.json.
func UserPath() (string, error) {
	dir, err := AnvilHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// HomeDir is the user's home directory, empty when it cannot be resolved. It is
// what a `~` in a protected-path glob expands to.
func HomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// LoadUser reads the user rule file from path. A missing file is (nil, nil,
// nil); a file that will not parse is an error naming the path and the place,
// never a silent ignore.
func LoadUser(path string) (*Set, []string, error) {
	if path == "" {
		return nil, nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	set, protected, err := ParseUser(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return set, protected, nil
}

// ParseUser compiles the contents of the user rule file. It returns the rules
// and the protected-path globs the file adds; the two are separate because a
// glob is not a rule and must never be counted as one.
func ParseUser(data []byte) (*Set, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f UserFile
	if err := dec.Decode(&f); err != nil {
		return nil, nil, jsonError(err, data)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, errors.New("trailing data after the rule object")
	}
	if f.Version != 0 && f.Version != Version {
		return nil, nil, fmt.Errorf("version %d is not supported; this build reads version %d", f.Version, Version)
	}
	set := &Set{}
	for i, r := range f.Rules {
		c, err := compile(r, i, SourceUser)
		if err != nil {
			return nil, nil, err
		}
		set.rules = append(set.rules, c)
	}
	protected := make([]string, 0, len(f.Protected))
	for _, g := range f.Protected {
		g = strings.TrimSpace(g)
		if g != "" {
			protected = append(protected, g)
		}
	}
	return set, protected, nil
}

// New compiles rules a program supplied. They are validated exactly as a file's
// rules are, so a caller cannot install one that a file would have been refused
// for. No rules at all compiles to nil, which means "no opinion".
//
// A rule that carries Source and Index — as one read from a file does — keeps
// them; a rule that does not is a rule of this program's own list, sourced to
// SourceCode with its position in that list.
func New(rules []Rule) (*Set, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	set := &Set{rules: make([]rule, 0, len(rules))}
	for i, r := range rules {
		c, err := compile(r, i, SourceCode)
		if err != nil {
			return nil, err
		}
		set.rules = append(set.rules, c)
	}
	return set, nil
}

// Parse compiles the contents of a workspace rule file.
func Parse(data []byte) (*Set, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	// A misspelled key is a rule the user believes is in force and is not.
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, jsonError(err, data)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the rule object")
	}
	if f.Version != 0 && f.Version != Version {
		return nil, fmt.Errorf("version %d is not supported; this build reads version %d", f.Version, Version)
	}
	set := &Set{}
	if f.DefaultMode != nil {
		if !modeWord(*f.DefaultMode) {
			return nil, defaultModeError(*f.DefaultMode, data)
		}
		set.defaultMode = *f.DefaultMode
	}
	for i, r := range f.Rules {
		c, err := compile(r, i, SourceWorkspace)
		if err != nil {
			return nil, err
		}
		set.rules = append(set.rules, c)
	}
	return set, nil
}

// defaultModeError points at the declaration rather than at the file, because a
// value that is not a word is a line to edit. The key is found by searching the
// text: encoding/json reports an offset for a syntax error but not for a value it
// decoded happily.
func defaultModeError(value string, data []byte) error {
	msg := fmt.Sprintf("defaultMode %q is not a single lowercase word of letters, digits and underscores", value)
	offset := bytes.Index(data, []byte(`"defaultMode"`))
	if offset < 0 {
		return errors.New(msg)
	}
	// The key's own position. lineColumn counts the bytes before the offset it is
	// given, so a 0-based index in doubles as the offset that lands on the first
	// character of the key.
	line, col := lineColumn(data, int64(offset))
	return &Error{Line: line, Column: col, Message: msg}
}

// compile validates one rule. pos is the rule's position in the list it came
// from, counted from zero; source names that list. A rule that already carries
// its own source and position — one read from a file and then passed back
// through New — keeps them, so a citation does not move when lists are merged.
func compile(r Rule, pos int, source string) (rule, error) {
	effect, ok := ParseEffect(r.Effect)
	if !ok {
		return rule{}, fmt.Errorf("rule %d: effect %q is not one of allow, ask, deny", pos+1, r.Effect)
	}
	if r.Tool == "" {
		return rule{}, fmt.Errorf("rule %d: tool is required; write %q to mean every tool", pos+1, "*")
	}
	if r.Source != "" {
		source = r.Source
	}
	index := pos
	if r.Index > 0 {
		index = r.Index - 1
	}
	return rule{effect: effect, tool: r.Tool, pattern: r.Pattern, reason: r.Reason, source: source, index: index}, nil
}

// jsonError names the line and column of a syntax or type error, because the
// point of refusing a malformed file is that the user can find the mistake. An
// unknown-field error carries no offset — encoding/json does not report one —
// but it does name the field, which is what a reader needs to find it.
func jsonError(err error, data []byte) error {
	var offset int64 = -1
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntaxErr):
		offset = syntaxErr.Offset
	case errors.As(err, &typeErr):
		offset = typeErr.Offset
	}
	if offset < 0 {
		return &Error{Message: err.Error()}
	}
	line, col := lineColumn(data, offset)
	return &Error{Line: line, Column: col, Message: err.Error()}
}

// lineColumn converts a byte offset from encoding/json, which counts from one,
// into the position a text editor shows.
func lineColumn(data []byte, offset int64) (int, int) {
	line, col := 1, 1
	for i := int64(0); i < offset && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}
