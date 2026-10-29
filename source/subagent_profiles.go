// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	subagentProfilesVersion = 1
	maxSubagentProfiles     = 32
	maxProfileDescription   = 512
	maxProfileInstructions  = 32 * 1024
	maxProfileTools         = 64
)

var subagentProfileID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// SubagentProfileState is the canonical on-disk profile state.
type SubagentProfileState struct {
	Path     string            `json:"path"`
	Exists   bool              `json:"exists"`
	Profiles []SubagentProfile `json:"profiles"`
	Problem  string            `json:"problem,omitempty"`
}

type subagentProfileDocument struct {
	Version  int               `json:"version"`
	Profiles []SubagentProfile `json:"profiles"`
}

// SubagentProfilesPath returns the workspace-shared profile file.
func SubagentProfilesPath(workspace string) string {
	return filepath.Join(workspace, ".anvil", "subagents.json")
}

// LoadSubagentProfiles strictly reads and validates workspace profiles.
func LoadSubagentProfiles(workspace string) SubagentProfileState {
	path := SubagentProfilesPath(workspace)
	state := SubagentProfileState{Path: path, Profiles: []SubagentProfile{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state
	}
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	state.Exists = true
	var doc subagentProfileDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		state.Problem = fmt.Sprintf("parse %s: %v", path, err)
		return state
	}
	if err := requireJSONEOF(dec); err != nil {
		state.Problem = fmt.Sprintf("parse %s: %v", path, err)
		return state
	}
	if doc.Version != subagentProfilesVersion {
		state.Problem = fmt.Sprintf("unsupported subagent profile version %d", doc.Version)
		return state
	}
	if doc.Profiles == nil {
		doc.Profiles = []SubagentProfile{}
	}
	if err := ValidateSubagentProfiles(doc.Profiles); err != nil {
		state.Problem = err.Error()
		return state
	}
	state.Profiles = doc.Profiles
	return state
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

// ValidateSubagentProfiles checks the strict profile contract.
func ValidateSubagentProfiles(profiles []SubagentProfile) error {
	if len(profiles) > maxSubagentProfiles {
		return fmt.Errorf("at most %d subagent profiles are allowed", maxSubagentProfiles)
	}
	seen := make(map[string]bool, len(profiles))
	for i := range profiles {
		p := &profiles[i]
		if !subagentProfileID.MatchString(p.ID) {
			return fmt.Errorf("profile %d has invalid id %q", i+1, p.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("duplicate subagent profile id %q", p.ID)
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Description) == "" || len(p.Description) > maxProfileDescription {
			return fmt.Errorf("profile %q description must be nonblank and at most %d bytes", p.ID, maxProfileDescription)
		}
		if strings.TrimSpace(p.Instructions) == "" || len(p.Instructions) > maxProfileInstructions {
			return fmt.Errorf("profile %q instructions must be nonblank and at most %d bytes", p.ID, maxProfileInstructions)
		}
		if len(p.Tools) > maxProfileTools {
			return fmt.Errorf("profile %q has too many tool selectors", p.ID)
		}
		if p.MaxSteps < 0 {
			return fmt.Errorf("profile %q max_steps cannot be negative", p.ID)
		}
		for _, tool := range p.Tools {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("profile %q has an empty tool selector", p.ID)
			}
		}
	}
	return nil
}

// SaveSubagentProfiles validates and atomically replaces the profile file.
func SaveSubagentProfiles(workspace string, profiles []SubagentProfile) (SubagentProfileState, error) {
	if err := ValidateSubagentProfiles(profiles); err != nil {
		return LoadSubagentProfiles(workspace), err
	}
	path := SubagentProfilesPath(workspace)
	doc := subagentProfileDocument{Version: subagentProfilesVersion, Profiles: profiles}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return LoadSubagentProfiles(workspace), err
	}
	data = append(data, '\n')
	if err := writeSubagentProfiles(path, data); err != nil {
		return LoadSubagentProfiles(workspace), err
	}
	return LoadSubagentProfiles(workspace), nil
}

// UpsertSubagentProfile updates a profile in place or appends a new profile.
func UpsertSubagentProfile(workspace string, profile SubagentProfile) (SubagentProfileState, error) {
	state := LoadSubagentProfiles(workspace)
	if state.Problem != "" {
		return state, errors.New(state.Problem)
	}
	profiles := append([]SubagentProfile(nil), state.Profiles...)
	found := false
	for i := range profiles {
		if profiles[i].ID == profile.ID {
			profiles[i] = profile
			found = true
			break
		}
	}
	if !found {
		profiles = append(profiles, profile)
	}
	return SaveSubagentProfiles(workspace, profiles)
}

// RemoveSubagentProfile removes a profile without rewriting malformed files.
func RemoveSubagentProfile(workspace, id string) (SubagentProfileState, error) {
	state := LoadSubagentProfiles(workspace)
	if state.Problem != "" {
		return state, errors.New(state.Problem)
	}
	profiles := make([]SubagentProfile, 0, len(state.Profiles))
	for _, profile := range state.Profiles {
		if profile.ID != id {
			profiles = append(profiles, profile)
		}
	}
	return SaveSubagentProfiles(workspace, profiles)
}

// SubagentToolGroups returns stable selectors suitable for profile editors.
func SubagentToolGroups() []string {
	groups := []string{"edit", "git", "lsp", "mcp", "read", "search", "shell", "skills", "verify", "vision", "web", "write"}
	sort.Strings(groups)
	return groups
}

func writeSubagentProfiles(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".subagents-*.json")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return atomicReplace(tmp, path)
}
