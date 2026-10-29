// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/store"
	"github.com/anvil-dev/anvil/internal/tools"
)

// Stage configures one stage of a Complex run (chapter 50 §5.2). The container fills
// Model and Effort from its own configuration; the SDK reads no file and picks no model.
type Stage struct {
	// Model is the stage's model id. Empty uses the session's.
	Model string
	// Effort is how hard the stage thinks. Empty takes the stage's default.
	Effort Effort
	// Tools names the groups the stage's children may use. Empty takes the stage's
	// default: read-only for Decision and Analyze, the worker set for Worker.
	Tools []string
	// MaxSteps bounds one child of the stage; 0 inherits the session's.
	MaxSteps int
}

// ComplexOptions configures the three stages of Mode == ModeComplex (0.50+).
type ComplexOptions struct {
	Decision Stage
	Analyze  Stage
	Worker   Stage
	// MaxParallel bounds how many Workers run at once. 0 means the workflow default.
	MaxParallel int
}

// The stage defaults: Decision is the long chain of reasoning, Analyze maps it onto
// files, Worker does the work. Decision and Analyze are read-only.
var (
	decisionTools = []string{"read", "search", "git", "skills"}
	analyzeTools  = []string{"read", "search", "skills"}
)

func stageEffort(s Stage, def Effort) Effort {
	if s.Effort != "" {
		return s.Effort
	}
	return def
}

func stageTools(reg *tools.Registry, s Stage, def []string) []string {
	if len(s.Tools) > 0 {
		return s.Tools
	}
	return availableSelectors(reg, def)
}

// availableSelectors keeps the default selectors the parent actually has a tool for. A
// default is a wish, not a request: a session without a test runner still gets a Worker,
// just one without verify. A selector the caller names in Stage.Tools is not filtered, so
// asking for a tool the parent lacks is still refused.
func availableSelectors(reg *tools.Registry, def []string) []string {
	if reg == nil {
		return append([]string(nil), def...)
	}
	have := map[string]bool{}
	for _, name := range reg.Names() {
		have[name] = true
	}
	out := make([]string, 0, len(def))
	for _, selector := range def {
		for name := range tools.ExpandToolSelectors([]string{selector}) {
			if have[name] {
				out = append(out, selector)
				break
			}
		}
	}
	return out
}

// complexSpec builds the three-step workflow a Complex run executes. Decision and
// Analyze are ordinary steps with a role and a schema; Worker is a fan-out over the
// roadmap's items whose children are created the way delegate creates one.
func complexSpec(reg *tools.Registry, o ComplexOptions, message, directive, skills string) workflowSpec {
	spec := complexSteps(reg, o, message)
	for i := range spec.Steps {
		spec.Steps[i].directive = directive
		spec.Steps[i].skills = skills
	}
	return spec
}

// withRunLayers appends what the caller's Send adds to a run (its directive and the skills
// matched to its message) to a stage's brief layer. With neither, the brief is returned
// exactly as it was.
func withRunLayers(brief, directive, skills string) string {
	parts := []string{}
	if brief != "" {
		parts = append(parts, brief)
	}
	if skills != "" {
		parts = append(parts, "Skills matched to the caller's request; use them where they apply to your stage:\n"+skills)
	}
	if directive != "" {
		parts = append(parts, "The caller gave this instruction for the whole run; it applies to your stage:\n"+directive)
	}
	return strings.Join(parts, "\n\n")
}

func complexSteps(reg *tools.Registry, o ComplexOptions, message string) workflowSpec {
	return workflowSpec{
		Name:        "complex",
		MaxParallel: o.MaxParallel,
		Steps: []workflowStep{
			{ID: "decide", Prompt: message, literal: true, Role: string(RoleDecision), ResultSchema: schemaDecision,
				Tools: stageTools(reg, o.Decision, decisionTools), Model: o.Decision.Model, MaxSteps: o.Decision.MaxSteps,
				Effort: string(stageEffort(o.Decision, EffortHigh))},
			{ID: "analyze", Prompt: "{{decide.result}}", DependsOn: []string{"decide"}, Role: string(RoleAnalyze), ResultSchema: schemaRoadmap,
				Tools: stageTools(reg, o.Analyze, analyzeTools), Model: o.Analyze.Model, MaxSteps: o.Analyze.MaxSteps,
				Effort: string(stageEffort(o.Analyze, EffortMedium))},
			{ID: "work", Prompt: "{{item}}", DependsOn: []string{"analyze"}, ForEach: &workflowFanOut{From: "analyze", Path: "items"},
				BriefFromItem: true, Tools: stageTools(reg, o.Worker, delegateTools), Model: o.Worker.Model, MaxSteps: o.Worker.MaxSteps,
				Effort: string(stageEffort(o.Worker, EffortLow))},
		},
	}
}

