// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

// run_workflow evaluates a declarative DAG of subagents. It is orchestration,
// not a programming language: a step is a prompt, a dependency list, and the
// same child agent spawn_agent creates; the only dynamic part is a literal
// substitution of earlier answers into later prompts.
//
// See doc/design/v0.x/41-subagents-and-tasks.md — the chapter states the
// boundary and doc/roadmaps/v0.x/m_0.42/p_0.42.4.md the phase contract.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/tasks"
	"github.com/anvil-dev/anvil/internal/tools"
)

const (
	// workflowMaxSteps and workflowMaxParallel are the hard ceilings; they keep
	// one tool call from becoming a program that spends a session's budget.
	workflowMaxSteps        = 32
	workflowMaxParallel     = 8
	workflowDefaultParallel = 3
	// workflowMessageLimit is how much of one child's final message the result
	// carries. The store holds the rest.
	workflowMessageLimit = 2000
	// workflowMaxProblems bounds the refusal list; five is enough to fix a spec.
	workflowMaxProblems = 5
)

// The states a step (or one fan-out item) can end in. "Pending" is the zero
// value and only the scheduler has to name it.
const (
	workflowFinished  = "finished"
	workflowFailed    = "failed"
	workflowCancelled = "cancelled"
	workflowSkipped   = "skipped"
)

var (
	workflowIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	workflowTokenPattern = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
)

// ---------------------------------------------------------------------------
// the spec
// ---------------------------------------------------------------------------

// workflowSpec is the decoded tool call. Pointers and slices are what make
// "absent" distinguishable from "present but zero".
type workflowSpec struct {
	Name        string         `json:"name"`
	MaxParallel int            `json:"max_parallel"`
	Background  bool           `json:"background"`
	Steps       []workflowStep `json:"steps"`
}

type workflowStep struct {
	ID          string          `json:"id"`
	Prompt      string          `json:"prompt"`
	Tools       []string        `json:"tools"`
	Model       string          `json:"model"`
	MaxSteps    int             `json:"max_steps"`
	DependsOn   []string        `json:"depends_on"`
	ForEach     *workflowFanOut `json:"for_each"`
	MaxParallel int             `json:"max_parallel"`
	// Role, Effort and ResultSchema are what a staged run adds to a step (0.50+). Role is
	// the child's Options.Role, Effort its Options.Effort (default: the parent's), and
	// ResultSchema names a schema the step's final message must satisfy: a message that
	// does not parse or fails it fails the step, and its dependants do not run.
	Role         string `json:"role"`
	Effort       string `json:"effort"`
	ResultSchema string `json:"result_schema"`
	// BriefFromItem makes each fan-out item a delegate brief: the child is created the way
	// delegate creates one, so the file allowlist and the acceptance report apply.
	BriefFromItem bool `json:"brief_from_item"`
	// literal marks a prompt the SDK built from text it does not control, such as a
	// user's message: its braces are not tokens and nothing is substituted into it.
	literal bool
	// directive is the caller's per-run instruction (WithDirective) for a staged run. It is
	// appended to the child's brief layer, so every stage sees it and a Worker that edits
	// files is bound by it. Unexported: a model-written workflow cannot set it.
	directive string
	// skills is the router's matched-skills text for the user's message, handed to every
	// stage the way Single hands it to the main agent. Unexported for the same reason.
	skills string
}

// tokens are the {{...}} references in the step's prompt; a literal prompt has none.
func (s workflowStep) tokens() []string {
	if s.literal {
		return nil
	}
	return workflowTokens(s.Prompt)
}

// substitute fills the step's tokens, or returns a literal prompt untouched.
func (w *workflowRun) substitute(s workflowStep, values map[string]string) string {
	if s.literal {
		return s.Prompt
	}
	return workflowSubstitute(s.Prompt, values)
}

type workflowFanOut struct {
	From string `json:"from"`
	Path string `json:"path"`
}

// decodeWorkflowSpec refuses a misspelled field rather than ignoring it: a
// workflow whose author believes a bound is in force is worse than a refused
// one.
func decodeWorkflowSpec(raw json.RawMessage) (workflowSpec, error) {
	var spec workflowSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return spec, fmt.Errorf("the workflow was refused; no agent was started:\n- invalid workflow: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return spec, errors.New("the workflow was refused; no agent was started:\n- invalid workflow: unexpected data after the workflow object")
	}
	return spec, nil
}

