// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/verify"
)

// VerifyConfig holds optional test/build command specs.
type VerifyConfig struct {
	Test  verify.CommandSpec
	Build verify.CommandSpec
}

// SetVerifyConfig registers run_tests / build when commands are configured.
func (r *Registry) SetVerifyConfig(cfg VerifyConfig) {
	if r == nil {
		return
	}
	r.Unregister("run_tests")
	r.Unregister("build")
	r.verifyCfg = cfg
	if len(cfg.Test.Command) > 0 {
		r.register(&runTestsTool{r: r})
	}
	if len(cfg.Build.Command) > 0 {
		r.register(&buildTool{r: r})
	}
}

type runTestsTool struct{ r *Registry }

func (t *runTestsTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name: "run_tests",
		Description: "Run the configured project test command (argv from Options/project.json). " +
			"Pass selector/path/extra_args as validated tokens only (no shell metacharacters).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"selector":{"type":"string","description":"e.g. go test -run pattern"},
				"path":{"type":"string","description":"package/path arg appended after --"},
				"extra_args":{"type":"array","items":{"type":"string"}}
			}
		}`),
	}
}

func (t *runTestsTool) Execute(ctx context.Context, args json.RawMessage) Result {
	spec := t.r.verifyCfg.Test
	if len(spec.Command) == 0 {
		return Result{Content: "run_tests is not configured; set Options.Test.Command or .anvil/project.json", IsError: true}
	}
	var in struct {
		Selector  string   `json:"selector"`
		Path      string   `json:"path"`
		ExtraArgs []string `json:"extra_args"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{Content: "invalid args: " + err.Error(), IsError: true}
		}
	}
	extra := append([]string{}, in.ExtraArgs...)
	sel := strings.TrimSpace(in.Selector)
	if sel != "" {
		if err := verify.ValidateToken(sel); err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
		if verify.LooksLikeGoTest(spec.Command) {
			extra = append(extra, "-run", sel)
		} else {
			extra = append(extra, sel)
		}
	}
	path := strings.TrimSpace(in.Path)
	if path != "" {
		if err := verify.ValidateToken(path); err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
		extra = append(extra, path)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = verify.DefaultTestTimeout()
	}
	res, err := verify.Run(ctx, t.r.cwd, spec, extra, timeout)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	out := verify.FormatResult("run_tests", res)
	return Result{Content: out, IsError: !res.OK}
}

type buildTool struct{ r *Registry }

func (t *buildTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "build",
		Description: "Run the configured project build/compile command (argv from Options/project.json).",
		Parameters: mustSchema(`{
			"type":"object",
			"properties":{
				"target":{"type":"string"},
				"extra_args":{"type":"array","items":{"type":"string"}}
			}
		}`),
	}
}

func (t *buildTool) Execute(ctx context.Context, args json.RawMessage) Result {
	spec := t.r.verifyCfg.Build
	if len(spec.Command) == 0 {
		return Result{Content: "build is not configured; set Options.Build.Command or .anvil/project.json", IsError: true}
	}
	var in struct {
		Target    string   `json:"target"`
		ExtraArgs []string `json:"extra_args"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{Content: "invalid args: " + err.Error(), IsError: true}
		}
	}
	extra := append([]string{}, in.ExtraArgs...)
	if tgt := strings.TrimSpace(in.Target); tgt != "" {
		if err := verify.ValidateToken(tgt); err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
		extra = append(extra, tgt)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = verify.DefaultBuildTimeout()
	}
	res, err := verify.Run(ctx, t.r.cwd, spec, extra, timeout)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	return Result{Content: verify.FormatResult("build", res), IsError: !res.OK}
}
