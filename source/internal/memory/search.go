// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package memory

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

// SearchOpts configures memory_search.
type SearchOpts struct {
	Query string
	Tags  []string
	TopK  int
	Embed EmbedConfig // optional; empty → lexical only
}

// Hit is one ranked search result.
type Hit struct {
	Entry  Entry
	Score  float64
	Source string // lexical | vector | hybrid
}

// Search ranks live entries by lexical overlap, optionally blending embeddings.
func (s *Store) Search(ctx context.Context, opts SearchOpts) ([]Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.TrimSpace(opts.Query)
	if q == "" {
		return nil, nil
	}
	if opts.TopK <= 0 {
		opts.TopK = 8
	}
	if opts.TopK > 32 {
		opts.TopK = 32
	}
	entries, err := s.allLiveUnlocked()
	if err != nil {
		return nil, err
	}
	tagFilter := normalizeTags(opts.Tags)
	qTokens := tokenize(q)
	var hits []Hit
	for _, e := range entries {
		if len(tagFilter) > 0 && !tagsMatchAll(e.Tags, tagFilter) {
			continue
		}
		lex := lexicalScore(q, qTokens, e)
		if lex <= 0 {
			continue
		}
		hits = append(hits, Hit{Entry: e, Score: lex, Source: "lexical"})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Entry.UpdatedAt.After(hits[j].Entry.UpdatedAt)
		}
		return hits[i].Score > hits[j].Score
	})

	if opts.Embed.Configured() {
		vecHits, err := s.vectorRerank(ctx, opts.Embed, q, entries, tagFilter)
		if err == nil && len(vecHits) > 0 {
			hits = mergeHybrid(hits, vecHits)
		}
	}

	if len(hits) > opts.TopK {
		hits = hits[:opts.TopK]
	}
	return hits, nil
}

func (s *Store) vectorRerank(ctx context.Context, cfg EmbedConfig, query string, entries []Entry, tagFilter []string) ([]Hit, error) {
	client := newEmbedClient(cfg)
	vecs, err := client.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, err
	}
	qv := vecs[0]
	var hits []Hit
	for _, e := range entries {
		if len(tagFilter) > 0 && !tagsMatchAll(e.Tags, tagFilter) {
			continue
		}
		if len(e.Vector) == 0 {
			continue
		}
		sim := cosine(qv, e.Vector)
		if sim <= 0 {
			continue
		}
		hits = append(hits, Hit{Entry: e, Score: sim, Source: "vector"})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits, nil
}

func mergeHybrid(lex, vec []Hit) []Hit {
	byID := map[string]Hit{}
	for _, h := range lex {
		byID[h.Entry.ID] = h
	}
	for _, h := range vec {
		if cur, ok := byID[h.Entry.ID]; ok {
			cur.Score = cur.Score*0.5 + h.Score*0.5
			cur.Source = "hybrid"
			byID[h.Entry.ID] = cur
		} else {
			byID[h.Entry.ID] = h
		}
	}
	out := make([]Hit, 0, len(byID))
	for _, h := range byID {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func lexicalScore(q string, qTokens []string, e Entry) float64 {
	text := strings.ToLower(e.Text)
	key := strings.ToLower(e.Key)
	ql := strings.ToLower(q)
	score := 0.0
	if key != "" && (key == ql || strings.Contains(key, ql)) {
		score += 5
	}
	if strings.Contains(text, ql) {
		score += 3
	}
	eTokens := tokenize(e.Text + " " + e.Key + " " + strings.Join(e.Tags, " "))
	if len(qTokens) == 0 {
		return score
	}
	set := map[string]bool{}
	for _, t := range eTokens {
		set[t] = true
	}
	overlap := 0
	for _, t := range qTokens {
		if set[t] {
			overlap++
		}
	}
	score += float64(overlap) / float64(len(qTokens))
	return score
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var b strings.Builder
	var out []string
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tok := b.String()
		b.Reset()
		if len(tok) >= 2 {
			out = append(out, tok)
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func tagsMatchAll(have, want []string) bool {
	for _, w := range want {
		if !hasTag(have, w) {
			return false
		}
	}
	return true
}

func cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
