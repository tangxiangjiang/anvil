// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/index"
)

func writeTree(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"pkg/alpha.go": "package pkg\n\nfunc AlphaWidget() int {\n\treturn 42\n}\n",
		"pkg/beta.go":  "package pkg\n\nfunc BetaHelper() string {\n\treturn \"beta\"\n}\n",
		"README.md":    "# Demo\n\nAlphaWidget is the entry.\n",
		".env":         "SECRET=do-not-index\n",
		"big.bin":      strings.Repeat("x", 600*1024),
	}
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChunkAndIncrementalBuild(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir)

	r1, err := index.Build(context.Background(), dir, index.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r1.FilesIndexed < 2 || r1.Chunks < 2 {
		t.Fatalf("unexpected build %+v", r1)
	}

	r2, err := index.Build(context.Background(), dir, index.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r2.FilesIndexed != 0 {
		t.Fatalf("second build should skip unchanged files, got indexed=%d", r2.FilesIndexed)
	}

	st, err := index.Stat(dir)
	if err != nil || !st.Exists || st.Chunks < 2 {
		t.Fatalf("status %+v err=%v", st, err)
	}
}

func TestSkipEnvAndSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Symlink creation may require privileges; still check .env skip.
	}
	dir := t.TempDir()
	writeTree(t, dir)

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("func OutsideLeak() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "escape.go")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("symlink not available:", err)
	}

	_, err := index.Build(context.Background(), dir, index.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := index.Search(context.Background(), dir, index.Query{Text: "OutsideLeak", TopK: 8})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if strings.Contains(h.Excerpt, "OutsideLeak") || strings.Contains(h.Path, "escape") {
			t.Fatalf("escaped symlink content indexed: %+v", h)
		}
	}
	// .env content must not appear
	hits, err = index.Search(context.Background(), dir, index.Query{Text: "SECRET", TopK: 8})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if strings.Contains(h.Excerpt, "do-not-index") {
			t.Fatalf(".env indexed: %+v", h)
		}
	}
}

func TestLexicalSemanticSearch(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir)
	if _, err := index.Build(context.Background(), dir, index.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	hits, err := index.Search(context.Background(), dir, index.Query{Text: "AlphaWidget", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	found := false
	for _, h := range hits {
		if strings.Contains(h.Path, "alpha.go") || strings.Contains(h.Excerpt, "AlphaWidget") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("AlphaWidget not in top hits: %+v", hits)
	}
}

func TestHybridWithFakeEmbed(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir)

	// Deterministic fake embeddings: bag-of-chars hashed into fixed dims.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type item struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		}
		var data []item
		for i, s := range req.Input {
			data = append(data, item{Index: i, Embedding: fakeVec(s)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	res, err := index.Build(context.Background(), dir, index.BuildOptions{
		EmbedURL:    srv.URL + "/v1",
		EmbedModel:  "fake-embed",
		EmbedAPIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Embedded < 1 {
		t.Fatalf("expected embeddings, got %+v", res)
	}

	hits, err := index.SearchWithEmbed(context.Background(), dir, index.Query{
		Text: "AlphaWidget",
		TopK: 5,
		Mode: "hybrid",
	}, index.BuildOptions{
		EmbedURL:   srv.URL + "/v1",
		EmbedModel: "fake-embed",
		EmbedAPIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !(strings.Contains(hits[0].Path, "alpha") || strings.Contains(hits[0].Excerpt, "AlphaWidget")) {
		t.Fatalf("expected alpha top hit, got %+v", hits)
	}

	// Without embed URL, lexical still works.
	lex, err := index.Search(context.Background(), dir, index.Query{Text: "BetaHelper", TopK: 3})
	if err != nil || len(lex) == 0 {
		t.Fatalf("lexical fallback failed: %v %+v", err, lex)
	}
}

func fakeVec(s string) []float64 {
	v := make([]float64, 8)
	for _, r := range s {
		v[int(r)%8] += 1
	}
	return v
}

func TestEmptyIndexSearch(t *testing.T) {
	dir := t.TempDir()
	if _, err := index.Build(context.Background(), dir, index.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	hits, err := index.Search(context.Background(), dir, index.Query{Text: "nothinghere", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected empty hits, got %+v", hits)
	}
}

func TestIncrementalReembedOnlyChangedFile(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir)
	var embedCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		embedCalls += len(req.Input)
		type item struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		}
		var data []item
		for i, s := range req.Input {
			data = append(data, item{Index: i, Embedding: fakeVec(s)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	opts := index.BuildOptions{EmbedURL: srv.URL + "/v1", EmbedModel: "fake", EmbedAPIKey: "k"}
	r1, err := index.Build(context.Background(), dir, opts)
	if err != nil || r1.Embedded < 1 {
		t.Fatalf("%v %+v", err, r1)
	}
	firstEmbeds := embedCalls
	embedCalls = 0

	// Touch only alpha.go
	path := filepath.Join(dir, "pkg", "alpha.go")
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(body, []byte("\n// touched\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	r2, err := index.Build(context.Background(), dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if r2.FilesIndexed != 1 {
		t.Fatalf("expected 1 file reindexed, got %+v", r2)
	}
	if embedCalls == 0 || embedCalls >= firstEmbeds {
		t.Fatalf("expected fewer embeds than full rebuild: first=%d second=%d", firstEmbeds, embedCalls)
	}
}

func TestEmbedModelChangeReembeds(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir)
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		models = append(models, req.Model)
		type item struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		}
		var data []item
		for i, s := range req.Input {
			data = append(data, item{Index: i, Embedding: fakeVec(s + req.Model)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	_, err := index.Build(context.Background(), dir, index.BuildOptions{
		EmbedURL: srv.URL + "/v1", EmbedModel: "m1", EmbedAPIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := index.Build(context.Background(), dir, index.BuildOptions{
		EmbedURL: srv.URL + "/v1", EmbedModel: "m2", EmbedAPIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Embedded < 1 {
		t.Fatalf("expected re-embed on model change, got %+v", r2)
	}
	st, _ := index.Stat(dir)
	if st.EmbedModel != "m2" {
		t.Fatalf("embed_model=%q", st.EmbedModel)
	}
	eng := index.NewEngine(dir)
	eng.Embed = index.BuildOptions{EmbedURL: srv.URL + "/v1", EmbedModel: "m2", EmbedAPIKey: "k"}
	hits, err := eng.Search(context.Background(), index.Query{Text: "AlphaWidget", Mode: "hybrid", TopK: 3})
	if err != nil || len(hits) == 0 {
		t.Fatalf("%v %+v", err, hits)
	}
}

func TestChunkerUnit(t *testing.T) {
	// Build via public API on a long file to ensure multiple chunks.
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line number "+string(rune('A'+i%26)))
	}
	// use decimal instead
	lines = nil
	for i := 0; i < 100; i++ {
		lines = append(lines, "func LineItem() {} // "+strings.Repeat("x", i%5))
	}
	if err := os.WriteFile(filepath.Join(dir, "long.go"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := index.Build(context.Background(), dir, index.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Chunks < 2 {
		t.Fatalf("expected multiple chunks, got %d", r.Chunks)
	}
}
