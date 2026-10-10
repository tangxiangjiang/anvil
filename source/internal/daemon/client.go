// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"net"
	"strings"
)

// Dial connects to an agentd listen address.
func Dial(addr string) (net.Conn, error) {
	if strings.HasPrefix(addr, "tcp://") {
		return net.Dial("tcp", strings.TrimPrefix(addr, "tcp://"))
	}
	return net.Dial("unix", addr)
}

// Client is a thin controller-side helper.
type Client struct {
	conn net.Conn
}

// NewClient wraps a connection.
func NewClient(conn net.Conn) *Client {
	return &Client{conn: conn}
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }

// Hello authenticates.
func (c *Client) Hello(token, clientName string) (HelloOKBody, error) {
	body, err := MarshalBody(HelloBody{Token: token, Client: clientName})
	if err != nil {
		return HelloOKBody{}, err
	}
	if err := WriteEnvelope(c.conn, Envelope{Kind: KindHello, ID: "1", Body: body}); err != nil {
		return HelloOKBody{}, err
	}
	env, err := ReadEnvelope(c.conn)
	if err != nil {
		return HelloOKBody{}, err
	}
	switch env.Kind {
	case KindHelloOK:
		var ok HelloOKBody
		_ = DecodeBody(env, &ok)
		return ok, nil
	case KindHelloFail:
		var fail HelloFailBody
		_ = DecodeBody(env, &fail)
		return HelloOKBody{}, fmt.Errorf("hello failed: %s", fail.Message)
	default:
		return HelloOKBody{}, fmt.Errorf("unexpected kind %s", env.Kind)
	}
}

// WorkerList requests capacity snapshot.
func (c *Client) WorkerList() (WorkerListResultBody, error) {
	if err := WriteEnvelope(c.conn, Envelope{Kind: KindWorkerList, ID: "2"}); err != nil {
		return WorkerListResultBody{}, err
	}
	env, err := ReadEnvelope(c.conn)
	if err != nil {
		return WorkerListResultBody{}, err
	}
	if env.Kind != KindWorkerListR {
		return WorkerListResultBody{}, fmt.Errorf("unexpected kind %s", env.Kind)
	}
	var out WorkerListResultBody
	_ = DecodeBody(env, &out)
	return out, nil
}

// Assign sends job.assign (does not wait for done).
func (c *Client) Assign(job JobAssignBody) error {
	body, err := MarshalBody(job)
	if err != nil {
		return err
	}
	return WriteEnvelope(c.conn, Envelope{Kind: KindJobAssign, ID: job.JobID, Body: body})
}

// Cancel sends job.cancel.
func (c *Client) Cancel(jobID string) error {
	body, err := MarshalBody(JobCancelBody{JobID: jobID})
	if err != nil {
		return err
	}
	return WriteEnvelope(c.conn, Envelope{Kind: KindJobCancel, ID: jobID, Body: body})
}

// ReadMsg reads the next envelope.
func (c *Client) ReadMsg() (Envelope, error) {
	return ReadEnvelope(c.conn)
}
