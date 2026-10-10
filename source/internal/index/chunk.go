// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	defaultChunkLines = 40
	defaultOverlap    = 5
	maxChunkRunes     = 4000
)

// Chunk is one indexed snippet.
type Chunk struct {
	UID       string
	Path      string
	StartLine int
	EndLine   int
	Hash      string
	Text      string
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func chunkFile(relPath string, content []byte) []Chunk {
	text := string(content)
	if !utf8.ValidString(text) {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil
	}
	var out []Chunk
	step := defaultChunkLines - defaultOverlap
	if step < 1 {
		step = defaultChunkLines
	}
	for start := 0; start < len(lines); start += step {
		end := start + defaultChunkLines
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.Join(lines[start:end], "\n")
		body = strings.TrimSpace(body)
		if body == "" {
			if end >= len(lines) {
				break
			}
			continue
		}
		runes := []rune(body)
		if len(runes) > maxChunkRunes {
			body = string(runes[:maxChunkRunes]) + "…"
		}
		h := hashBytes([]byte(body))
		uid := hashBytes([]byte(relPath + "\x00" + h + "\x00" + strconv.Itoa(start+1)))
		out = append(out, Chunk{
			UID:       uid[:16],
			Path:      relPath,
			StartLine: start + 1,
			EndLine:   end,
			Hash:      h,
			Text:      body,
		})
		if end >= len(lines) {
			break
		}
	}
	return out
}
