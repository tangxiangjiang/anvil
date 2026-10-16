// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package rules

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultMaxChars = 24 * 1024

// Options controls which rule files are loaded.
type Options struct {
	// LoadProject enables AGENTS.md and .anvil/rules/*.md
	LoadProject bool
	// LoadCursor enables .cursor/rules/* (off by default)
	LoadCursor bool
	// MaxChars truncates combined rules (0 = defaultMaxChars)
	MaxChars int
}

// File is one rule file discovery found, in the order Load concatenates it.
//
// It exists so a caller can say *which* rules applied, not just that some did.
// The flags are decided by the same pass that builds the prompt text, so the
// list cannot drift from what the model actually received: Included is false
// for a file the budget did not reach, and Truncated is true for the one file
// that was cut to fit.
type File struct {
	// Label is the heading Load writes above this body: "AGENTS.md", or the
	// workspace-relative path for everything else.
	Label string
	// Path is the file on disk.
	Path string
	// Bytes is the size of the body after trimming.
	Bytes int
	// Included reports whether the body reached the prompt at all.
	Included bool
	// Truncated reports that the body was cut short by the character budget.
	Truncated bool
}

// Discover reports the rule files under cwd and how much of each one Load would
// use. It reads the files it names, because "empty" and "unreadable" have to be
// decided the same way for both callers.
func Discover(cwd string, opts Options) ([]File, error) {
	d, err := discover(cwd, opts)
	if err != nil {
		return nil, err
	}
	return d.files, nil
}

// Load discovers and concatenates rule files under cwd.
// Discovery order: AGENTS.md, then .anvil/rules/*.md (sorted), then optional .cursor/rules/*.mdc|*.md.
func Load(cwd string, opts Options) (string, error) {
	d, err := discover(cwd, opts)
	if err != nil {
		return "", err
	}
	return strings.Join(d.chunks, "\n\n"), nil
}

// discovery is one pass over the rule files: the text Load joins, and the
// per-file report Discover hands back.
type discovery struct {
	files  []File
	chunks []string
}

func discover(cwd string, opts Options) (discovery, error) {
	var d discovery
	if !opts.LoadProject && !opts.LoadCursor {
		return d, nil
	}
	max := opts.MaxChars
	if max <= 0 {
		max = defaultMaxChars
	}
	total := 0

	addFile := func(path string, label string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		base := filepath.Base(path)
		lower := strings.ToLower(base)
		if lower == ".env" || strings.HasSuffix(lower, ".env") || lower == "credentials.json" {
			return nil
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			return nil
		}
		file := File{Label: label, Path: path, Bytes: len(body)}
		chunk := "## " + label + "\n" + body
		if total+len(chunk) > max {
			remain := max - total
			if remain <= 0 {
				// A file the budget never reached. It was found, so it is
				// reported — as present but unused, which is not the same as
				// absent.
				d.files = append(d.files, file)
				return nil
			}
			chunk = chunk[:remain] + "\n…[rules truncated]"
			file.Included = true
			file.Truncated = true
			d.files = append(d.files, file)
			d.chunks = append(d.chunks, chunk)
			total = max
			return nil
		}
		file.Included = true
		d.files = append(d.files, file)
		d.chunks = append(d.chunks, chunk)
		total += len(chunk)
		return nil
	}

	if opts.LoadProject {
		if err := addFile(filepath.Join(cwd, "AGENTS.md"), "AGENTS.md"); err != nil {
			return d, err
		}
		anvilDir := filepath.Join(cwd, ".anvil", "rules")
		entries, err := os.ReadDir(anvilDir)
		if err == nil {
			var names []string
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.HasSuffix(strings.ToLower(name), ".md") {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				if err := addFile(filepath.Join(anvilDir, name), ".anvil/rules/"+name); err != nil {
					return d, err
				}
			}
		}
	}
	if opts.LoadCursor {
		cursorDir := filepath.Join(cwd, ".cursor", "rules")
		entries, err := os.ReadDir(cursorDir)
		if err == nil {
			var names []string
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				lower := strings.ToLower(name)
				if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".mdc") {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				if err := addFile(filepath.Join(cursorDir, name), ".cursor/rules/"+name); err != nil {
					return d, err
				}
			}
		}
	}
	return d, nil
}
