//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"fmt"

	"github.com/anvil-dev/anvil/internal/llm"
)

// CreateForEval injects a provider at the existing child-client seam. It is only
// available in evaluation builds; default SDK builds expose no testing API.
func CreateForEval(ctx context.Context, opts Options, completer llm.Completer) (*Agent, error) {
	if completer == nil {
		return nil, fmt.Errorf("eval completer is nil")
	}
	opts.completer = completer
	return Create(ctx, opts)
}
