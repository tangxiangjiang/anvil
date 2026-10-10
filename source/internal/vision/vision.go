// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

// Package vision loads and downscales images for multimodal LLM requests (0.19).
package vision

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"
)

const (
	DefaultMaxSide  = 2048
	DefaultMaxBytes = 4 << 20 // 4 MiB encoded
)

// Options controls processing limits.
type Options struct {
	MaxSide  int // longest side; 0 = DefaultMaxSide
	MaxBytes int // max base64 payload bytes; 0 = DefaultMaxBytes
}

// Image is processed image data ready for providers.
type Image struct {
	MIME    string
	Data    []byte // raw encoded file bytes (png/jpeg)
	Width   int
	Height  int
	Detail  string
	Source  string // path or "bytes" for diagnostics (never log Data)
}

// LoadFile reads and processes an image from disk.
func LoadFile(path string, opts Options) (*Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Process(data, sniffMIME(path, data), opts)
}

// Process decodes, optionally downscales, and re-encodes image bytes.
func Process(data []byte, mime string, opts Options) (*Image, error) {
	if opts.MaxSide <= 0 {
		opts.MaxSide = DefaultMaxSide
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime == "" {
		mime = sniffMIME("", data)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unsupported or corrupt image: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid image dimensions")
	}
	if w > opts.MaxSide || h > opts.MaxSide {
		img = resizeMaxSide(img, opts.MaxSide)
		b = img.Bounds()
		w, h = b.Dx(), b.Dy()
	}

	outMIME := "image/png"
	var buf bytes.Buffer
	switch {
	case format == "jpeg" || mime == "image/jpeg" || mime == "image/jpg":
		outMIME = "image/jpeg"
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, err
		}
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	}
	encoded := buf.Bytes()
	// If still too large as base64 (~4/3), try stronger jpeg.
	if b64Len(len(encoded)) > opts.MaxBytes {
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, img, &jpeg.Options{Quality: 70}); err != nil {
			return nil, fmt.Errorf("image exceeds max size (%d bytes encoded budget)", opts.MaxBytes)
		}
		encoded = jbuf.Bytes()
		outMIME = "image/jpeg"
	}
	if b64Len(len(encoded)) > opts.MaxBytes {
		return nil, fmt.Errorf("image exceeds max size (%d bytes encoded budget)", opts.MaxBytes)
	}
	return &Image{MIME: outMIME, Data: encoded, Width: w, Height: h}, nil
}

func b64Len(n int) int {
	return base64.StdEncoding.EncodedLen(n)
}

// DataURL returns a data: URL (for OpenAI image_url).
func (img *Image) DataURL() string {
	if img == nil {
		return ""
	}
	return "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

// Base64 returns raw base64 (for Anthropic).
func (img *Image) Base64() string {
	if img == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(img.Data)
}

func sniffMIME(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 {
		return "image/jpeg"
	}
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	return "application/octet-stream"
}

func resizeMaxSide(src image.Image, maxSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := float64(maxSide) / float64(w)
	if h > w {
		scale = float64(maxSide) / float64(h)
	}
	if scale >= 1 {
		return src
	}
	nw := int(float64(w)*scale + 0.5)
	nh := int(float64(h)*scale + 0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
