// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

type embedJob struct {
	UID  string
	Text string
}

type embedClient struct {
	url    string
	apiKey string
	model  string
	http   *http.Client
}

func newEmbedClient(opts BuildOptions) *embedClient {
	url := strings.TrimRight(opts.EmbedURL, "/")
	if !strings.HasSuffix(url, "/embeddings") {
		url = url + "/embeddings"
	}
	return &embedClient{
		url:    url,
		apiKey: opts.EmbedAPIKey,
		model:  opts.EmbedModel,
		http:   &http.Client{Timeout: 60 * time.Second},
	}
}

type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResp struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *embedClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(embedReq{Model: c.model, Input: texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, truncateErr(string(raw)))
	}
	var out embedResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("embeddings: %s", out.Error.Message)
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			continue
		}
		v := make([]float32, len(d.Embedding))
		for i, f := range d.Embedding {
			v[i] = float32(f)
		}
		vecs[d.Index] = v
	}
	for i, v := range vecs {
		if v == nil {
			return nil, fmt.Errorf("embeddings: missing vector for input %d", i)
		}
	}
	return vecs, nil
}

func (d *DB) embedChunks(ctx context.Context, opts BuildOptions, jobs []embedJob) (int, error) {
	client := newEmbedClient(opts)
	const batch = 16
	n := 0
	for i := 0; i < len(jobs); i += batch {
		select {
		case <-ctx.Done():
			return n, ctx.Err()
		default:
		}
		end := i + batch
		if end > len(jobs) {
			end = len(jobs)
		}
		part := jobs[i:end]
		texts := make([]string, len(part))
		for j, jn := range part {
			texts[j] = jn.Text
		}
		vecs, err := client.Embed(ctx, texts)
		if err != nil {
			return n, err
		}
		for j, v := range vecs {
			var chunkID int64
			err := d.db.QueryRow(`SELECT id FROM chunks WHERE uid=?`, part[j].UID).Scan(&chunkID)
			if err != nil {
				continue
			}
			blob := floatsToBlob(v)
			if _, err := d.db.Exec(`INSERT OR REPLACE INTO chunk_vec(chunk_id, embedding) VALUES(?,?)`, chunkID, blob); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func (d *DB) vectorSearch(ctx context.Context, text, pathGlob string, topK int, queryVec []float32) ([]Hit, error) {
	_ = ctx
	_ = text
	if queryVec == nil {
		return nil, fmt.Errorf("vector search requires query embedding")
	}
	rows, err := d.db.Query(`
		SELECT c.id, c.uid, c.path, c.start_line, c.end_line, c.text, v.embedding
		FROM chunk_vec v
		JOIN chunks c ON c.id = v.chunk_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		hit   Hit
		score float64
	}
	var all []scored
	for rows.Next() {
		var id int64
		var h Hit
		var blob []byte
		if err := rows.Scan(&id, &h.UID, &h.Path, &h.StartLine, &h.EndLine, &h.Excerpt, &blob); err != nil {
			return nil, err
		}
		if pathGlob != "" && !matchGlob(pathGlob, h.Path) {
			continue
		}
		vec := blobToFloats(blob)
		sim := cosine(queryVec, vec)
		h.Score = sim
		h.Source = "vector"
		h.Excerpt = trimExcerpt(h.Excerpt, 500)
		all = append(all, scored{hit: h, score: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score == all[j].score {
			return all[i].hit.UID < all[j].hit.UID
		}
		return all[i].score > all[j].score
	})
	if len(all) > topK {
		all = all[:topK]
	}
	out := make([]Hit, len(all))
	for i, s := range all {
		out[i] = s.hit
	}
	return out, nil
}

// SearchWithEmbed runs search; for vector/hybrid modes it embeds the query via opts.
func SearchWithEmbed(ctx context.Context, cwd string, q Query, opts BuildOptions) ([]Hit, error) {
	db, err := Open(cwd)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	topK := q.TopK
	if topK <= 0 {
		topK = defaultTopK
	}
	if topK > maxTopK {
		topK = maxTopK
	}
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return nil, fmt.Errorf("query is required")
	}

	mode := strings.ToLower(strings.TrimSpace(q.Mode))
	if mode == "" || mode == "auto" {
		var n int
		_ = db.db.QueryRow(`SELECT COUNT(*) FROM chunk_vec`).Scan(&n)
		if n > 0 && opts.EmbedURL != "" && opts.EmbedModel != "" {
			mode = "hybrid"
		} else {
			mode = "lexical"
		}
	}

	if mode == "lexical" {
		return db.lexicalSearch(text, q.PathGlob, topK)
	}

	if opts.EmbedURL == "" || opts.EmbedModel == "" {
		return db.lexicalSearch(text, q.PathGlob, topK)
	}
	client := newEmbedClient(opts)
	vecs, err := client.Embed(ctx, []string{text})
	if err != nil {
		return db.lexicalSearch(text, q.PathGlob, topK)
	}
	qvec := vecs[0]

	if mode == "vector" {
		return db.vectorSearch(ctx, text, q.PathGlob, topK, qvec)
	}
	// hybrid
	lex, err := db.lexicalSearch(text, q.PathGlob, topK*2)
	if err != nil {
		return nil, err
	}
	vec, err := db.vectorSearch(ctx, text, q.PathGlob, topK*2, qvec)
	if err != nil {
		return lex, nil
	}
	return mergeRRF(lex, vec, topK), nil
}

func floatsToBlob(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func blobToFloats(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
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
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func truncateErr(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
