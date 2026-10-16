// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The path containment rules here are deliberately independent of the SDK's
// tool sandbox. They govern a different promise: the sandbox says what *tools*
// may touch once the model asks, while this says what the host will read into a
// prompt on a client's word. A bug in the editing client must not be able to
// turn "the user mentioned a file" into "put ~/.ssh/id_rsa in the model's
// context", so the host checks for itself rather than assuming the client
// sanitised anything.
//
// The host imports the public anvil package only, so it cannot reuse the
// sandbox helper in internal/tools; this file is the whole implementation.

// resolveWorkspacePath resolves a client-supplied, workspace-relative path
// against root and refuses anything that lands outside it.
//
// Three escapes are refused, each with its own message so a client bug is
// diagnosable: an absolute path, a `..` traversal, and a symlink that points
// out of the workspace. The returned path is absolute and symlink-free.
func resolveWorkspacePath(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("path is empty")
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", errors.New("absolute paths are not accepted; paths are workspace-relative: " + rel)
	}
	// Reject on the cleaned path so "a/../../b" cannot slip through as "b".
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the workspace: " + rel)
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(realRoot, clean))
	if err != nil {
		return "", err
	}
	if !within(realRoot, target) {
		// Only reachable through a symlink: the lexical check above already
		// rejected a textual escape, so this one resolved to somewhere else.
		return "", errors.New("path leaves the workspace through a link: " + rel)
	}
	return target, nil
}

// within reports whether path is root itself or a descendant of it. Both must
// be absolute and symlink-free. Windows compares case-insensitively because its
// filesystem does; a case-sensitive test there would reject a path that names
// the very same file.
func within(root, path string) bool {
	if path == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix))
	}
	return strings.HasPrefix(path, prefix)
}

// workspaceListing renders one directory as a flat listing: entries first, with
// a trailing separator marking the directories, capped at maxListingEntries.
//
// It is a listing and not a dump on purpose. A folder mention means "this part
// of the tree is relevant", and reading every file under it would fill the
// context with material the user did not ask for and the model did not need.
func workspaceListing(dir string, limit int) (string, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, err
	}
	truncated := len(entries) > limit
	if truncated {
		entries = entries[:limit]
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += string(filepath.Separator)
		}
		b.WriteString(name)
		b.WriteString("\n")
	}
	return b.String(), truncated, nil
}
