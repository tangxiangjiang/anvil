// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	defaultTopK = 8
	maxTopK     = 32
)

// Query is a retrieval request.
type Query struct {
	Text     string
	PathGlob string // optional filepath.Match against slash paths
	TopK     int
	// Mode: "" / "auto" = hybrid when vectors exist; "lexical"; "vector"; "hybrid".
	Mode string
}

// Hit is one ranked chunk.
type Hit struct {
	UID       string  `json:"uid"`
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Excerpt   string  `json:"excerpt"`
	Source    string  `json:"source"` // lexical | vector | hybrid
}

// Search runs lexical retrieval (FTS5). For hybrid/vector use SearchWithEmbed.
func Search(ctx context.Context, cwd string, q Query) ([]Hit, error) {
	_ = ctx
	q.Mode = "lexical"
	return SearchWithEmbed(ctx, cwd, q, BuildOptions{})
}

func (d *DB) lexicalSearch(text, pathGlob string, topK int) ([]Hit, error) {
	fts := toFTSQuery(text)
	if fts == "" {
		return nil, nil
	}
	rows, err := d.db.Query(`
		SELECT c.uid, c.path, c.start_line, c.end_line, c.text, bm25(chunks_fts) AS rank
		FROM chunks_fts
		JOIN chunks c ON c.uid = chunks_fts.uid
		WHERE chunks_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, fts, topK*4)
	if err != nil {
		return nil, fmt.Errorf("fts: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		var rank float64
		if err := rows.Scan(&h.UID, &h.Path, &h.StartLine, &h.EndLine, &h.Excerpt, &rank); err != nil {
			return nil, err
		}
		if pathGlob != "" && !matchGlob(pathGlob, h.Path) {
			continue
		}
		// bm25: lower is better in SQLite; convert to positive score.
		h.Score = 1.0 / (1.0 + math.Max(0, rank))
		h.Source = "lexical"
		h.Excerpt = trimExcerpt(h.Excerpt, 500)
		hits = append(hits, h)
		if len(hits) >= topK {
			break
		}
	}
	return hits, rows.Err()
}

func mergeRRF(a, b []Hit, topK int) []Hit {
	const k = 60.0
	type agg struct {
		hit   Hit
		score float64
	}
	m := map[string]*agg{}
	add := func(list []Hit, source string) {
		for i, h := range list {
			e, ok := m[h.UID]
			if !ok {
				cp := h
				cp.Source = source
				e = &agg{hit: cp}
				m[h.UID] = e
			}
			e.score += 1.0 / (k + float64(i+1))
			if e.hit.Source != source && source != "" {
				e.hit.Source = "hybrid"
			}
		}
	}
	add(a, "lexical")
	add(b, "vector")
	out := make([]Hit, 0, len(m))
	for _, e := range m {
		h := e.hit
		h.Score = e.score
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].UID < out[j].UID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

func toFTSQuery(q string) string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if t == "" {
			return
		}
		t = strings.ReplaceAll(t, `"`, "")
		if t == "" {
			return
		}
		toks = append(toks, `"`+t+`"`)
	}
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	if len(toks) == 0 {
		return ""
	}
	return strings.Join(toks, " OR ")
}

func matchGlob(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)
	ok, err := filepath.Match(pattern, path)
	if err == nil && ok {
		return true
	}
	// Also try matching base name when pattern has no slash.
	if !strings.Contains(pattern, "/") {
		ok, _ = filepath.Match(pattern, filepath.Base(path))
		return ok
	}
	// Prefix ** / style: treat as suffix match of last segment pattern.
	if strings.HasPrefix(pattern, "**/") {
		suf := strings.TrimPrefix(pattern, "**/")
		ok, _ = filepath.Match(suf, path)
		if ok {
			return true
		}
		ok, _ = filepath.Match(suf, filepath.Base(path))
		return ok
	}
	return false
}

func trimExcerpt(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
