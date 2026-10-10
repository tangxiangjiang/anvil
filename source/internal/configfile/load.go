// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package configfile loads operator configs from JSON or YAML by file extension (0.23).
package configfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads path into dest. Extension selects decoder: .json | .yaml | .yml.
func Load(path string, dest any) error {
	if dest == nil {
		return fmt.Errorf("configfile: nil dest")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, dest); err != nil {
			return fmt.Errorf("parse json config %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, dest); err != nil {
			return fmt.Errorf("parse yaml config %s: %w", path, err)
		}
	default:
		return fmt.Errorf("configfile: unsupported extension %q (use .json, .yaml, or .yml)", ext)
	}
	return nil
}
