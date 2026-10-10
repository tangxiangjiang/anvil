// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package plugin

import (
	"fmt"
	"strings"
)

// Policy is the daemon/worker allowlist intersection gate.
type Policy struct {
	// Available, when non-empty, is the only ids that may load.
	Available []string
	// Deny is always rejected even if listed in Available / job request.
	Deny []string
	// MaxPermissions, when non-empty, caps manifest permissions.
	MaxPermissions []Permission
}

// AllowID checks deny/available for a plugin id (before Init).
func (p *Policy) AllowID(id string) error {
	if p == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	for _, d := range p.Deny {
		if strings.TrimSpace(d) == id {
			return fmt.Errorf("plugin %q denied by policy", id)
		}
	}
	if len(p.Available) == 0 {
		return nil
	}
	for _, a := range p.Available {
		if strings.TrimSpace(a) == id {
			return nil
		}
	}
	return fmt.Errorf("plugin %q not in available allowlist", id)
}

// AllowManifest checks permissions after manifest is known.
func (p *Policy) AllowManifest(m Manifest) error {
	if p == nil {
		return nil
	}
	if err := p.AllowID(m.ID); err != nil {
		return err
	}
	if len(p.MaxPermissions) == 0 {
		return nil
	}
	allowed := map[Permission]bool{}
	for _, x := range p.MaxPermissions {
		allowed[x] = true
	}
	return m.Exceeds(allowed)
}

// ResolveIDs intersects job-requested plugins with Available and subtracts Deny.
// Empty requested → copy of Available (minus Deny). Empty Available with empty
// requested → nil (load nothing by default at daemon).
func (p *Policy) ResolveIDs(requested []string) ([]string, error) {
	if p == nil {
		return unique(requested), nil
	}
	deny := map[string]bool{}
	for _, d := range p.Deny {
		deny[strings.TrimSpace(d)] = true
	}
	avail := map[string]bool{}
	for _, a := range p.Available {
		a = strings.TrimSpace(a)
		if a != "" && !deny[a] {
			avail[a] = true
		}
	}
	req := unique(requested)
	if len(req) == 0 {
		if len(p.Available) == 0 {
			return nil, nil
		}
		out := make([]string, 0, len(avail))
		for _, a := range p.Available {
			a = strings.TrimSpace(a)
			if avail[a] {
				out = append(out, a)
			}
		}
		return out, nil
	}
	var out []string
	for _, id := range req {
		if deny[id] {
			return nil, fmt.Errorf("plugin %q denied by policy", id)
		}
		if len(p.Available) > 0 && !avail[id] {
			return nil, fmt.Errorf("plugin %q not in available allowlist", id)
		}
		out = append(out, id)
	}
	return out, nil
}

func unique(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
