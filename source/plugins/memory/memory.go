// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package memory is the first-party development memory plugin (0.21).
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anvil-dev/anvil/internal/memory"
	"github.com/anvil-dev/anvil/internal/plugin"
	"github.com/anvil-dev/anvil/internal/tools"
)

// SessionHint is injected into the system prompt when the plugin is loaded.
const SessionHint = "Memory tools are available (memory_write / memory_read / memory_search / memory_list / memory_delete). " +
	"Persist durable project facts, conventions, and pitfalls — do not memorize every turn."

// New returns the memory plugin.
func New() plugin.Plugin {
	return &memPlugin{}
}

type memPlugin struct {
	store *memory.Store
	embed memory.EmbedConfig
}

func (p *memPlugin) ID() string { return "memory" }

func (p *memPlugin) Init(ctx context.Context, host plugin.Host) error {
	_ = ctx
	st, err := memory.Open(host.CWD(), memory.Caps{})
	if err != nil {
		return err
	}
	p.store = st
	ec := host.EmbedConfig()
	p.embed = memory.EmbedConfig{BaseURL: ec.BaseURL, APIKey: ec.APIKey, Model: ec.Model}

	toolsList := []tools.Tool{
		&tools.FuncTool{
			Name:        "memory_write",
			Description: "Persist a durable project memory entry under .anvil/memory/ (survives new agent ids on same cwd).",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"},"key":{"type":"string","description":"optional stable upsert key"},"tags":{"type":"array","items":{"type":"string"}},"source":{"type":"string"}},"required":["text"]}`),
			Handler:     p.write,
		},
		&tools.FuncTool{
			Name:        "memory_read",
			Description: "Fetch one memory entry by id or key.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"key":{"type":"string"}},"required":[]}`),
			Handler:     p.read,
		},
		&tools.FuncTool{
			Name:        "memory_list",
			Description: "List recent memory entries (optional tag filter).",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer"},"tag":{"type":"string"}}}`),
			Handler:     p.list,
		},
		&tools.FuncTool{
			Name:        "memory_search",
			Description: "Search memory entries (lexical; optional vector when embed is configured).",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}},"top_k":{"type":"integer"}},"required":["query"]}`),
			Handler:     p.search,
		},
		&tools.FuncTool{
			Name:        "memory_delete",
			Description: "Soft-delete a memory entry by id.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
			Handler:     p.delete,
		},
	}
	for _, t := range toolsList {
		if err := host.RegisterTool(t); err != nil {
			return err
		}
	}
	return nil
}

func (p *memPlugin) Close(ctx context.Context) error {
	_ = ctx
	return nil
}

func (p *memPlugin) write(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Text   string   `json:"text"`
		Key    string   `json:"key"`
		Tags   []string `json:"tags"`
		Source string   `json:"source"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	vec := memory.MaybeEmbed(ctx, p.embed, in.Text)
	e, err := p.store.Write(memory.WriteInput{
		Key: in.Key, Text: in.Text, Tags: in.Tags, Source: in.Source, Vector: vec,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s key=%q tags=%v", e.ID, e.Key, e.Tags), nil
}

func (p *memPlugin) read(ctx context.Context, args json.RawMessage) (string, error) {
	_ = ctx
	var in struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	idOrKey := strings.TrimSpace(in.ID)
	if idOrKey == "" {
		idOrKey = strings.TrimSpace(in.Key)
	}
	e, err := p.store.Read(idOrKey)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(map[string]any{
		"id": e.ID, "key": e.Key, "text": e.Text, "tags": e.Tags,
		"created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "source": e.Source,
	})
	return string(raw), nil
}

func (p *memPlugin) list(ctx context.Context, args json.RawMessage) (string, error) {
	_ = ctx
	var in struct {
		Limit int    `json:"limit"`
		Tag   string `json:"tag"`
	}
	if len(args) > 0 && string(args) != "null" {
		_ = json.Unmarshal(args, &in)
	}
	entries, err := p.store.List(memory.ListOpts{Limit: in.Limit, Tag: in.Tag})
	if err != nil {
		return "", err
	}
	type row struct {
		ID   string   `json:"id"`
		Key  string   `json:"key,omitempty"`
		Text string   `json:"text"`
		Tags []string `json:"tags,omitempty"`
	}
	rows := make([]row, 0, len(entries))
	for _, e := range entries {
		text := e.Text
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		rows = append(rows, row{ID: e.ID, Key: e.Key, Text: text, Tags: e.Tags})
	}
	raw, _ := json.Marshal(rows)
	return string(raw), nil
}

func (p *memPlugin) search(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Query string   `json:"query"`
		Tags  []string `json:"tags"`
		TopK  int      `json:"top_k"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	hits, err := p.store.Search(ctx, memory.SearchOpts{
		Query: in.Query, Tags: in.Tags, TopK: in.TopK, Embed: p.embed,
	})
	if err != nil {
		return "", err
	}
	type row struct {
		ID     string   `json:"id"`
		Key    string   `json:"key,omitempty"`
		Text   string   `json:"text"`
		Tags   []string `json:"tags,omitempty"`
		Score  float64  `json:"score"`
		Source string   `json:"source"`
	}
	rows := make([]row, 0, len(hits))
	for _, h := range hits {
		text := h.Entry.Text
		if len(text) > 300 {
			text = text[:300] + "…"
		}
		rows = append(rows, row{
			ID: h.Entry.ID, Key: h.Entry.Key, Text: text, Tags: h.Entry.Tags,
			Score: h.Score, Source: h.Source,
		})
	}
	raw, _ := json.Marshal(rows)
	return string(raw), nil
}

func (p *memPlugin) delete(ctx context.Context, args json.RawMessage) (string, error) {
	_ = ctx
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := p.store.Delete(in.ID); err != nil {
		return "", err
	}
	return "deleted " + in.ID, nil
}

// Manifest describes the pack.
func Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "memory",
		Version:      "0.1.0",
		AnvilAPI:     plugin.APIVersion,
		Capabilities: []string{"tools.memory"},
		Permissions:  []plugin.Permission{plugin.PermFSRead, plugin.PermFSWrite, plugin.PermNetNone},
	}
}
