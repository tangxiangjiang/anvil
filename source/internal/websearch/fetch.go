// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package websearch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/redact"
)

const (
	defaultFetchMaxBytes = 64 << 10
	hardFetchMaxBytes    = 512 << 10
	defaultFetchTimeout  = 15 * time.Second
	maxFetchTextRunes    = 24000
)

// FetchOpts controls fetch_url.
type FetchOpts struct {
	MaxBytes         int
	AllowPrivateURLs bool
	Timeout          time.Duration
	HTTPClient       *http.Client
}

// FetchURL GETs http(s) URL and returns readable text (best-effort HTML strip).
func FetchURL(ctx context.Context, rawURL string, opts FetchOpts) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("only http/https URLs are allowed")
	}
	if u.Host == "" {
		return "", fmt.Errorf("url host is required")
	}

	host := u.Hostname()
	if !opts.AllowPrivateURLs {
		if err := denyPrivateHost(ctx, host); err != nil {
			return "", err
		}
	}

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultFetchMaxBytes
	}
	if maxBytes > hardFetchMaxBytes {
		maxBytes = hardFetchMaxBytes
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				if !opts.AllowPrivateURLs {
					h := req.URL.Hostname()
					if err := denyPrivateHost(req.Context(), h); err != nil {
						return err
					}
				}
				return nil
			},
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "anvil-fetch_url/0.15")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch HTTP %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "octet-stream") || strings.HasPrefix(ct, "image/") ||
		strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "video/") ||
		strings.Contains(ct, "application/pdf") || strings.Contains(ct, "application/zip") {
		return "", fmt.Errorf("unsupported content-type %q", ct)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return "", err
	}
	truncated := len(raw) > maxBytes
	if truncated {
		raw = raw[:maxBytes]
	}
	text := string(raw)
	if strings.Contains(ct, "html") || looksLikeHTML(text) {
		text = stripHTML(text)
	}
	text = redact.String(strings.TrimSpace(text))
	if utf8.RuneCountInString(text) > maxFetchTextRunes {
		text = string([]rune(text)[:maxFetchTextRunes]) + "…"
	}
	if truncated {
		text += "\n…[truncated at max_bytes]"
	}
	if text == "" {
		return "(empty body)", nil
	}
	return text, nil
}

func looksLikeHTML(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "<html") || strings.Contains(low, "<body") || strings.Contains(low, "<!doctype")
}

func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteByte(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.ReplaceAll(out, "\u00a0", " ")
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

func denyPrivateHost(ctx context.Context, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty host")
	}
	// Literal IP?
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("private or link-local URL blocked (SSRF guard)")
		}
		return nil
	}
	// Block obvious local names without DNS.
	low := strings.ToLower(host)
	if low == "localhost" || strings.HasSuffix(low, ".localhost") || low == "metadata.google.internal" {
		return fmt.Errorf("private or link-local URL blocked (SSRF guard)")
	}

	resolver := net.DefaultResolver
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("dns lookup failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("dns lookup returned no addresses")
	}
	for _, ipa := range ips {
		if isPrivateIP(ipa.IP) {
			return fmt.Errorf("private or link-local URL blocked (SSRF guard)")
		}
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// Extra cloud metadata ranges commonly used in SSRF.
	if ip4 := ip.To4(); ip4 != nil {
		// 169.254.0.0/16 already link-local; 100.64.0.0/10 CGNAT — treat as non-public for fetch.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return false
}
