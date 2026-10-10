// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/redact"
)

// BuildOptions controls index construction.
type BuildOptions struct {
	// EmbedURL is an OpenAI-compatible API root (…/v1) or full …/embeddings URL.
	// Empty disables embedding (FTS-only).
	EmbedURL string
	// EmbedAPIKey is sent as Bearer when non-empty.
	EmbedAPIKey string
	// EmbedModel is the embedding model id (required when EmbedURL is set).
	EmbedModel string
}

// BuildResult summarizes an index build.
type BuildResult struct {
	FilesSeen    int
	FilesIndexed int
	FilesSkipped int
	FilesRemoved int
	Chunks       int
	Embedded     int
	DBPath       string
}

// Build walks cwd and incrementally updates search.db.
func Build(ctx context.Context, cwd string, opts BuildOptions) (*BuildResult, error) {
	db, err := Open(cwd)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	root := db.cwd
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	rootResolved = filepath.Clean(rootResolved)

	seen := map[string]bool{}
	res := &BuildResult{DBPath: db.Path()}
	var pendingEmbed []embedJob

	wantEmbed := opts.EmbedURL != "" && opts.EmbedModel != ""
	forceReembed := false
	if wantEmbed {
		prevModel := db.getMeta("embed_model")
		// Treat empty prev as "needs embed" so a failed first/re-embed can retry
		// without leaving the index stuck (unchanged files would otherwise skip).
		if prevModel != opts.EmbedModel {
			if prevModel != "" {
				// Drop incompatible vectors; keep prevModel in meta until embed
				// succeeds so a failed switch still forces re-embed next time.
				if _, err := db.db.Exec(`DELETE FROM chunk_vec`); err != nil {
					return nil, fmt.Errorf("clear vectors on embed model change: %w", err)
				}
			}
			forceReembed = true
		}
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && skipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		res.FilesSeen++

		info, err := d.Info()
		if err != nil || skipFile(rel, info.Size()) {
			res.FilesSkipped++
			return nil
		}
		// Symlink / escape check: resolved path must stay under cwd.
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		resolved = filepath.Clean(resolved)
		relOut, err := filepath.Rel(rootResolved, resolved)
		if err != nil || relOut == ".." || strings.HasPrefix(relOut, ".."+string(filepath.Separator)) {
			res.FilesSkipped++
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			res.FilesSkipped++
			return nil
		}
		fileHash := hashBytes(data)
		seen[rel] = true

		var oldHash string
		_ = db.db.QueryRow(`SELECT hash FROM files WHERE path=?`, rel).Scan(&oldHash)
		unchanged := oldHash == fileHash
		if unchanged && !forceReembed {
			return nil
		}

		chunks := chunkFile(rel, data)
		for i := range chunks {
			chunks[i].Text = redact.String(chunks[i].Text)
		}
		if !unchanged {
			if err := db.replaceFile(rel, info.ModTime().Unix(), fileHash, chunks); err != nil {
				return fmt.Errorf("index %s: %w", rel, err)
			}
			res.FilesIndexed++
			res.Chunks += len(chunks)
		} else {
			// Model change: keep chunks, re-queue embeddings only.
			uids, texts, err := db.chunkTextsForPath(rel)
			if err != nil {
				return err
			}
			chunks = chunks[:0]
			for i := range uids {
				chunks = append(chunks, Chunk{UID: uids[i], Text: texts[i]})
			}
		}
		if wantEmbed {
			for _, c := range chunks {
				pendingEmbed = append(pendingEmbed, embedJob{UID: c.UID, Text: c.Text})
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	removed, err := db.pruneMissing(seen)
	if err != nil {
		return res, err
	}
	res.FilesRemoved = removed

	if opts.EmbedURL != "" && opts.EmbedModel != "" && len(pendingEmbed) > 0 {
		n, err := db.embedChunks(ctx, opts, pendingEmbed)
		if err != nil {
			// Graceful fallback: keep FTS index; report embed error lightly.
			_ = db.setMeta("embed_error", err.Error())
		} else {
			res.Embedded = n
			_ = db.setMeta("embed_model", opts.EmbedModel)
			_ = db.setMeta("embed_url", opts.EmbedURL)
			_ = db.setMeta("embed_error", "")
		}
	}

	_ = db.setMeta("built_at", time.Now().UTC().Format(time.RFC3339))
	_ = db.setMeta("cwd", root)
	return res, nil
}

// Status describes the on-disk index.
type Status struct {
	DBPath        string `json:"db_path"`
	Exists        bool   `json:"exists"`
	SchemaVersion string `json:"schema_version,omitempty"`
	BuiltAt       string `json:"built_at,omitempty"`
	EmbedModel    string `json:"embed_model,omitempty"`
	EmbedURL      string `json:"embed_url,omitempty"`
	EmbedError    string `json:"embed_error,omitempty"`
	Files         int    `json:"files"`
	Chunks        int    `json:"chunks"`
	Vectors       int    `json:"vectors"`
}

// Stat returns index status for cwd (opens DB if present).
func Stat(cwd string) (Status, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return Status{}, err
	}
	path := filepath.Join(abs, ".anvil", "index", "search.db")
	st := Status{DBPath: path}
	if _, err := os.Stat(path); err != nil {
		return st, nil
	}
	st.Exists = true
	db, err := Open(abs)
	if err != nil {
		return st, err
	}
	defer db.Close()
	st.SchemaVersion = db.getMeta("schema_version")
	st.BuiltAt = db.getMeta("built_at")
	st.EmbedModel = db.getMeta("embed_model")
	st.EmbedURL = db.getMeta("embed_url")
	st.EmbedError = db.getMeta("embed_error")
	_ = db.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&st.Files)
	_ = db.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&st.Chunks)
	_ = db.db.QueryRow(`SELECT COUNT(*) FROM chunk_vec`).Scan(&st.Vectors)
	return st, nil
}

