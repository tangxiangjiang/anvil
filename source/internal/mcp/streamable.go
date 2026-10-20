// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// AuthRequiredError is a 401 from a remote server: it needs a sign-in, not a
// different configuration. Challenge is the raw WWW-Authenticate value, kept so
// the OAuth discovery can follow resource_metadata; it is never printed.
type AuthRequiredError struct {
	Server              string
	Challenge           string
	ResourceMetadataURL string
}

func (e *AuthRequiredError) Error() string {
	if e.ResourceMetadataURL != "" {
		return fmt.Sprintf("mcp %s: authorization required (metadata: %s)", e.Server, redactURL(e.ResourceMetadataURL))
	}
	return fmt.Sprintf("mcp %s: authorization required", e.Server)
}

// SessionExpiredError is a 404 on a request that carried a session id: the
// server forgot the session, and the answer is a fresh initialize, not a retry
// of the same call.
type SessionExpiredError struct {
	Server string
}

func (e *SessionExpiredError) Error() string {
	return fmt.Sprintf("mcp %s: the server no longer knows this session", e.Server)
}

// httpStatusError is a non-2xx answer from a remote server.
type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("mcp http: status %d: %s", e.status, truncate(e.body, 512))
}

// rpcError is a JSON-RPC error object returned by a server. It exists so the
// protocol-version retry can recognise a rejection instead of matching strings.
type rpcError struct {
	method  string
	code    int
	message string
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp %s: %s", e.method, e.message)
}

// rejectableVersion reports whether err looks like the server refusing the
// protocol version we opened with, as opposed to a network or auth failure.
func rejectableVersion(err error) bool {
	var rpcErr *rpcError
	if errors.As(err, &rpcErr) {
		lower := strings.ToLower(rpcErr.message)
		return strings.Contains(lower, "protocol") && strings.Contains(lower, "version") ||
			strings.Contains(lower, "unsupported version")
	}
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.status == 400
	}
	return false
}

// prepareStreamable sets what every request to a Streamable HTTP endpoint
// carries: the content type, both accepted response shapes, the session id once
// the server has issued one, and the negotiated protocol version after
// initialize.
func (s *Session) prepareStreamable(req *http.Request) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", s.sessionID)
	}
	if s.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", s.protocolVersion)
	}
	return s.applyHeaders(req)
}

// classify turns the status codes that carry meaning into typed errors and
// remembers the session id the server hands out. It is called before the body
// is read, because a 401 body is not a JSON-RPC answer.
func (s *Session) classify(resp *http.Response) error {
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		s.sessionID = id
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		challenge := resp.Header.Get("Www-Authenticate")
		_, metadataURL := parseWWWAuthenticate(challenge)
		if metadataURL == "" {
			metadataURL = resourceMetadataURL(s.baseURL)
		}
		return &AuthRequiredError{Server: s.name, Challenge: challenge, ResourceMetadataURL: metadataURL}
	case http.StatusNotFound:
		if s.sessionID != "" {
			return &SessionExpiredError{Server: s.name}
		}
	}
	return nil
}

// requestStreamable posts one JSON-RPC request and reads the answer from either
// a JSON body or an SSE stream. Servers that answer plain JSON keep working;
// that is what makes the upgrade backward compatible.
func (s *Session) requestStreamable(ctx context.Context, method string, payload []byte, id int64) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.msgURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if err := s.prepareStreamable(req); err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := s.classify(resp); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &httpStatusError{status: resp.StatusCode, body: string(body)}
	}
	var body json.RawMessage
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		body, err = parseSSEResponse(resp.Body, id)
	} else {
		body, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	}
	if err != nil {
		return nil, err
	}
	var respObj rpcResponse
	if err := json.Unmarshal(body, &respObj); err != nil {
		return nil, err
	}
	if respObj.Error != nil {
		return nil, &rpcError{method: method, code: respObj.Error.Code, message: respObj.Error.Message}
	}
	return respObj.Result, nil
}

// notifyStreamable posts a notification. The server acknowledges with 202 or
// 204 and has nothing to say; anything outside 2xx is a failure of the
// connection, not of a call.
func (s *Session) notifyStreamable(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.msgURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if err := s.prepareStreamable(req); err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err := s.classify(resp); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{status: resp.StatusCode, body: string(body)}
	}
	return nil
}

// parseSSEResponse reads an SSE body until it finds the JSON-RPC answer with
// the given id. Notifications (no id) and events for other ids are skipped; a
// server may interleave them.
func parseSSEResponse(r io.Reader, id int64) (json.RawMessage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var data []string
	flush := func() (json.RawMessage, bool) {
		payload := strings.Join(data, "\n")
		data = data[:0]
		if strings.TrimSpace(payload) == "" {
			return nil, false
		}
		var probe struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal([]byte(payload), &probe); err != nil {
			return nil, false
		}
		if len(probe.ID) == 0 || string(probe.ID) == "null" {
			return nil, false
		}
		var gotID int64
		if err := json.Unmarshal(probe.ID, &gotID); err != nil || gotID != id {
			return nil, false
		}
		return json.RawMessage(payload), true
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if msg, ok := flush(); ok {
				return msg, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		// event:, id: and retry: carry nothing we need: the JSON-RPC id in the
		// payload is what identifies the answer.
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if msg, ok := flush(); ok {
		return msg, nil
	}
	return nil, fmt.Errorf("sse stream ended without a reply to request %d", id)
}

// parseWWWAuthenticate pulls resource_metadata (and, for diagnostics, the
// realm) out of a Bearer challenge. Other schemes have nothing we can follow.
func parseWWWAuthenticate(header string) (realm, metadataURL string) {
	trimmed := strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(trimmed), "bearer") {
		return "", ""
	}
	params := parseAuthParams(strings.TrimSpace(trimmed[len("bearer"):]))
	return params["realm"], params["resource_metadata"]
}

// parseAuthParams parses `k="v", k2=v2` after a scheme keyword. Quoted values
// may contain commas and equals signs; unquoted ones end at a comma.
func parseAuthParams(s string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(s); {
		for i < len(s) && (s[i] == ' ' || s[i] == ',' || s[i] == '\t') {
			i++
		}
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ',' {
			i++
		}
		key := strings.ToLower(strings.TrimSpace(s[start:i]))
		if i >= len(s) || s[i] != '=' {
			continue
		}
		i++ // '='
		var value string
		if i < len(s) && s[i] == '"' {
			i++
			var b strings.Builder
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			value = b.String()
			i++ // closing quote
		} else {
			start := i
			for i < len(s) && s[i] != ',' {
				i++
			}
			value = strings.TrimSpace(s[start:i])
		}
		if key != "" {
			out[key] = value
		}
	}
	return out
}

// resourceMetadataURL derives the RFC 9728 well-known location for a resource:
// the path is appended to the well-known prefix on the same host.
func resourceMetadataURL(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	path := strings.TrimRight(u.Path, "/")
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + path
}

// authorizationServerMetadataURLs lists the discovery documents to try, in
// order: RFC 8414 with the issuer path inserted, then OpenID configuration.
func authorizationServerMetadataURLs(issuer string) []string {
	issuer = strings.TrimRight(issuer, "/")
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	base := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.Path, "/")
	return []string{
		base + "/.well-known/oauth-authorization-server" + path,
		issuer + "/.well-known/openid-configuration",
	}
}

// redactURL removes the query and fragment. Authorization URLs carry state and
// PKCE challenges in the query; none of that belongs in a message.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
