// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package vision_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/vision"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcessDownscalesLargeImage(t *testing.T) {
	raw := makePNG(t, 4000, 3000)
	img, err := vision.Process(raw, "image/png", vision.Options{MaxSide: 512, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if img.Width > 512 || img.Height > 512 {
		t.Fatalf("got %dx%d", img.Width, img.Height)
	}
	if vision.DefaultMaxSide != 2048 {
		t.Fatal("unexpected default")
	}
}

func TestLoadFileAndRejectCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.png")
	if err := os.WriteFile(path, makePNG(t, 32, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := vision.LoadFile(path, vision.Options{})
	if err != nil || img.MIME == "" || len(img.Data) == 0 {
		t.Fatalf("%v %+v", err, img)
	}
	if _, err := vision.Process([]byte("not-an-image"), "", vision.Options{}); err == nil {
		t.Fatal("expected corrupt error")
	}
}

func TestDataURLHasNoRawInStringHelpers(t *testing.T) {
	img, err := vision.Process(makePNG(t, 8, 8), "image/png", vision.Options{})
	if err != nil {
		t.Fatal(err)
	}
	u := img.DataURL()
	if u == "" || img.Base64() == "" {
		t.Fatal("empty helpers")
	}
}