// workflowTool builds the run_workflow FuncTool. It closes over the parent's
// registry for the same reason subagentTool does: a step's tool selector is
// checked against the tools this session actually has.
func workflowTool(reg *tools.Registry) *tools.FuncTool {
	return &tools.FuncTool{
		Name:    "run_workflow",
		Builtin: true,
		Description: "Run a declarative DAG of subagents: steps, dependencies, bounded fan-out. " +
			"A step runs after the steps it depends on; {{<step>.result}} in its prompt is replaced once with " +
			"that step's final message, and for_each runs one child per element of a JSON array with {{item}} and {{index}}. " +
			"This is orchestration, not a scripting language: there are no expressions, conditions or loops. " +
			"Every step is an ordinary subagent, and only the result block returns to this conversation.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"name":{"type":"string","description":"A short name for the workflow. It names the background task and the result."},
				"max_parallel":{"type":"integer","minimum":1,"maximum":8,"description":"How many child agents may run at once. Default 3."},
				"background":{"type":"boolean","description":"Return a task id immediately instead of waiting for the workflow."},
				"steps":{
					"type":"array",
					"minItems":1,
					"maxItems":32,
					"items":{
						"type":"object",
						"properties":{
							"id":{"type":"string","description":"Unique within the workflow: letters, digits, _ and -, at most 64."},
							"prompt":{"type":"string","description":"The whole task for this step's child. It sees nothing else from this conversation."},
							"tools":{"type":"array","items":{"type":"string"},"description":"Group or tool names this step's child may use. Default: read, search, skills, git, vision."},
							"model":{"type":"string","description":"Model id. Empty uses this session's model."},
							"max_steps":{"type":"integer","minimum":0,"description":"Optional step ceiling for this child; it can never exceed this session's own."},
							"depends_on":{"type":"array","items":{"type":"string"},"description":"Step ids that must finish first. They must be listed before this step."},
							"for_each":{"type":"object","properties":{"from":{"type":"string","description":"An earlier step whose final message is JSON."},"path":{"type":"string","description":"The key (or dotted key path) holding the JSON array."}},"required":["from","path"]},
							"max_parallel":{"type":"integer","minimum":1,"maximum":8,"description":"For a fan-out step, how many of its items may run at once. Default: the workflow's."}
						},
						"required":["id","prompt"]
					}
				}
			},
			"required":["name","steps"]
		}`),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			return runWorkflow(ctx, reg, raw)
		},
	}
}

// ---------------------------------------------------------------------------
// validation
// ---------------------------------------------------------------------------

// validateWorkflow is a pure function: no registry, no provider, no clock. The
// phase requires that a refused workflow spends nothing, and the only way to
// prove that is to keep the decision free of anything that could start a run.
func validateWorkflow(spec workflowSpec) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(spec.Name) == "" {
		add("name is required")
	}
	switch {
	case len(spec.Steps) == 0:
		add("steps must contain at least one step")
	case len(spec.Steps) > workflowMaxSteps:
		add("%d steps is more than the limit of %d", len(spec.Steps), workflowMaxSteps)
	}
	if spec.MaxParallel < 0 || spec.MaxParallel > workflowMaxParallel {
		add("max_parallel must be between 1 and %d (omit it for the default of %d)",
			workflowMaxParallel, workflowDefaultParallel)
	}

	index := make(map[string]int, len(spec.Steps))
	valid := make([]bool, len(spec.Steps))
	for i, step := range spec.Steps {
		id := stepID(step)
		if !workflowIDPattern.MatchString(id) {
			add("step %d: id %q must match [A-Za-z0-9_-]{1,64}", i+1, step.ID)
			continue
		}
		if prev, dup := index[id]; dup {
			add("step %q is defined twice (steps %d and %d)", id, prev+1, i+1)
			continue
		}
		index[id] = i
		valid[i] = true
	}

	for i, step := range spec.Steps {
		if !valid[i] {
			continue
		}
		id := stepID(step)
		for _, rawDep := range step.DependsOn {
			dep := strings.TrimSpace(rawDep)
			if dep == "" {
				add("step %q has an empty depends_on entry", id)
				continue
			}
			j, ok := index[dep]
			if !ok {
				add("step %q depends on unknown step %q", id, dep)
				continue
			}
			if j == i {
				add("step %q depends on itself", id)
			}
		}
		if step.ForEach == nil {
			continue
		}
		from := strings.TrimSpace(step.ForEach.From)
		switch j, ok := index[from]; {
		case from == "":
			add("step %q: for_each.from is required", id)
		case !ok:
			add("step %q: for_each.from names unknown step %q", id, from)
		case j == i:
			add("step %q: for_each.from names the step itself", id)
		case spec.Steps[j].ForEach != nil:
			add("step %q fans out over %q, which is itself a fan-out step; fan-out is one level only", id, from)
		}
		if strings.TrimSpace(step.ForEach.Path) == "" {
			add("step %q: for_each.path is required", id)
		}
	}

	// A cycle is reported as itself rather than as two "listed later" messages,
	// because the cycle is the thing its author has to break.
	if leftover := workflowCycleLeftovers(spec, index, valid); len(leftover) > 0 {
		add("steps %s form or depend on a dependency cycle", strings.Join(leftover, ", "))
	} else {
		for i, step := range spec.Steps {
			if !valid[i] {
				continue
			}
			id := stepID(step)
			for _, rawDep := range step.DependsOn {
				dep := strings.TrimSpace(rawDep)
				if j, ok := index[dep]; ok && j > i {
					add("step %q depends on %q, which is listed later; a step may only depend on steps before it", id, dep)
				}
			}
			if step.ForEach != nil {
				from := strings.TrimSpace(step.ForEach.From)
				if j, ok := index[from]; ok && j > i {
					add("step %q: for_each.from %q is listed later; a step may only fan out over steps before it", id, from)
				}
			}
			for _, tok := range step.tokens() {
				ref, isResult := workflowResultToken(tok)
				if !isResult {
					continue
				}
				if j, ok := index[ref]; ok && j > i {
					add("step %q: {{%s}} names %q, which is listed later; a step may only read earlier answers", id, tok, ref)
				}
			}
		}
	}

	for i, step := range spec.Steps {
		if !valid[i] {
			continue
		}
		id := stepID(step)
		if strings.TrimSpace(step.Prompt) == "" {
			add("step %q: prompt is required", id)
		}
		if step.MaxSteps < 0 {
			add("step %q: max_steps cannot be negative", id)
		}
		if !prompt.Role(step.Role).Valid() {
			add("step %q: role %q is not one of subagent, worker, decision, analyze", id, step.Role)
		}
		if !llm.Effort(step.Effort).Valid() {
			add("step %q: effort %q is not low, medium or high", id, step.Effort)
		}
		if step.ResultSchema != "" && !knownSchema(step.ResultSchema) {
			add("step %q: result_schema %q is not one of %s, %s", id, step.ResultSchema, schemaDecision, schemaRoadmap)
		}
		if step.BriefFromItem && step.ForEach == nil {
			add("step %q: brief_from_item needs for_each", id)
		}
		if step.MaxParallel < 0 || step.MaxParallel > workflowMaxParallel {
			add("step %q: max_parallel must be between 1 and %d", id, workflowMaxParallel)
		}
		for _, tok := range step.tokens() {
			switch tok {
			case "item", "index":
				if step.ForEach == nil {
					add("step %q: {{%s}} is only available in a step with for_each", id, tok)
				}
				continue
			}
			ref, isResult := workflowResultToken(tok)
			if !isResult {
				add("step %q: unknown token {{%s}}; the only tokens are {{<step>.result}}, {{item}} and {{index}}", id, tok)
				continue
			}
			if !workflowIDPattern.MatchString(ref) {
				add("step %q: {{%s}} does not name a step", id, tok)
				continue
			}
			if ref == id {
				add("step %q: {{%s}} reads the step's own answer, which does not exist yet", id, tok)
				continue
			}
			if _, ok := index[ref]; !ok {
				add("step %q: {{%s}} names unknown step %q", id, tok, ref)
			}
		}
	}

	return workflowRefusal(problems)
}

// workflowRefusal renders the problem list a tool result carries. It stops at
// workflowMaxProblems but says how many remain, so a long list is still honest.
func workflowRefusal(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("the workflow was refused; no agent was started:")
	shown := len(problems)
	if shown > workflowMaxProblems {
		shown = workflowMaxProblems
	}
	for _, problem := range problems[:shown] {
		b.WriteString("\n- ")
		b.WriteString(problem)
	}
	if more := len(problems) - shown; more > 0 {
		fmt.Fprintf(&b, "\n- and %d more problem(s)", more)
	}
	return errors.New(b.String())
}

// workflowCycleLeftovers is Kahn's algorithm over the known edges. The nodes it
// cannot remove are in a cycle or behind one; naming them is more useful than
// naming the one forward edge through which the cycle was first noticed.
func workflowCycleLeftovers(spec workflowSpec, index map[string]int, valid []bool) []string {
	n := len(spec.Steps)
	indegree := make([]int, n)
	dependents := make([][]int, n)
	nodes := 0
	for i, step := range spec.Steps {
		if !valid[i] {
			continue
		}
		nodes++
		seen := map[int]bool{}
		for _, dep := range workflowDependencyIDs(step) {
			j, ok := index[dep]
			// A self-edge is reported by name, not as a cycle among many.
			if !ok || j == i || seen[j] {
				continue
			}
			seen[j] = true
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}
	queue := make([]int, 0, n)
	for i := range spec.Steps {
		if valid[i] && indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	removed := 0
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		removed++
		for _, next := range dependents[i] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if removed == nodes {
		return nil
	}
	out := make([]string, 0, nodes-removed)
	for i := range spec.Steps {
		if valid[i] && indegree[i] > 0 {
			out = append(out, stepID(spec.Steps[i]))
		}
	}
	return out
}

func stepID(step workflowStep) string {
	return strings.TrimSpace(step.ID)
}

// workflowTokens returns the inner text of every {{…}} token, trimmed.
func workflowTokens(prompt string) []string {
	matches := workflowTokenPattern.FindAllStringSubmatch(prompt, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// workflowResultToken splits "<id>.result"; the bool is false for item, index
// and anything else.
func workflowResultToken(token string) (string, bool) {
	if token == "item" || token == "index" || !strings.HasSuffix(token, ".result") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimSuffix(token, ".result")), true
}

// workflowDependencyIDs is every step a prompt cannot be correct without:
// declared dependencies, the fan-out source, and the steps it reads through
// {{id.result}}. Reading an answer without declaring a dependency would make
// the substitution race the step that produces it, so the reader waits too.
func workflowDependencyIDs(step workflowStep) []string {
	out := make([]string, 0, len(step.DependsOn)+1)
	for _, raw := range step.DependsOn {
		if dep := strings.TrimSpace(raw); dep != "" {
			out = append(out, dep)
		}
	}
	if step.ForEach != nil {
		if from := strings.TrimSpace(step.ForEach.From); from != "" {
			out = append(out, from)
		}
	}
	for _, tok := range step.tokens() {
		if ref, ok := workflowResultToken(tok); ok && ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// execution
// ---------------------------------------------------------------------------

// workflowPlan is the validated spec plus everything resolved against this
// session: step indices for dependencies and child tool selectors.
type workflowPlan struct {
	spec      workflowSpec
	ids       []string
	index     map[string]int
	deps      [][]int
	selectors [][]string
	parallel  int
}

// newWorkflowPlan resolves what the pure validation could not: tool selectors
// against this session's registry. Every selector is resolved before the first
// agent exists, so a bad one costs nothing either.
func newWorkflowPlan(reg *tools.Registry, spec workflowSpec) (*workflowPlan, error) {
	plan := &workflowPlan{
		spec:      spec,
		ids:       make([]string, len(spec.Steps)),
		index:     make(map[string]int, len(spec.Steps)),
		deps:      make([][]int, len(spec.Steps)),
		selectors: make([][]string, len(spec.Steps)),
		parallel:  spec.MaxParallel,
	}
	if plan.parallel <= 0 {
		plan.parallel = workflowDefaultParallel
	}
	for i, step := range spec.Steps {
		plan.ids[i] = stepID(step)
		plan.index[plan.ids[i]] = i
	}
	var problems []string
	for i, step := range spec.Steps {
		selectors, err := resolveSubagentSelectors(reg, step.Tools)
		if err != nil {
			problems = append(problems, fmt.Sprintf("step %q: %v", plan.ids[i], err))
			continue
		}
		plan.selectors[i] = selectors
		seen := map[int]bool{}
		for _, dep := range workflowDependencyIDs(step) {
			j, ok := plan.index[dep]
			if !ok || j == i || seen[j] {
				continue
			}
			seen[j] = true
			plan.deps[i] = append(plan.deps[i], j)
		}
		sort.Ints(plan.deps[i])
	}
	return plan, workflowRefusal(problems)
}

// runWorkflow is the tool handler: decode, refuse, then either wait or file the
// whole thing as one background task.
func runWorkflow(ctx context.Context, reg *tools.Registry, raw json.RawMessage) (string, error) {
	parent := agentFromContext(ctx)
	if parent == nil {
		return "", errors.New("run_workflow is only available inside a run")
	}
	spec, err := decodeWorkflowSpec(raw)
	if err != nil {
		return "", err
	}
	if err := validateWorkflow(spec); err != nil {
		return "", err
	}
	plan, err := newWorkflowPlan(reg, spec)
	if err != nil {
		return "", err
	}
	parentRunID := parent.currentRunID()
	toolCallID := tools.CallID(ctx)
	if spec.Background {
		return workflowBackground(reg, parent, plan, parentRunID, toolCallID)
	}
	block, status := executeWorkflow(ctx, parent, plan, parentRunID, toolCallID)
	if status == workflowFinished {
		return block, nil
	}
	// A failed or cancelled workflow is a tool error whose text is still the
	// whole result block: the model needs the per-step detail, not a summary.
	return "", errors.New(block)
}

// workflowBackground files the workflow as one unit of background work. Its
// children are still individual agents, but the parent and the task table see
// one row whose title is the workflow's name.
func workflowBackground(reg *tools.Registry, parent *Agent, plan *workflowPlan, parentRunID, toolCallID string) (string, error) {
	mgr := reg.TaskManager()
	if mgr == nil {
		return "", errors.New("background tasks are not available in this session")
	}
	title := workflowTitle(plan.spec.Name)
	info, err := mgr.StartAgent(tasks.AgentSpec{
		Title:   title,
		AgentID: parent.id,
		Run: func(taskCtx context.Context) (string, string, error) {
			block, status := executeWorkflow(taskCtx, parent, plan, parentRunID, toolCallID)
			return status, block, nil
		},
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s started in the background: workflow %s\nRead it with task_output, stop it with task_cancel.",
		info.ID, title), nil
}

// workflowOutcome is one step's terminal state as the result block reports it.
type workflowOutcome struct {
	status  string
	agentID string
	result  string
	errText string
	reason  string
	items   []workflowItemOutcome
}

// workflowItemOutcome is one child of a fan-out step.
type workflowItemOutcome struct {
	index   int
	status  string
	agentID string
	result  string
	errText string
}

type workflowDone struct {
	index   int
	outcome workflowOutcome
}

type workflowRun struct {
	plan        *workflowPlan
	parent      *Agent
	parentRunID string
	toolCallID  string
	depth       int
	// children bounds how many child agents of this workflow exist at once.
	children chan struct{}
}

// executeWorkflow runs the DAG and returns the result block and its status. The
// scheduler is the only writer of outcomes; each step goroutine hands its
// result back over a channel, which keeps the dependency reads race-free.
func executeWorkflow(ctx context.Context, parent *Agent, plan *workflowPlan, parentRunID, toolCallID string) (string, string) {
	n := len(plan.spec.Steps)
	w := &workflowRun{
		plan:        plan,
		parent:      parent,
		parentRunID: parentRunID,
		toolCallID:  toolCallID,
		depth:       parent.depth + 1,
		children:    make(chan struct{}, plan.parallel),
	}
	outcomes := make([]workflowOutcome, n)
	pending := make([]bool, n)
	for i := range pending {
		pending[i] = true
	}
	done := make(chan workflowDone, n)
	running := 0
	remaining := n

	for remaining > 0 {
		// A step whose dependency did not finish never runs. Cascading this
		// before launching keeps a newly blocked step from starting in the same
		// pass that its dependency fails.
		for i := 0; i < n; i++ {
			if !pending[i] {
				continue
			}
			if reason, blocked := w.blocked(i, outcomes); blocked {
				outcomes[i] = workflowOutcome{status: workflowSkipped, reason: reason}
				pending[i] = false
				remaining--
			}
		}

		if ctx.Err() == nil {
			for i := 0; i < n && running < plan.parallel; i++ {
				if !pending[i] || !w.ready(i, outcomes) {
					continue
				}
				pending[i] = false
				running++
				// Values are read here, in the scheduler, because every step this
				// one reads has reached its terminal state.
				values := w.values(i, outcomes)
				go func(i int, values map[string]string) {
					done <- workflowDone{index: i, outcome: w.runStep(ctx, i, values)}
				}(i, values)
			}
		}

		if running == 0 {
			// Nothing is left that could start. A validated DAG only gets here
			// when the workflow was cancelled before its first step.
			for i := 0; i < n; i++ {
				if pending[i] {
					outcomes[i] = workflowOutcome{
						status:  workflowCancelled,
						errText: "the workflow was cancelled before this step started",
					}
					pending[i] = false
					remaining--
				}
			}
			break
		}
		d := <-done
		running--
		outcomes[d.index] = d.outcome
		remaining--
	}

	status := workflowStatus(ctx, outcomes)
	return renderWorkflow(plan, outcomes, status), status
}

// blocked reports why a step can no longer run. An outcome is an empty status
// while its step is still running, and a running dependency blocks nothing yet.
func (w *workflowRun) blocked(i int, outcomes []workflowOutcome) (string, bool) {
	for _, dep := range w.plan.deps[i] {
		switch outcomes[dep].status {
		case "", workflowFinished:
			continue
		case workflowSkipped:
			return fmt.Sprintf("depends on %s, which was skipped", w.plan.ids[dep]), true
		default:
			return fmt.Sprintf("depends on %s, which %s", w.plan.ids[dep], outcomes[dep].status), true
		}
	}
	return "", false
}

// ready reports whether every dependency has finished. A dependency that failed
// or was skipped has already skipped this step, so finished is the only answer
// that lets it run.
func (w *workflowRun) ready(i int, outcomes []workflowOutcome) bool {
	for _, dep := range w.plan.deps[i] {
		if outcomes[dep].status != workflowFinished {
			return false
		}
	}
	return true
}

// values is the substitution table for one step: every dependency's final
// message, keyed by the token it fills.
func (w *workflowRun) values(i int, outcomes []workflowOutcome) map[string]string {
	values := make(map[string]string, len(w.plan.deps[i]))
	for _, dep := range w.plan.deps[i] {
		values[w.plan.ids[dep]+".result"] = outcomes[dep].result
	}
	return values
}

func (w *workflowRun) runStep(ctx context.Context, i int, values map[string]string) workflowOutcome {
	step := w.plan.spec.Steps[i]
	if step.ForEach == nil {
		item := w.runChild(ctx, i, -1, w.substitute(step, values))
		return workflowOutcome{
			status:  item.status,
			agentID: item.agentID,
			result:  item.result,
			errText: item.errText,
		}
	}
	return w.runFanOut(ctx, i, values)
}

// runFanOut reads the source step's answer as JSON, then runs one child per
// element. A source that is not an array fails the step with a sentence naming
// the path — never a silent zero or one child.
func (w *workflowRun) runFanOut(ctx context.Context, i int, values map[string]string) workflowOutcome {
	step := w.plan.spec.Steps[i]
	fromIndex := w.plan.index[strings.TrimSpace(step.ForEach.From)]
	sourceID := w.plan.ids[fromIndex]
	path := strings.TrimSpace(step.ForEach.Path)
	items, err := workflowArrayAt(values[sourceID+".result"], path)
	if err != nil {
		return workflowOutcome{
			status:  workflowFailed,
			errText: fmt.Sprintf("for_each over %s: %v", sourceID, err),
		}
	}

	limit := step.MaxParallel
	if limit <= 0 {
		limit = w.plan.parallel
	}
	// Items that name their own order (an id and a depends_on, as a roadmap's do) run in
	// that order. A fan-out whose items name none takes the path below, unchanged.
	if order, problems := itemOrder(items); len(problems) > 0 {
		return workflowOutcome{
			status:  workflowFailed,
			errText: fmt.Sprintf("for_each over %s: the items' own order is invalid:\n- %s", sourceID, strings.Join(problems, "\n- ")),
		}
	} else if order != nil {
		return w.runOrderedFanOut(ctx, i, values, items, order, limit)
	}
	out := make([]workflowItemOutcome, len(items))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	cancelled := false
	for idx := range items {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			cancelled = true
		}
		if cancelled {
			for rest := idx; rest < len(items); rest++ {
				out[rest] = workflowItemOutcome{
					index:   rest,
					status:  workflowCancelled,
					errText: "the workflow was cancelled before this item started",
				}
			}
			break
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			vars := make(map[string]string, len(values)+2)
			for k, v := range values {
				vars[k] = v
			}
			vars["item"] = items[idx]
			vars["index"] = strconv.Itoa(idx)
			out[idx] = w.runChild(ctx, i, idx, w.substitute(step, vars))
		}(idx)
	}
	wg.Wait()
	return aggregateWorkflowItems(out)
}

// itemOrder reads the order the items declare for themselves. It returns deps[i], the
// indices item i waits for, or nil when no item declares a dependency, which is every
// fan-out that existed before. An item is an object with a string id and an optional
// depends_on list; one that is not has no id and so cannot be depended on.
func itemOrder(items []string) ([][]int, []string) {
	ids := make([]string, len(items))
	deps := make([][]string, len(items))
	any := false
	for i, text := range items {
		var it struct {
			ID        string   `json:"id"`
			DependsOn []string `json:"depends_on"`
		}
		if json.Unmarshal([]byte(text), &it) != nil {
			continue
		}
		ids[i], deps[i] = it.ID, it.DependsOn
		if len(it.DependsOn) > 0 {
			any = true
		}
	}
	if !any {
		return nil, nil
	}
	index := map[string]int{}
	for i, id := range ids {
		if id != "" {
			if _, dup := index[id]; !dup {
				index[id] = i
			}
		}
	}
	// A depended-on item must have an id: otherwise "unknown item" would hide that the
	// item exists but cannot be named.
	problems := itemOrderProblems(ids, deps)
	if len(problems) > 0 {
		return nil, problems
	}
	order := make([][]int, len(items))
	for i, list := range deps {
		for _, dep := range list {
			order[i] = append(order[i], index[dep])
		}
	}
	return order, nil
}

// runOrderedFanOut runs the items of a fan-out whose items depend on one another. An item
// starts when the items it names have finished and a slot is free; one whose dependency
// did not finish is skipped, as a step is. Slots are taken after the wait, so an item
// that is waiting never holds one a runnable item needs.
func (w *workflowRun) runOrderedFanOut(ctx context.Context, i int, values map[string]string, items []string, deps [][]int, limit int) workflowOutcome {
	step := w.plan.spec.Steps[i]
	out := make([]workflowItemOutcome, len(items))
	finished := make([]chan struct{}, len(items))
	for idx := range finished {
		finished[idx] = make(chan struct{})
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for idx := range items {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer close(finished[idx])
			for _, dep := range deps[idx] {
				select {
				case <-finished[dep]:
				case <-ctx.Done():
					out[idx] = workflowItemOutcome{index: idx, status: workflowCancelled,
						errText: "the workflow was cancelled before this item started"}
					return
				}
				if out[dep].status != workflowFinished {
					out[idx] = workflowItemOutcome{index: idx, status: workflowSkipped,
						errText: fmt.Sprintf("depends on item %d, which %s", dep, out[dep].status)}
					return
				}
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[idx] = workflowItemOutcome{index: idx, status: workflowCancelled,
					errText: "the workflow was cancelled before this item started"}
				return
			}
			defer func() { <-sem }()
			vars := make(map[string]string, len(values)+2)
			for k, v := range values {
				vars[k] = v
			}
			vars["item"] = items[idx]
			vars["index"] = strconv.Itoa(idx)
			out[idx] = w.runChild(ctx, i, idx, w.substitute(step, vars))
		}(idx)
	}
	wg.Wait()
	return aggregateWorkflowItems(out)
}

// runChild is the same child spawn_agent creates, with the workflow call as its
// origin and the step's description as its task title.
func (w *workflowRun) runChild(ctx context.Context, stepIndex, itemIndex int, prompt string) workflowItemOutcome {
	step := w.plan.spec.Steps[stepIndex]
	out := workflowItemOutcome{index: itemIndex}
	description := workflowTitle(w.plan.spec.Name) + " / " + w.plan.ids[stepIndex]
	if itemIndex >= 0 {
		description += " #" + strconv.Itoa(itemIndex)
	}

	if err := w.parent.budget.take(); err != nil {
		out.status = workflowFailed
		out.errText = err.Error()
		return out
	}
	select {
	case w.children <- struct{}{}:
	case <-ctx.Done():
		out.status = workflowCancelled
		out.errText = "the workflow was cancelled before this child started"
		return out
	}
	defer func() { <-w.children }()

	var opts Options
	var worker *Brief
	if step.BriefFromItem {
		// The item is a brief: the child is created the way delegate creates one, so the
		// allowlist and the acceptance report apply. The item was validated when the
		// roadmap was, but a hand-written workflow gets the same check here.
		brief, problems := briefFromItem(w.parent.cwd, prompt)
		if len(problems) > 0 {
			w.parent.budget.give()
			out.status = workflowFailed
			out.errText = "the item is not a valid brief:\n- " + strings.Join(problems, "\n- ")
			return out
		}
		worker = brief
		if step.Effort != "" && worker.Effort == "" {
			worker.Effort = step.Effort
		}
		opts = w.parent.workerOptions(worker, w.plan.selectors[stepIndex], w.depth)
		if step.Model != "" {
			opts.Model = Model{ID: step.Model}
		}
		prompt = workerPrompt
	} else {
		opts = w.parent.childOptions(spawnParams{
			Prompt:      prompt,
			Description: description,
			Model:       step.Model,
			MaxSteps:    step.MaxSteps,
		}, w.plan.selectors[stepIndex], w.depth)
		if step.Role != "" {
			opts.SystemPrompt = ""
			opts.Role = Role(step.Role)
		}
		if step.Effort != "" {
			opts.Effort = Effort(step.Effort)
		}
		// A stage that must answer in a schema is told the shape in its own brief layer.
		opts.brief = schemaBrief(step.ResultSchema)
	}
	opts.brief = withRunLayers(opts.brief, step.directive, step.skills)
	ident := subagentIdentity{
		ParentAgentID:    w.parent.id,
		ParentRunID:      w.parentRunID,
		ParentToolCallID: w.toolCallID,
		TaskTitle:        description,
		Depth:            w.depth,
		Brief:            worker,
	}
	callCtx := withSubagentOrigin(ctx, w.parent, ident.ParentRunID, ident.ParentToolCallID)
	child, err := createSubagent(callCtx, opts, ident)
	if err != nil {
		out.status = workflowFailed
		out.errText = "start subagent: " + err.Error()
		return out
	}
	defer child.Close()
	out.agentID = child.AgentID()

	run, err := child.Send(callCtx, prompt)
	if err != nil {
		out.status = workflowFailed
		out.errText = "start subagent: " + err.Error()
		return out
	}
	res, waitErr := run.Wait(callCtx)
	if waitErr != nil && res == nil {
		out.status = workflowFailed
		out.errText = fmt.Sprintf("%s did not finish: %v", child.AgentID(), waitErr)
		return out
	}
	if res == nil {
		out.status = workflowFailed
		out.errText = fmt.Sprintf("%s returned no result", child.AgentID())
		return out
	}
	switch res.Status {
	case StatusFinished:
		out.status = workflowFinished
		out.result = res.Result
		if worker != nil {
			changed, recorded := child.changedFiles()
			out.result = report(worker, res.Result, changed, recorded) + res.Result
		}
		if step.ResultSchema != "" {
			env := schemaEnv{cwd: w.parent.cwd, budgetLeft: w.parent.budget.remaining()}
			if problems := validateSchema(step.ResultSchema, res.Result, env); len(problems) > 0 {
				out.status = workflowFailed
				out.errText = fmt.Sprintf("the %s stage's answer does not satisfy the %s schema:\n- %s",
					w.plan.ids[stepIndex], step.ResultSchema, strings.Join(problems, "\n- "))
			}
		}
	case StatusCancelled:
		out.status = workflowCancelled
		out.errText = firstNonEmpty(res.Err, "the run stopped")
	default:
		out.status = workflowFailed
		out.errText = firstNonEmpty(res.Err, res.Result, "the run failed")
	}
	return out
}

// aggregateWorkflowItems folds the items of one fan-out step into the step's
// state. One item reads as itself; several are prefixed so the joined answer can
// still be told apart.
func aggregateWorkflowItems(items []workflowItemOutcome) workflowOutcome {
	out := workflowOutcome{status: workflowFinished, items: items}
	var results, errs []string
	for _, item := range items {
		switch item.status {
		case workflowFailed, workflowSkipped:
			out.status = workflowFailed
		case workflowCancelled:
			if out.status != workflowFailed {
				out.status = workflowCancelled
			}
		}
		if out.agentID == "" {
			out.agentID = item.agentID
		}
		label := ""
		if len(items) > 1 {
			label = fmt.Sprintf("[%d] ", item.index)
		}
		if item.result != "" {
			results = append(results, label+item.result)
		}
		if item.errText != "" {
			errs = append(errs, label+item.errText)
		}
	}
	out.result = strings.Join(results, "\n\n")
	out.errText = strings.Join(errs, "\n\n")
	return out
}

// workflowArrayAt extracts a JSON array from a child's final message. The whole
// message is tried first (the prompt asks for JSON); a fenced block or a JSON
// value embedded in prose is accepted after that, because a model that explains
// before answering has still answered.
func workflowArrayAt(text, path string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	doc, err := decodeWorkflowValue(trimmed)
	if err != nil {
		return nil, fmt.Errorf("the result is not JSON: %v", err)
	}
	keys := strings.Split(path, ".")
	current := doc
	var walked []string
	for _, key := range keys {
		obj, ok := current.(map[string]any)
		if !ok {
			where := "the result"
			if len(walked) > 0 {
				where = fmt.Sprintf("%q", strings.Join(walked, "."))
			}
			return nil, fmt.Errorf("%s is not an object, so it has no key %q", where, key)
		}
		next, ok := obj[key]
		if !ok {
			return nil, fmt.Errorf("the result has no key %q (path %q)", key, strings.Join(append(walked, key), "."))
		}
		walked = append(walked, key)
		current = next
	}
	arr, ok := current.([]any)
	if !ok {
		return nil, fmt.Errorf("%q is not an array", path)
	}
	out := make([]string, 0, len(arr))
	for _, element := range arr {
		if s, ok := element.(string); ok {
			out = append(out, s)
			continue
		}
		encoded, err := json.Marshal(element)
		if err != nil {
			return nil, fmt.Errorf("%q contains a value that cannot be read back: %v", path, err)
		}
		out = append(out, string(encoded))
	}
	return out, nil
}

func decodeWorkflowValue(text string) (any, error) {
	var doc any
	if err := json.Unmarshal([]byte(text), &doc); err == nil {
		return doc, nil
	}
	// A fenced block: drop the opening and closing fence lines.
	if strings.HasPrefix(text, "```") {
		body := text
		if nl := strings.IndexByte(body, '\n'); nl >= 0 {
			body = body[nl+1:]
		} else {
			body = ""
		}
		if end := strings.LastIndex(body, "```"); end >= 0 {
			body = body[:end]
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &doc); err == nil {
			return doc, nil
		}
	}
	// A value embedded in prose: the widest brace span is the best guess.
	first := strings.IndexByte(text, '{')
	last := strings.LastIndexByte(text, '}')
	if first >= 0 && last > first {
		if err := json.Unmarshal([]byte(text[first:last+1]), &doc); err == nil {
			return doc, nil
		}
	}
	return nil, errors.New("no JSON object in the message")
}

