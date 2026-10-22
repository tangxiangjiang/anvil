// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"embed"
	"io/fs"
)

// Embed only browser assets. The sibling package/test files and node_modules are
// development inputs, not HTTP content and must not be shipped by FileServer.
//go:embed static/index.html static/css static/js static/vendor
var staticRoot embed.FS

// StaticFS returns the embedded UI filesystem (rooted at static/).
func StaticFS() (fs.FS, error) {
	return fs.Sub(staticRoot, "static")
}