// Clear removes the index directory for cwd.
func Clear(cwd string) error {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(abs, ".anvil", "index"))
}

// Exists reports whether search.db is present.
func Exists(cwd string) bool {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(abs, ".anvil", "index", "search.db"))
	return err == nil
}

func (d *DB) replaceFile(rel string, mtime int64, hash string, chunks []Chunk) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Remove old FTS rows by uid list.
	rows, err := tx.Query(`SELECT uid FROM chunks WHERE path=?`, rel)
	if err != nil {
		return err
	}
	var uids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return err
		}
		uids = append(uids, uid)
	}
	rows.Close()
	for _, uid := range uids {
		if _, err := tx.Exec(`DELETE FROM chunks_fts WHERE uid=?`, uid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM chunks WHERE path=?`, rel); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO files(path,mtime,hash,lang) VALUES(?,?,?,?)`,
		rel, mtime, hash, langOf(rel)); err != nil {
		return err
	}
	for _, c := range chunks {
		res, err := tx.Exec(
			`INSERT INTO chunks(uid,path,start_line,end_line,hash,text) VALUES(?,?,?,?,?,?)`,
			c.UID, c.Path, c.StartLine, c.EndLine, c.Hash, c.Text,
		)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(
			`INSERT INTO chunks_fts(uid,path,start_line,end_line,text) VALUES(?,?,?,?,?)`,
			c.UID, c.Path, c.StartLine, c.EndLine, c.Text,
		); err != nil {
			return err
		}
		_ = id
	}
	return tx.Commit()
}

func (d *DB) pruneMissing(seen map[string]bool) (int, error) {
	rows, err := d.db.Query(`SELECT path FROM files`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var gone []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return 0, err
		}
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	for _, p := range gone {
		uids, _ := d.uidsForPath(p)
		for _, uid := range uids {
			_, _ = d.db.Exec(`DELETE FROM chunks_fts WHERE uid=?`, uid)
		}
		_, _ = d.db.Exec(`DELETE FROM chunks WHERE path=?`, p)
		_, _ = d.db.Exec(`DELETE FROM files WHERE path=?`, p)
	}
	return len(gone), nil
}

func (d *DB) uidsForPath(path string) ([]string, error) {
	rows, err := d.db.Query(`SELECT uid FROM chunks WHERE path=?`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, nil
}

func (d *DB) chunkTextsForPath(path string) (uids []string, texts []string, err error) {
	rows, err := d.db.Query(`SELECT uid, text FROM chunks WHERE path=?`, path)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, text string
		if err := rows.Scan(&uid, &text); err != nil {
			return nil, nil, err
		}
		uids = append(uids, uid)
		texts = append(texts, text)
	}
	return uids, texts, rows.Err()
}

func langOf(rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	if ext == "" {
		return ""
	}
	return strings.TrimPrefix(ext, ".")
}