// workflowSubstitute replaces every token in one pass. Inserted answers are
// never scanned again: a child that writes {{item}} into its answer cannot make
// the next child's prompt expand it.
func workflowSubstitute(prompt string, values map[string]string) string {
	return workflowTokenPattern.ReplaceAllStringFunc(prompt, func(token string) string {
		inner := strings.TrimSpace(token[2 : len(token)-2])
		if value, ok := values[inner]; ok {
			return value
		}
		return token
	})
}

func workflowStatus(ctx context.Context, outcomes []workflowOutcome) string {
	if ctx.Err() != nil {
		return workflowCancelled
	}
	status := workflowFinished
	for _, outcome := range outcomes {
		switch outcome.status {
		case workflowFailed:
			status = workflowFailed
		case workflowCancelled:
			if status != workflowFailed {
				status = workflowCancelled
			}
		}
	}
	return status
}

// ---------------------------------------------------------------------------
// the result
// ---------------------------------------------------------------------------

// renderWorkflow is the block the parent reads and a background task returns.
// Rows keep the document's order, which is how a workflow author refers to it.
func renderWorkflow(plan *workflowPlan, outcomes []workflowOutcome, status string) string {
	var b strings.Builder
	counts := map[string]int{}
	for _, outcome := range outcomes {
		counts[outcome.status]++
	}
	noun := "steps"
	if len(outcomes) == 1 {
		noun = "step"
	}
	fmt.Fprintf(&b, "workflow %s: %s (%d %s", workflowTitle(plan.spec.Name), status, len(outcomes), noun)
	var extras []string
	for _, state := range []string{workflowFailed, workflowSkipped, workflowCancelled} {
		if counts[state] > 0 {
			extras = append(extras, fmt.Sprintf("%d %s", counts[state], state))
		}
	}
	if len(extras) > 0 {
		b.WriteString(", ")
		b.WriteString(strings.Join(extras, ", "))
	}
	b.WriteString(")\n")

	idWidth, agentWidth := 2, 1
	for i, outcome := range outcomes {
		idWidth = max(idWidth, utf8.RuneCountInString(plan.ids[i]))
		if outcome.agentID != "" {
			agentWidth = max(agentWidth, utf8.RuneCountInString(outcome.agentID))
		}
	}
	for i, outcome := range outcomes {
		agent := outcome.agentID
		if agent == "" {
			agent = "—"
		}
		summary := outcome.reason
		if summary == "" {
			summary = workflowFirstLine(outcome.errText)
		}
		if summary == "" {
			summary = workflowFirstLine(outcome.result)
		}
		fmt.Fprintf(&b, "%s  %-9s  %s  %s\n",
			padRight(plan.ids[i], idWidth), outcome.status, padRight(agent, agentWidth),
			workflowSummary(summary))
	}

	for i, outcome := range outcomes {
		if outcome.status == workflowSkipped {
			continue
		}
		if len(outcome.items) > 0 {
			for _, item := range outcome.items {
				text := firstNonEmpty(item.errText, item.result)
				if strings.TrimSpace(text) == "" {
					if item.status != workflowFinished {
						continue
					}
					text = "(finished without a message)"
				}
				fmt.Fprintf(&b, "\n%s[%d]:\n%s\n", plan.ids[i], item.index, workflowTruncate(text, item.agentID))
			}
			continue
		}
		text := firstNonEmpty(outcome.errText, outcome.result)
		if strings.TrimSpace(text) == "" {
			if outcome.status != workflowFinished {
				continue
			}
			text = "(finished without a message)"
		}
		fmt.Fprintf(&b, "\n%s:\n%s\n", plan.ids[i], workflowTruncate(text, outcome.agentID))
	}
	return strings.TrimRight(b.String(), "\n")
}

// workflowTruncate keeps the head of one child's message. The store file holds
// the rest, and the tail says which file to open.
func workflowTruncate(text, agentID string) string {
	runes := []rune(text)
	if len(runes) <= workflowMessageLimit {
		return text
	}
	tail := "… [truncated"
	if agentID != "" {
		tail += "; open " + agentID + " for the full transcript"
	}
	tail += "]"
	return string(runes[:workflowMessageLimit]) + "\n" + tail
}

func workflowFirstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	return text
}

func workflowSummary(text string) string {
	runes := []rune(workflowFirstLine(text))
	const width = 72
	if len(runes) <= width {
		return string(runes)
	}
	return string(runes[:width]) + "…"
}

// workflowTitle is the name as one line: a task title must not span lines.
func workflowTitle(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

func padRight(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}
