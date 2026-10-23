// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

func pathToURI(abs string) string {
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = filepath.ToSlash(abs)
		if len(abs) >= 2 && abs[1] == ':' {
			return "file:///" + abs
		}
	}
	return "file://" + abs
}

// UriToPath is uriToPath for callers outside the package. It is the one conversion
// a workspace edit's file names go through, so a caller that checkpoints those files
// and the edit itself agree on which path each URI means.
func UriToPath(uri string) (string, error) {
	return uriToPath(uri)
}

func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported uri scheme %q", u.Scheme)
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(p, "/") && len(p) >= 3 && p[2] == ':' {
			p = p[1:]
		}
		p = filepath.FromSlash(p)
	}
	return filepath.Clean(p), nil
}

// insideCWD reports whether abs, with its symbolic links followed, lies inside
// cwd. A lexical check alone let an in-workspace symlink to a file outside the
// workspace through, so a write or a read followed it out (audit #2). A path that
// does not exist yet is judged by its nearest existing ancestor.
func insideCWD(cwd, abs string) bool {
	cwd = filepath.Clean(cwd)
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = filepath.Clean(resolved)
	}
	abs = resolveExisting(filepath.Clean(abs))
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting follows symlinks in p. Where p does not exist, it resolves the
// nearest ancestor that does and re-joins the rest, so a new file under a
// symlinked directory is still judged where it would really be written.
func resolveExisting(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolveExisting(parent), filepath.Base(p))
}

func matchPattern(pattern, relSlash string) bool {
	pattern = filepath.ToSlash(pattern)
	relSlash = filepath.ToSlash(relSlash)
	if pattern == "" {
		return true
	}
	ok, err := filepath.Match(pattern, relSlash)
	if err == nil && ok {
		return true
	}
	// **/ext style
	if strings.HasPrefix(pattern, "**/") {
		suf := strings.TrimPrefix(pattern, "**/")
		ok, _ = filepath.Match(suf, filepath.Base(relSlash))
		if ok {
			return true
		}
		ok, _ = filepath.Match(suf, relSlash)
		return ok
	}
	ok, _ = filepath.Match(pattern, filepath.Base(relSlash))
	return ok
}
