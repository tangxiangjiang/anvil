// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"

	"github.com/anvil-dev/anvil/internal/index"
)

// IndexBuildOptions configures anvil.BuildIndex.
type IndexBuildOptions struct {
	EmbedBaseURL string
	EmbedAPIKey  string
	EmbedModel   string
}

// IndexBuildResult summarizes BuildIndex.
type IndexBuildResult = index.BuildResult

// IndexStatus describes {cwd}/.anvil/index/search.db.
type IndexStatus = index.Status

// BuildIndex walks cwd and updates the project search index (0.13+).
func BuildIndex(ctx context.Context, cwd string, opts IndexBuildOptions) (*IndexBuildResult, error) {
	return index.Build(ctx, cwd, index.BuildOptions{
		EmbedURL:    opts.EmbedBaseURL,
		EmbedAPIKey: opts.EmbedAPIKey,
		EmbedModel:  opts.EmbedModel,
	})
}

// IndexStat returns status for the project index.
func IndexStat(cwd string) (IndexStatus, error) {
	return index.Stat(cwd)
}

// ClearIndex removes {cwd}/.anvil/index.
func ClearIndex(cwd string) error {
	return index.Clear(cwd)
}
