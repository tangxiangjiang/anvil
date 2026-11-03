// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build !windows

package anvil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func finalWorktreePath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func secureWorktreePath(path string, directory bool) error {
	mode := os.FileMode(0o600)
	if directory {
		mode = 0o700
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	return verifySecureWorktreePath(path)
}

func verifySecureWorktreePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("managed path permissions are not private")
	}
	return nil
}

func syncWorktreeDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func worktreePathContainsAbsolute(data []byte, paths ...string) bool {
	text := string(data)
	for _, path := range paths {
		if path != "" && strings.Contains(text, filepath.Clean(path)) {
			return true
		}
	}
	return false
}
