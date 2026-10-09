// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"path/filepath"

	"github.com/anvil-dev/anvil/internal/hooks"
	"github.com/anvil-dev/anvil/internal/rules"
	"github.com/anvil-dev/anvil/internal/settings"
)

// Prewarm resolves project/user MCP JSON, rules, and hooks for cwd without starting
// an agent or requiring the Cursor app (0.10+). release may be used to drop any
// best-effort caches (currently a no-op cleanup).
func Prewarm(ctx context.Context, cwd string, sources []SettingSource) (release func(), err error) {
	_ = ctx
	if cwd == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, newError("resolve cwd", false, err)
	}
	opts := Options{Local: LocalOptions{CWD: abs, SettingSources: sources}}
	if _, err := mergeMCPFromSources(abs, opts); err != nil {
		return nil, err
	}
	loadCursor := opts.Local.HasSettingSource(SettingSourceProject)
	if _, err := rules.Load(abs, rules.Options{
		LoadProject: true,
		LoadCursor:  loadCursor || opts.Local.LoadCursorRules,
	}); err != nil {
		return nil, newError("prewarm rules", false, err)
	}
	if loadCursor {
		if _, _, err := hooks.Load(abs); err != nil {
			return nil, newError("prewarm hooks", false, err)
		}
	}
	// Touch user mcp path resolution when requested.
	if opts.Local.HasSettingSource(SettingSourceUser) {
		if _, err := settings.UserMCPPath(); err != nil {
			return nil, newError("prewarm user path", false, err)
		}
	}
	return func() {}, nil
}
