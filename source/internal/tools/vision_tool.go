// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/vision"
)

// VisionConfig enables read_image and size limits (0.19+).
type VisionConfig struct {
	Enabled  bool
	MaxSide  int
	MaxBytes int
}

// SetVisionConfig registers read_image when Enabled.
func (r *Registry) SetVisionConfig(cfg VisionConfig) {
	if r == nil {
		return
	}
	r.Unregister("read_image")
	r.visionCfg = cfg
	r.visionMu.Lock()
	r.visionQ = nil
	r.visionMu.Unlock()
	if cfg.Enabled {
		r.register(&readImageTool{r: r})
	}
}

// ResolvePath exposes cwd sandbox resolution for host attachments.
func (r *Registry) ResolvePath(p string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("nil registry")
	}
	return r.resolvePath(p)
}

// QueueVisionPart enqueues an image part for the next Completer turn.
func (r *Registry) QueueVisionPart(p llm.ContentPart) {
	if r == nil {
		return
	}
	r.visionMu.Lock()
	r.visionQ = append(r.visionQ, p)
	r.visionMu.Unlock()
}

// TakeVisionParts drains the queued image parts (may be empty).
func (r *Registry) TakeVisionParts() []llm.ContentPart {
	if r == nil {
		return nil
	}
	r.visionMu.Lock()
	defer r.visionMu.Unlock()
	if len(r.visionQ) == 0 {
		return nil
	}
	out := r.visionQ
	r.visionQ = nil
	return out
}

type readImageTool struct{ r *Registry }

func (t *readImageTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "read_image",
		Description: "Load an image from the workspace sandbox and queue it for the next model turn. " +
			"Tool result is text confirmation; the runtime injects the image automatically.",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"path":{"type":"string","description":"Image path under cwd"},
				"max_side":{"type":"integer","description":"Optional longest-side downscale override"},
				"detail":{"type":"string","description":"OpenAI detail hint: low|high|auto"}
			},
			"required":["path"]
		}`),
	}
}

func (t *readImageTool) Execute(ctx context.Context, args json.RawMessage) Result {
	_ = ctx
	if !t.r.visionCfg.Enabled {
		return Result{Content: "vision is disabled", IsError: true}
	}
	var in struct {
		Path    string `json:"path"`
		MaxSide int    `json:"max_side"`
		Detail  string `json:"detail"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{Content: "invalid args: " + err.Error(), IsError: true}
		}
	}
	abs, err := t.r.resolvePath(in.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	opts := vision.Options{
		MaxSide:  t.r.visionCfg.MaxSide,
		MaxBytes: t.r.visionCfg.MaxBytes,
	}
	if in.MaxSide > 0 {
		opts.MaxSide = in.MaxSide
	}
	img, err := vision.LoadFile(abs, opts)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	img.Source = in.Path
	img.Detail = in.Detail
	t.r.QueueVisionPart(llm.ContentPart{
		Type:   "image",
		MIME:   img.MIME,
		Data:   img.Data,
		Detail: img.Detail,
	})
	return Result{Content: fmt.Sprintf("image queued (%s, %dx%d, %s)", in.Path, img.Width, img.Height, img.MIME)}
}
