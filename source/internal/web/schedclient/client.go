// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package schedclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to anvil-scheduler HTTP API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) do(ctx context.Context, method, path string, body any, dest any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("scheduler %s %s: HTTP %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(data)))
	}
	if dest == nil {
		return nil
	}
	return json.Unmarshal(data, dest)
}

// NodesResponse is GET /v1/nodes.
type NodesResponse struct {
	Nodes []json.RawMessage `json:"nodes"`
}

// ListNodes fetches nodes.
func (c *Client) ListNodes(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/v1/nodes", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// PatchNode patches a node.
func (c *Client) PatchNode(ctx context.Context, id string, body map[string]any) error {
	return c.do(ctx, http.MethodPatch, "/v1/nodes/"+id, body, nil)
}

// Usage fetches usage aggregates.
func (c *Client) Usage(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/v1/usage", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
