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
	"net/url"
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
	return &http.Client{Timeout: 3 * time.Minute}
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

// Meta is GET /v1/meta.
type Meta struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	ListenApp     string `json:"listen_app"`
	ListenCluster string `json:"listen_cluster"`
}

// GetMeta fetches scheduler identity.
func (c *Client) GetMeta(ctx context.Context) (Meta, error) {
	var m Meta
	if err := c.do(ctx, http.MethodGet, "/v1/meta", nil, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
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

// AddWorker creates a project worker on a node.
func (c *Client) AddWorker(ctx context.Context, nodeID string, body map[string]any) (json.RawMessage, error) {
	var raw json.RawMessage
	path := "/v1/nodes/" + nodeID + "/workers"
	if err := c.do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// RemoveWorker deletes a project worker.
func (c *Client) RemoveWorker(ctx context.Context, nodeID, workerID string) (json.RawMessage, error) {
	var raw json.RawMessage
	path := "/v1/nodes/" + nodeID + "/workers/" + workerID
	if err := c.do(ctx, http.MethodDelete, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// StartWorker enables a stopped project worker.
func (c *Client) StartWorker(ctx context.Context, nodeID, workerID string) (json.RawMessage, error) {
	var raw json.RawMessage
	path := "/v1/nodes/" + nodeID + "/workers/" + workerID + "/start"
	if err := c.do(ctx, http.MethodPost, path, map[string]any{}, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ShutdownWorker stops an idle project worker.
func (c *Client) ShutdownWorker(ctx context.Context, nodeID, workerID string) (json.RawMessage, error) {
	var raw json.RawMessage
	path := "/v1/nodes/" + nodeID + "/workers/" + workerID + "/shutdown"
	if err := c.do(ctx, http.MethodPost, path, map[string]any{}, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ListSandbox lists directories under a node's sandbox.
func (c *Client) ListSandbox(ctx context.Context, nodeID, relPath string) (json.RawMessage, error) {
	var raw json.RawMessage
	path := "/v1/nodes/" + nodeID + "/sandbox"
	if relPath != "" {
		path += "?path=" + url.QueryEscape(relPath)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// MkdirSandbox creates a directory under a node's sandbox.
func (c *Client) MkdirSandbox(ctx context.Context, nodeID string, body map[string]any) (json.RawMessage, error) {
	var raw json.RawMessage
	path := "/v1/nodes/" + nodeID + "/sandbox/mkdir"
	if err := c.do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Usage fetches usage aggregates.
func (c *Client) Usage(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/v1/usage", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// RegisterWorkspace registers a workspace token on the scheduler for Dev Node join.
func (c *Client) RegisterWorkspace(ctx context.Context, id, token, name string) error {
	return c.do(ctx, http.MethodPost, "/v1/workspaces", map[string]any{
		"id": id, "token": token, "name": name,
	}, nil)
}
