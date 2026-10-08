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

// Load discovers and concatenates rule files under cwd.
// Discovery order: AGENTS.md, then .anvil/rules/*.md (sorted), then optional .cursor/rules/*.mdc|*.md.
func Load(cwd string, opts Options) (string, error) {
	if !opts.LoadProject && !opts.LoadCursor {
		return "", nil
	}
	max := opts.MaxChars
	if max <= 0 {
		max = defaultMaxChars
	}
	var parts []string
	var total int

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
		chunk := "## " + label + "\n" + body
		if total+len(chunk) > max {
			remain := max - total
			if remain <= 0 {
				return nil
			}
			chunk = chunk[:remain] + "\n…[rules truncated]"
			parts = append(parts, chunk)
			total = max
			return nil
		}
		parts = append(parts, chunk)
		total += len(chunk)
		return nil
	}

	if opts.LoadProject {
		if err := addFile(filepath.Join(cwd, "AGENTS.md"), "AGENTS.md"); err != nil {
			return "", err
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
					return "", err
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
					return "", err
				}
			}
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
