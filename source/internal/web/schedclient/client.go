// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package schedclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
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

// JobCreate is POST /v1/jobs body (app-facing).
type JobCreate struct {
	JobID       string   `json:"job_id,omitempty"`
	RepoID      string   `json:"repo_id"`
	Prompt      string   `json:"prompt"`
	ResumeID    string   `json:"resume_id,omitempty"`
	MaxSteps    int      `json:"max_steps,omitempty"`
	Model       string   `json:"model,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Plugins     []string `json:"plugins,omitempty"`
	NodeID      string   `json:"node_id,omitempty"`
	ForceStub   bool     `json:"force_stub,omitempty"`
	Stream      bool     `json:"stream,omitempty"`
	MilestoneID string   `json:"milestone_id,omitempty"`
	PhaseID     string   `json:"phase_id,omitempty"`
	EngReqID    string   `json:"eng_req_id,omitempty"`
	Major       int      `json:"major,omitempty"`
	Minor       int      `json:"minor,omitempty"`
}

// CancelJob POSTs /v1/jobs/{id}/cancel.
func (c *Client) CancelJob(ctx context.Context, jobID string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("job_id required")
	}
	return c.do(ctx, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", map[string]any{}, nil)
}

// CreateJob POSTs /v1/jobs and waits for JobDone (non-streaming).
func (c *Client) CreateJob(ctx context.Context, in JobCreate) (daemon.JobDoneBody, error) {
	in.Stream = false
	var done daemon.JobDoneBody
	if err := c.do(ctx, http.MethodPost, "/v1/jobs", in, &done); err != nil {
		return daemon.JobDoneBody{}, err
	}
	return done, nil
}

// CreateJobStream POSTs /v1/jobs with SSE; calls onEvent for each job.event; returns job.done.
func (c *Client) CreateJobStream(ctx context.Context, in JobCreate, onEvent func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	in.Stream = true
	base := strings.TrimRight(c.BaseURL, "/")
	b, err := json.Marshal(in)
	if err != nil {
		return daemon.JobDoneBody{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/jobs", bytes.NewReader(b))
	if err != nil {
		return daemon.JobDoneBody{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	cli := c.HTTP
	if cli == nil {
		cli = &http.Client{Timeout: 0} // stream until done
	}
	res, err := cli.Do(req)
	if err != nil {
		return daemon.JobDoneBody{}, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		data, _ := io.ReadAll(res.Body)
		return daemon.JobDoneBody{}, fmt.Errorf("scheduler POST /v1/jobs: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return readJobSSE(res.Body, onEvent)
}

func readJobSSE(r io.Reader, onEvent func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventName string
	var dataBuf strings.Builder
	flush := func() error {
		if eventName == "" && dataBuf.Len() == 0 {
			return nil
		}
		data := strings.TrimRight(dataBuf.String(), "\n")
		name := eventName
		eventName = ""
		dataBuf.Reset()
		switch name {
		case "job.event":
			var ev daemon.JobEventBody
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				return err
			}
			if onEvent != nil {
				onEvent(ev)
			}
		case "job.done":
			var done daemon.JobDoneBody
			if err := json.Unmarshal([]byte(data), &done); err != nil {
				return err
			}
			return errDone{done}
		case "error":
			var e struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			if e.Error == "" {
				e.Error = data
			}
			return fmt.Errorf("%s", e.Error)
		}
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				if d, ok := err.(errDone); ok {
					return d.done, nil
				}
				return daemon.JobDoneBody{}, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := flush(); err != nil {
		if d, ok := err.(errDone); ok {
			return d.done, nil
		}
		return daemon.JobDoneBody{}, err
	}
	if err := sc.Err(); err != nil {
		return daemon.JobDoneBody{}, err
	}
	return daemon.JobDoneBody{}, fmt.Errorf("job stream ended without job.done")
}

type errDone struct{ done daemon.JobDoneBody }

func (e errDone) Error() string { return "job.done" }
