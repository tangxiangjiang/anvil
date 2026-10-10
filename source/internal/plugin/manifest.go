// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package plugin

import (
	"encoding/json"
	"fmt"
	"strings"
)

// APIVersion is the supported plugin.json anvil_api.
const APIVersion = 1

// Permission is a declared plugin capability gate.
type Permission string

const (
	PermFSRead       Permission = "fs.read"
	PermFSWrite      Permission = "fs.write"
	PermNetNone      Permission = "net.none"
	PermNet          Permission = "net"
	PermProcessSpawn Permission = "process.spawn"
)

var knownPermissions = map[Permission]bool{
	PermFSRead:       true,
	PermFSWrite:      true,
	PermNetNone:      true,
	PermNet:          true,
	PermProcessSpawn: true,
}

// Manifest is plugin.json.
type Manifest struct {
	ID           string       `json:"id"`
	Version      string       `json:"version"`
	AnvilAPI     int          `json:"anvil_api"`
	Capabilities []string     `json:"capabilities"`
	Permissions  []Permission `json:"permissions"`
	Command      string       `json:"command,omitempty"`
	Args         []string     `json:"args,omitempty"`
}

// ParseManifest decodes and validates a plugin.json document.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("plugin manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate checks required fields and known permissions / API version.
func (m Manifest) Validate() error {
	id := strings.TrimSpace(m.ID)
	if id == "" {
		return fmt.Errorf("plugin manifest: id is required")
	}
	if m.AnvilAPI == 0 {
		return fmt.Errorf("plugin manifest %q: anvil_api is required", id)
	}
	if m.AnvilAPI != APIVersion {
		return fmt.Errorf("plugin manifest %q: unsupported anvil_api %d (want %d)", id, m.AnvilAPI, APIVersion)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("plugin manifest %q: version is required", id)
	}
	for _, p := range m.Permissions {
		if !knownPermissions[p] {
			return fmt.Errorf("plugin manifest %q: unknown permission %q", id, p)
		}
	}
	return nil
}

// Exceeds reports whether m requests a permission outside allowed.
func (m Manifest) Exceeds(allowed map[Permission]bool) error {
	if allowed == nil {
		return nil
	}
	for _, p := range m.Permissions {
		if p == PermNetNone {
			continue
		}
		if !allowed[p] {
			return fmt.Errorf("plugin %q permission %q not allowed by policy", m.ID, p)
		}
	}
	return nil
}
