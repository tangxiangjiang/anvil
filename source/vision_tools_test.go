// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/redact"
)

func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type captureCompleter struct {
	reqs []llm.Request
}

func (c *captureCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	c.reqs = append(c.reqs, req)
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "seen"}}, nil
}

type readImageThenFinish struct {
	n    int
	reqs []llm.Request
}

func (c *readImageThenFinish) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	c.reqs = append(c.reqs, req)
	c.n++
	if c.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "1",
				Name: "read_image",
				Args: json.RawMessage(`{"path":"shot.png"}`),
			}},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "got image"}}, nil
}

func TestVisionDisabledNoToolNoParts(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range a.registry.Names() {
		if name == "read_image" {
			t.Fatal("read_image must be absent when Vision disabled")
		}
	}
	_, err = a.Send(context.Background(), "hi", WithAttachments(ImageAttachment{Path: "x.png"}))
	if err == nil || !strings.Contains(err.Error(), "Vision.Enabled") {
		t.Fatalf("expected vision required error, got %v", err)
	}
}

func TestVisionAttachmentOpenAIShapeViaFakeCompleter(t *testing.T) {
	dir := t.TempDir()
	writeTestPNG(t, filepath.Join(dir, "ui.png"), 64, 48)
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
		Vision: VisionOptions{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cap := &captureCompleter{}
	a.complete = cap

	run, err := a.Send(context.Background(), "look", WithAttachments(ImageAttachment{Path: "ui.png", Detail: "high"}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil || res.Status != StatusFinished {
		t.Fatalf("%v %+v", err, res)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("reqs=%d", len(cap.reqs))
	}
	msg := cap.reqs[0].Messages[len(cap.reqs[0].Messages)-1]
	if msg.Role != llm.RoleUser || len(msg.Parts) != 1 || msg.Parts[0].Type != "image" {
		t.Fatalf("%+v", msg)
	}
	if len(msg.Parts[0].Data) == 0 || msg.Parts[0].Detail != "high" {
		t.Fatalf("%+v", msg.Parts[0])
	}
	// Persist path must not embed base64 in content.
	if strings.Contains(msg.Content, "base64,") {
		t.Fatal("user content must not embed data url")
	}
}

func TestVisionPathEscapeDenied(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
		Vision: VisionOptions{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.Send(context.Background(), "x", WithAttachments(ImageAttachment{Path: "../outside.png"}))
	if err == nil || !strings.Contains(err.Error(), "escapes cwd") {
		t.Fatalf("got %v", err)
	}
}

func TestReadImageInjectsNextTurn(t *testing.T) {
	dir := t.TempDir()
	writeTestPNG(t, filepath.Join(dir, "shot.png"), 32, 32)
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
		Vision: VisionOptions{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	script := &readImageThenFinish{}
	a.complete = script

	run, err := a.Send(context.Background(), "inspect shot")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Wait(context.Background())
	if err != nil || res.Result != "got image" {
		t.Fatalf("%v %+v", err, res)
	}
	if len(script.reqs) < 2 {
		t.Fatalf("expected 2 completes, got %d", len(script.reqs))
	}
	second := script.reqs[1]
	found := false
	for _, m := range second.Messages {
		if m.Role == llm.RoleUser && len(m.Parts) > 0 && m.Parts[0].Type == "image" {
			found = true
			redacted := redact.String("data:image/png;base64," + strings.Repeat("B", 48))
			if strings.Contains(redacted, strings.Repeat("B", 48)) {
				t.Fatal("redact should scrub data urls")
			}
			break
		}
	}
	if !found {
		t.Fatalf("second request missing injected image: %+v", second.Messages)
	}
}

func TestVisionDisallowedGroup(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey:          "k",
		Model:           Model{ID: "m"},
		Local:           LocalOptions{CWD: dir},
		Vision:          VisionOptions{Enabled: true},
		DisallowedTools: []string{"vision"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if toolPresent(a, "read_image") {
		t.Fatal("vision group should be stripped")
	}
}
