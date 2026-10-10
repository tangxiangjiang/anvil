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

func insideCWD(cwd, abs string) bool {
	cwd = filepath.Clean(cwd)
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