// executeComplex runs a Complex Send: the three-stage workflow on this agent, rendered as
// the run's final message. Its children's events reach Subagents.OnEvent exactly as a
// run_workflow's do, and the run's own stream carries the stage outcome as a message.
func (a *Agent) executeComplex(ctx context.Context, cancelCause context.CancelCauseFunc, wall time.Duration, run *Run, message, directive string) {
	defer func() {
		a.registry.SetRunContext("", "")
		cancelCause(nil)
		a.clearRunState(run)
	}()
	if wall > 0 {
		var wallCancel context.CancelFunc
		ctx, wallCancel = context.WithTimeoutCause(ctx, wall, runtime.ErrMaxWallTime)
		defer wallCancel()
	}
	ctx = withAgent(ctx, a)
	if a.depth == 0 {
		a.budget.reset()
	}
	a.persist(store.Record{Type: store.TypeRunStart, RunID: run.id})
	a.registry.SetRunContext(a.id, run.id)
	a.persistMessageValue(run.id, "user", message)

	status, text, code := a.runComplex(ctx, run, message, directive)

	a.persistMessageValue(run.id, "assistant", text)
	a.mu.Lock()
	a.history = append(a.history, userAssistantPair(message, text)...)
	a.mu.Unlock()
	rr := &RunResult{ID: run.id, Status: status, Result: text, Steps: 3, Code: code}
	if status != StatusFinished {
		rr.Err = firstNonEmpty(strings.SplitN(text, "\n", 2)[0], "the complex run did not finish")
	}
	a.persist(store.Record{Type: store.TypeRunEnd, RunID: run.id, Status: string(status), Steps: rr.Steps})
	run.emit(Event{Type: EventAssistant, Text: text})
	run.emit(Event{Type: EventDone, Text: text})
	var err error
	if status == StatusError {
		err = newError(rr.Err, false, nil)
	}
	a.mu.Lock()
	persistErr := a.persistErr
	a.mu.Unlock()
	if err == nil && persistErr != nil {
		err = newError("session file write failed; the run is not fully recorded", false, persistErr)
	}
	a.clearRunState(run)
	run.finish(rr, err)
}

// runComplex builds the workflow, runs it and renders the answer.
func (a *Agent) runComplex(ctx context.Context, run *Run, message, directive string) (RunStatus, string, Code) {
	spec := complexSpec(a.registry, a.opts.Complex, message, directive, routerSkillsText(a.cwd, a.opts, message))
	if err := validateWorkflow(spec); err != nil {
		return StatusError, err.Error(), ""
	}
	plan, err := newWorkflowPlan(a.registry, spec)
	if err != nil {
		return StatusError, err.Error(), ""
	}
	toolCallID := "complex-" + id.New()
	block, state := executeWorkflow(ctx, a, plan, run.id, toolCallID)
	switch state {
	case workflowFinished:
		return StatusFinished, block, ""
	case workflowCancelled:
		return StatusCancelled, "the complex run was cancelled\n" + block, CodeCancelled
	}
	return StatusError, "the complex run failed\n" + block, ""
}

// complexRefusal is why Send cannot start a Complex run, or "" when it can.
func (a *Agent) complexRefusal() string {
	if !a.opts.Subagents.Enabled {
		return "mode complex needs Subagents.Enabled: its stages are child agents"
	}
	if tools.SelectorSetMatches("edit_file", tools.ExpandToolSelectors(a.opts.DisallowedTools)) ||
		tools.SelectorSetMatches("write_file", tools.ExpandToolSelectors(a.opts.DisallowedTools)) {
		return "mode complex cannot run in plan mode: its Worker stage edits files"
	}
	return ""
}

// persistMessageValue records a user or assistant message that did not come from the loop.
func (a *Agent) persistMessageValue(runID, role, content string) {
	a.persist(store.Record{Type: store.TypeMessage, RunID: runID, Role: role, Content: content})
}

// userAssistantPair is the exchange a Complex run adds to the conversation, so a later
// Send (or a resume) sees what was asked and what the stages concluded.
func userAssistantPair(user, assistant string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: user},
		{Role: llm.RoleAssistant, Content: assistant},
	}
}
