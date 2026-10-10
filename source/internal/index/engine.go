// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index

import "context"

// ProjectSearch is the 0.22 engine surface (Build / Status / Search / Close).
// Package-level Build/Stat/Search/Clear implement the same contract without
// requiring a long-lived handle; Engine wraps a cwd for callers that prefer OO style.
type ProjectSearch interface {
	Build(ctx context.Context, opts BuildOptions) (*BuildResult, error)
	Status(ctx context.Context) (Status, error)
	Search(ctx context.Context, q Query) ([]Hit, error)
	Close() error
}

// Engine is a cwd-bound ProjectSearch that opens the DB per operation
// (safe for concurrent readers; one builder at a time per cwd recommended).
type Engine struct {
	CWD string
	// Embed is used for Search hybrid/vector when Query.Mode needs embeddings.
	Embed BuildOptions
}

// NewEngine returns a ProjectSearch for cwd.
func NewEngine(cwd string) *Engine {
	return &Engine{CWD: cwd}
}

func (e *Engine) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	return Build(ctx, e.CWD, opts)
}

func (e *Engine) Status(ctx context.Context) (Status, error) {
	_ = ctx
	return Stat(e.CWD)
}

func (e *Engine) Search(ctx context.Context, q Query) ([]Hit, error) {
	return SearchWithEmbed(ctx, e.CWD, q, e.Embed)
}

func (e *Engine) Close() error { return nil }

// Ensure Engine implements ProjectSearch.
var _ ProjectSearch = (*Engine)(nil)
