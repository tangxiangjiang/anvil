// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package skills discovers Agent Skill directories and formats them for prompts/tools (0.14).
package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxBodyRunes   = 12000
	defaultAlwaysBudget   = 8000
	defaultRouterBudget   = 6000
	defaultRouterTopK     = 3
)

// Skill is a discovered skill package.
type Skill struct {
	ID          string
	Name        string
	Description string
	Body        string
	Dir         string
}

// DiscoverOpts controls skill directory scanning.
type DiscoverOpts struct {
	SkillDirs   []string // extra absolute or cwd-relative dirs containing skill packages
	ScanProject bool     // .anvil/skills + .cursor/skills
	ScanUser    bool     // ~/.anvil/skills
}

// Discover finds skills under configured roots. Later roots override same ID.
func Discover(cwd string, opts DiscoverOpts) ([]Skill, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	byID := map[string]Skill{}
	var roots []string
	if opts.ScanProject {
		roots = append(roots,
			filepath.Join(abs, ".anvil", "skills"),
			filepath.Join(abs, ".cursor", "skills"),
		)
	}
	if opts.ScanUser {
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, filepath.Join(home, ".anvil", "skills"))
		}
	}
	for _, d := range opts.SkillDirs {
		if d == "" {
			continue
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(abs, d)
		}
		roots = append(roots, filepath.Clean(d))
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			id := e.Name()
			dir := filepath.Join(root, id)
			sk, err := loadSkill(id, dir)
			if err != nil {
				continue
			}
			byID[id] = sk
		}
	}
	out := make([]Skill, 0, len(byID))
	for _, sk := range byID {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func loadSkill(id, dir string) (Skill, error) {
	path := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	name, desc, body := parseFrontMatter(string(data))
	if name == "" {
		name = id
	}
	if utf8.RuneCountInString(body) > defaultMaxBodyRunes {
		body = string([]rune(body)[:defaultMaxBodyRunes]) + "\n…[truncated]"
	}
	return Skill{ID: id, Name: name, Description: desc, Body: body, Dir: dir}, nil
}

func parseFrontMatter(content string) (name, description, body string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") && content != "---" {
		return "", "", strings.TrimSpace(content)
	}
	rest := strings.TrimPrefix(content, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", strings.TrimSpace(content)
	}
	fm := rest[:end]
	body = strings.TrimSpace(rest[end+len("\n---"):])
	body = strings.TrimPrefix(body, "\n")
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch key {
		case "name":
			name = val
		case "description":
			description = val
		}
	}
	return name, description, body
}

// LoadAlwaysIDs reads .anvil/skills.json always list (optional).
func LoadAlwaysIDs(cwd string) []string {
	path := filepath.Join(cwd, ".anvil", "skills.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Always []string `json:"always"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg.Always
}

// SelectByID returns skills whose ID is in ids (order of ids).
func SelectByID(all []Skill, ids []string) []Skill {
	m := map[string]Skill{}
	for _, sk := range all {
		m[sk.ID] = sk
	}
	var out []Skill
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		if sk, ok := m[id]; ok {
			out = append(out, sk)
			seen[id] = true
		}
	}
	return out
}

// FormatAlways builds capped markdown for always-injected skills.
func FormatAlways(skills []Skill, budget int) string {
	if budget <= 0 {
		budget = defaultAlwaysBudget
	}
	return formatSkills(skills, budget, "always")
}

// Router picks top-k skills by keyword overlap with prompt (no LLM).
func Router(all []Skill, excludeIDs []string, prompt string, topK, budget int) []Skill {
	if topK <= 0 {
		topK = defaultRouterTopK
	}
	if budget <= 0 {
		budget = defaultRouterBudget
	}
	ex := map[string]bool{}
	for _, id := range excludeIDs {
		ex[id] = true
	}
	qTokens := tokenize(prompt)
	type scored struct {
		sk    Skill
		score int
	}
	var ranked []scored
	for _, sk := range all {
		if ex[sk.ID] {
			continue
		}
		hay := tokenize(sk.Name + " " + sk.Description + " " + sk.ID)
		score := overlap(qTokens, hay)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, scored{sk: sk, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].sk.ID < ranked[j].sk.ID
		}
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}
	var out []Skill
	used := 0
	for _, r := range ranked {
		block := formatOne(r.sk)
		n := utf8.RuneCountInString(block)
		if used+n > budget && used > 0 {
			break
		}
		if n > budget && used == 0 {
			runes := []rune(block)
			block = string(runes[:budget]) + "…"
			out = append(out, Skill{ID: r.sk.ID, Name: r.sk.Name, Description: r.sk.Description, Body: block, Dir: r.sk.Dir})
			break
		}
		out = append(out, r.sk)
		used += n
	}
	return out
}

// FormatRouter formats router-selected skills for prompt injection.
func FormatRouter(skills []Skill, budget int) string {
	if budget <= 0 {
		budget = defaultRouterBudget
	}
	return formatSkills(skills, budget, "matched")
}

func formatSkills(skills []Skill, budget int, kind string) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, sk := range skills {
		block := "## Skill: " + sk.Name + " (" + sk.ID + ") [" + kind + "]\n"
		if sk.Description != "" {
			block += sk.Description + "\n\n"
		}
		block += sk.Body + "\n\n"
		n := utf8.RuneCountInString(block)
		if used+n > budget {
			remain := budget - used
			if remain < 80 {
				break
			}
			runes := []rune(block)
			b.WriteString(string(runes[:remain]) + "…\n")
			break
		}
		b.WriteString(block)
		used += n
	}
	return strings.TrimSpace(b.String())
}

func formatOne(sk Skill) string {
	return "## Skill: " + sk.Name + " (" + sk.ID + ")\n" + sk.Description + "\n\n" + sk.Body
}

func tokenize(s string) map[string]bool {
	out := map[string]bool{}
	var cur strings.Builder
	flush := func() {
		t := strings.ToLower(cur.String())
		cur.Reset()
		if len(t) < 3 {
			return
		}
		out[t] = true
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func overlap(a, b map[string]bool) int {
	n := 0
	for t := range a {
		if b[t] {
			n++
		}
	}
	return n
}

// ResolveReference resolves a path under a skill directory (sandbox).
func ResolveReference(skillDir, rel string) (string, error) {
	if rel == "" {
		return "", os.ErrInvalid
	}
	absSkill, err := filepath.Abs(skillDir)
	if err != nil {
		return "", err
	}
	absSkill, err = filepath.EvalSymlinks(absSkill)
	if err != nil {
		absSkill = filepath.Clean(skillDir)
	}
	candidate := rel
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absSkill, rel)
	}
	candidate = filepath.Clean(candidate)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		resolved = candidate
	}
	relOut, err := filepath.Rel(absSkill, resolved)
	if err != nil || relOut == ".." || strings.HasPrefix(relOut, ".."+string(filepath.Separator)) {
		return "", os.ErrPermission
	}
	return resolved, nil
}
