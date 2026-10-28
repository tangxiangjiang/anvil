// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"sync"
	"testing"
)

func TestCloseIdempotentConcurrent(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.Close()
		}()
	}
	wg.Wait()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
}
