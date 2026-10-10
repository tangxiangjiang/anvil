// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	body, err := MarshalBody(HelloBody{Token: "secret", Client: "test"})
	if err != nil {
		t.Fatal(err)
	}
	in := Envelope{Proto: ProtoVersion, Kind: KindHello, ID: "1", Body: body}
	var buf bytes.Buffer
	if err := WriteEnvelope(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadEnvelope(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != KindHello || out.ID != "1" || out.Proto != ProtoVersion {
		t.Fatalf("got %+v", out)
	}
	var hb HelloBody
	if err := DecodeBody(out, &hb); err != nil {
		t.Fatal(err)
	}
	if hb.Token != "secret" || hb.Client != "test" {
		t.Fatalf("body %+v", hb)
	}
}

func TestJobAssignRoundTrip(t *testing.T) {
	body, _ := MarshalBody(JobAssignBody{JobID: "j1", RepoID: "demo", Prompt: "hi", ForceStub: true})
	var buf bytes.Buffer
	_ = WriteEnvelope(&buf, Envelope{Kind: KindJobAssign, Body: body})
	env, err := ReadEnvelope(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var job JobAssignBody
	_ = DecodeBody(env, &job)
	if !job.ForceStub || job.Prompt != "hi" {
		t.Fatalf("%+v", job)
	}
}
