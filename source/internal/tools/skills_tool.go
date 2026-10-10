// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/skills"
)

// SkillsConfig configures list_skills / read_skill discovery.
type SkillsConfig struct {
	SkillDirs   []string
	ScanProject bool
	ScanUser    bool
}

// SetSkillsConfig installs skill discovery settings for skill tools.
func (r *Registry) SetSkillsConfig(cfg SkillsConfig) {
	if r == nil {
		return
	}
	r.skillsCfg = cfg
}

func (r *Registry) discoverSkills() ([]skills.Skill, error) {
	return skills.Discover(r.cwd, skills.DiscoverOpts{
		SkillDirs:   r.skillsCfg.SkillDirs,
		ScanProject: r.skillsCfg.ScanProject,
		ScanUser:    r.skillsCfg.ScanUser,
	})
}

type listSkillsTool struct{ r *Registry }

func (t *listSkillsTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "list_skills",
		Description: "List available project/user skills (id, name, description). Use read_skill to load full instructions.",
		Parameters:  mustSchema(`{"type":"object","properties":{}}`),
	}
}

func (t *listSkillsTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	_ = args
	list, err := t.r.discoverSkills()
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	if len(list) == 0 {
		return Result{Content: "(no skills discovered)"}
	}
	var b strings.Builder
	for _, sk := range list {
		fmt.Fprintf(&b, "- %s: %s\n  %s\n", sk.ID, sk.Name, sk.Description)
	}
	return Result{Content: strings.TrimSpace(b.String())}
}

type readSkillTool struct{ r *Registry }

func (t *readSkillTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "read_skill",
		Description: "Read a skill's SKILL.md body (and optional references/ file under the skill directory).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Skill directory id"},
				"path":{"type":"string","description":"Optional relative path under the skill dir (e.g. references/foo.md)"}
			},
			"required":["id"]
		}`),
	}
}

func (t *readSkillTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	var in struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{Content: "invalid args: " + err.Error(), IsError: true}
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return Result{Content: "id is required", IsError: true}
	}
	list, err := t.r.discoverSkills()
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var sk *skills.Skill
	for i := range list {
		if list[i].ID == in.ID {
			sk = &list[i]
			break
		}
	}
	if sk == nil {
		return Result{Content: "unknown skill: " + in.ID, IsError: true}
	}
	if strings.TrimSpace(in.Path) == "" {
		return Result{Content: sk.Body}
	}
	p, err := skills.ResolveReference(sk.Dir, in.Path)
	if err != nil {
		return Result{Content: "path denied or missing under skill dir", IsError: true}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	text := string(data)
	if utf8.RuneCountInString(text) > 12000 {
		text = string([]rune(text)[:12000]) + "\n…[truncated]"
	}
	return Result{Content: text}
}
