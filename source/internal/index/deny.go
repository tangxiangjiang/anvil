// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index

import (
	"path/filepath"
	"strings"
)

var denyNames = map[string]bool{
	".env": true, ".env.local": true, ".env.production": true,
	"id_rsa": true, "id_ed25519": true, "credentials.json": true,
}

var denyExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".pdf": true, ".zip": true, ".gz": true, ".tar": true, ".7z": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".mp3": true, ".mp4": true, ".mov": true, ".wasm": true,
	".pyc": true, ".class": true, ".o": true, ".a": true,
	".db": true, ".sqlite": true, ".sqlite3": true,
}

var denyDir = map[string]bool{
	".git": true, ".anvil": true, "node_modules": true, "vendor": true,
	".venv": true, "venv": true, "dist": true, "build": true,
	"__pycache__": true, ".idea": true, ".vscode": true,
	"target": true, ".next": true, "coverage": true,
}

const maxFileBytes = 512 << 10 // 512 KiB per file

func skipDir(name string) bool {
	return denyDir[name] || strings.HasPrefix(name, ".")
}

func skipFile(rel string, size int64) bool {
	if size > maxFileBytes || size <= 0 {
		return true
	}
	base := filepath.Base(rel)
	if denyNames[base] || strings.HasPrefix(base, ".env.") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	if denyExt[ext] {
		return true
	}
	// skip lockfiles that are huge/noisy
	switch base {
	case "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.sum":
		return true
	}
	return false
}
