// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TokenRecord is what one server's sign-in leaves behind. It lives in a file
// only this user can read; nothing here is ever copied into a status, an error
// or a protocol message.
type TokenRecord struct {
	Server         string    `json:"server"`
	URL            string    `json:"url"`
	Issuer         string    `json:"issuer,omitempty"`
	Resource       string    `json:"resource,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	ClientSecret   string    `json:"client_secret,omitempty"`
	Scope          string    `json:"scope,omitempty"`
	TokenEndpoint  string    `json:"token_endpoint,omitempty"`
	TokenType      string    `json:"token_type,omitempty"`
	AccessToken    string    `json:"access_token,omitempty"`
	RefreshToken   string    `json:"refresh_token,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	RegisteredWith string    `json:"registered_redirect_uri,omitempty"`
}

// normalizeURL is what "the same resource" means: scheme, host and path with a
// trailing slash removed. The token filename and the RFC 8707 resource value
// both use it, so a decoration cannot produce two token files for one server.
func normalizeURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func tokenFileName(server, serverURL string) string {
	sum := sha256.Sum256([]byte(server + "\n" + normalizeURL(serverURL)))
	return hex.EncodeToString(sum[:])[:32] + ".json"
}

func tokenPath(dir, server, serverURL string) string {
	return filepath.Join(dir, tokenFileName(server, serverURL))
}

// loadTokenRecord returns nil (and no error) when there is no file: "not signed
// in yet" is not a failure.
func loadTokenRecord(dir, server, serverURL string) (*TokenRecord, error) {
	body, err := os.ReadFile(tokenPath(dir, server, serverURL))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read mcp token file: %w", err)
	}
	var rec TokenRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		// A corrupt file is unusable, but its name is a hash, not a secret.
		return nil, fmt.Errorf("parse mcp token file: %w", err)
	}
	return &rec, nil
}

// saveTokenRecord writes through a temp file and a rename with mode 0600. On
// Windows the mode is best effort; the file stays under the user profile.
func saveTokenRecord(dir string, rec *TokenRecord) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create mcp token directory: %w", err)
	}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	path := tokenPath(dir, rec.Server, rec.URL)
	temp, err := os.CreateTemp(dir, ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("create mcp token file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set mcp token mode: %w", err)
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write mcp token file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close mcp token file: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace mcp token file: %w", err)
	}
	return nil
}

func deleteTokenRecord(dir, server, serverURL string) error {
	err := os.Remove(tokenPath(dir, server, serverURL))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete mcp token file: %w", err)
	}
	return nil
}
