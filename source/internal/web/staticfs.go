// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var staticRoot embed.FS

// StaticFS returns the embedded UI filesystem (rooted at static/).
func StaticFS() (fs.FS, error) {
	return fs.Sub(staticRoot, "static")
}
