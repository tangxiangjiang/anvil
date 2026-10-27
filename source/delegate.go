// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/tools"
)

// Brief limits (chapter 50 §4.2).
const (
	briefMaxItems = 20
	briefMaxFiles = 64
)

// delegateTools is what a worker gets when the brief names none: it can look, change
// files and run the project's checks, but not shell out or create more agents.
var delegateTools = []string{"read", "search", "edit", "write", "verify", "skills", "git"}

// Brief is the validated hand-off a worker is given.
type Brief struct {
	Goal       string   `json:"goal"`
	Plan       []string `json:"plan"`
	Files      []string `json:"files"`
	Acceptance []string `json:"acceptance"`
	Tools      []string `json:"tools"`
	Effort     string   `json:"effort"`
	MaxSteps   int      `json:"max_steps"`
	Background bool     `json:"background"`
}

// validateBrief checks a brief against the workspace and the session's tools and
// returns every problem it finds, so a model fixes the brief once. It starts no agent.
func validateBrief(cwd string, reg *tools.Registry, b *Brief) []string {
	var problems []string
	if strings.TrimSpace(b.Goal) == "" {
		problems = append(problems, "goal is required")
	}
	problems = append(problems, countProblems("plan", b.Plan, 1, briefMaxItems)...)
	problems = append(problems, countProblems("acceptance", b.Acceptance, 1, briefMaxItems)...)
	problems = append(problems, countProblems("files", b.Files, 1, briefMaxFiles)...)
	for _, f := range b.Files {
		if strings.TrimSpace(f) == "" {
			continue
		}
		if _, err := tools.ResolveInRoot(cwd, filepath.FromSlash(f)); err != nil {
			problems = append(problems, fmt.Sprintf("files: %q is outside the workspace", f))
		}
	}
	if !llm.Effort(b.Effort).Valid() {
		problems = append(problems, fmt.Sprintf("effort: %q is not low, medium or high", b.Effort))
	}
	if b.MaxSteps < 0 {
		problems = append(problems, "max_steps cannot be negative")
	}
	if _, err := resolveWorkerSelectors(reg, b.Tools); err != nil {
		problems = append(problems, err.Error())
	}
	return problems
}

func countProblems(name string, items []string, lo, hi int) []string {
	var out []string
	n := 0
	for _, it := range items {
		if strings.TrimSpace(it) == "" {
			out = append(out, name+": an entry is empty")
			continue
		}
		n++
	}
	if len(items) < lo {
		out = append(out, fmt.Sprintf("%s is required: give %d to %d entries", name, lo, hi))
	} else if len(items) > hi {
		out = append(out, fmt.Sprintf("%s has %d entries; the limit is %d", name, len(items), hi))
	}
	return out
}

// resolveWorkerSelectors is spawn_agent's resolution with the worker default.
func resolveWorkerSelectors(reg *tools.Registry, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string(nil), delegateTools...), nil
	}
	return resolveSubagentSelectors(reg, requested)
}

// renderBrief is the SDK's own rendering of a validated brief: it is the child's L3.
// The text comes from the struct, never from the model's JSON, so formatting cannot
// smuggle an instruction into the layer.
func renderBrief(b *Brief) string {
	var sb strings.Builder
	sb.WriteString("Goal: " + oneLine(b.Goal) + "\n")
	sb.WriteString("Plan:\n")
	for i, step := range b.Plan {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, oneLine(step))
	}
	sb.WriteString("Files you may change (anything else is refused):\n")
	for _, f := range b.Files {
		sb.WriteString("- " + oneLine(f) + "\n")
	}
	sb.WriteString("Acceptance (report each as `[met] <item>` or `[not met] <item>` on its own line, at the end of your final message):\n")
	for _, a := range b.Acceptance {
		sb.WriteString("- " + oneLine(a) + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

var spaceRun = regexp.MustCompile(`\s+`)

// oneLine keeps one entry on one line, so an entry cannot open a new section.
func oneLine(s string) string { return strings.TrimSpace(spaceRun.ReplaceAllString(s, " ")) }

// confineRules is the file allowlist as generated deny rules for the write tools.
func confineRules(files []string) []permissions.Rule {
	globs := make([]string, 0, len(files))
	for _, f := range files {
		globs = append(globs, strings.TrimSpace(f))
	}
	reason := "outside the delegated brief's files"
	var rules []permissions.Rule
	for _, tool := range []string{"write_file", "edit_file", "apply_patch"} {
		rules = append(rules, permissions.Rule{Effect: "deny", Tool: tool, Unless: globs, Reason: reason})
	}
	return rules
}

// acceptanceLine is one `[met] text` / `[not met] text` line a worker ends with.
var acceptanceLine = regexp.MustCompile(`(?mi)^\s*[-*]?\s*\[(met|not met)\]\s*(.*?)\s*$`)

// report states, per acceptance item, what the worker claimed. An item the worker did
// not mention is [unreported], which the parent reads as "check it yourself".
func report(b *Brief, final string, changed []string, recorded bool) string {
	claims := map[string]string{}
	for _, m := range acceptanceLine.FindAllStringSubmatch(final, -1) {
		claims[normalizeItem(m[2])] = strings.ToLower(m[1])
	}
	var sb strings.Builder
	sb.WriteString("acceptance:\n")
	for _, item := range b.Acceptance {
		state := "unreported"
		if got, ok := claims[normalizeItem(item)]; ok {
			state = got
		}
		fmt.Fprintf(&sb, "  %-12s%s\n", "["+state+"]", oneLine(item))
	}
	if !recorded {
		sb.WriteString("changed: (not recorded: checkpoints are off, so check the workspace yourself)\n")
	} else if len(changed) == 0 {
		sb.WriteString("changed: (nothing)\n")
	} else {
		sb.WriteString("changed: " + strings.Join(changed, ", ") + "\n")
	}
	return sb.String()
}

func normalizeItem(s string) string {
	return strings.ToLower(strings.Trim(oneLine(s), " .`"))
}

// delegateTool builds the delegate FuncTool for Multi mode.
func delegateTool(reg *tools.Registry) *tools.FuncTool {
	return &tools.FuncTool{
		Name:    "delegate",
		Builtin: true,
		Description: "Hand one well-specified piece of work to a worker that edits files, and read its report. " +
			"Use it when you can name the files it may change and say what done looks like; it is editing, not research. " +
			"The brief is checked before anything starts. The worker may change only the files you list, and its report " +
			"states each acceptance item as [met], [not met] or [unreported], plus the files it changed. " +
			"For finding something out, use spawn_agent instead.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"goal":{"type":"string","description":"One sentence: what the worker is to achieve."},
				"plan":{"type":"array","items":{"type":"string"},"description":"1 to 20 ordered steps."},
				"files":{"type":"array","items":{"type":"string"},"description":"1 to 64 paths the worker may change; a directory ends with /."},
				"acceptance":{"type":"array","items":{"type":"string"},"description":"1 to 20 checks the worker reports against, one per line."},
				"tools":{"type":"array","items":{"type":"string"},"description":"Tool groups. Default: read, search, edit, write, verify, skills, git."},
				"effort":{"type":"string","enum":["low","medium","high"],"description":"Default: this session's."},
				"max_steps":{"type":"integer","minimum":0},
				"background":{"type":"boolean","description":"Return a task id immediately instead of waiting."}
			},
			"required":["goal","plan","files","acceptance"]
		}`),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			return delegate(ctx, reg, raw)
		},
	}
}

// workerPrompt is the user message a worker is sent: its task is the brief in its system
// prompt, so the message only starts it.
const workerPrompt = "Do the work in your brief. End with the acceptance lines."

// workerOptions builds a worker child's Options: the worker role, the brief rendered as
// its L3, the brief's effort or the parent's, and the file allowlist as generated deny
// rules. delegate and a Complex run's Worker stage both create workers through here, so
// one allowlist and one report serve both.
func (a *Agent) workerOptions(b *Brief, selectors []string, depth int) Options {
	in := spawnParams{Prompt: b.Goal, Description: oneLine(b.Goal), Tools: selectors, MaxSteps: b.MaxSteps}
	opts := a.childOptions(in, selectors, depth)
	opts.SystemPrompt = ""
	opts.Role = RoleWorker
	opts.brief = renderBrief(b)
	if b.Effort != "" {
		opts.Effort = Effort(b.Effort)
	}
	// After the parent's own rules, so theirs are the ones a reader meets first and a
	// path the parent may not touch stays untouchable whatever the brief says.
	opts.confine = append(opts.confine, confineRules(b.Files)...)
	return opts
}

func delegate(ctx context.Context, reg *tools.Registry, raw json.RawMessage) (string, error) {
	parent := agentFromContext(ctx)
	if parent == nil {
		return "", errors.New("delegate is only available inside a run")
	}
	var b Brief
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if problems := validateBrief(parent.cwd, reg, &b); len(problems) > 0 {
		sort.Strings(problems)
		return "", fmt.Errorf("the brief was refused and no worker was started:\n- %s", strings.Join(problems, "\n- "))
	}
	if b.Background {
		return "", errors.New("background delegation is not supported yet; omit background")
	}
	selectors, _ := resolveWorkerSelectors(reg, b.Tools)
	if err := parent.budget.take(); err != nil {
		return "", err
	}

	depth := parent.depth + 1
	opts := parent.workerOptions(&b, selectors, depth)
	ident := subagentIdentity{
		ParentAgentID:    parent.id,
		ParentRunID:      parent.currentRunID(),
		ParentToolCallID: tools.CallID(ctx),
		TaskTitle:        oneLine(b.Goal),
		Depth:            depth,
		Brief:            &b,
	}
	callCtx := withSubagentOrigin(ctx, parent.id, ident.ParentRunID, ident.ParentToolCallID)
	child, err := createSubagent(callCtx, opts, ident)
	if err != nil {
		parent.budget.give()
		return "", fmt.Errorf("start worker: %w", err)
	}
	defer child.Close()

	run, err := child.Send(callCtx, workerPrompt)
	if err != nil {
		return "", fmt.Errorf("start worker: %w", err)
	}
	res, waitErr := run.Wait(callCtx)
	if waitErr != nil && res == nil {
		return "", fmt.Errorf("%s did not finish: %w", child.AgentID(), waitErr)
	}
	if res == nil {
		return "", fmt.Errorf("%s returned no result", child.AgentID())
	}
	changed, recorded := child.changedFiles()
	switch res.Status {
	case StatusFinished:
		return fmt.Sprintf("%s finished in %d steps.\n%s%s", child.AgentID(), res.Steps, report(&b, res.Result, changed, recorded), res.Result), nil
	case StatusCancelled:
		return "", fmt.Errorf("%s was cancelled: %s", child.AgentID(), firstNonEmpty(res.Err, "the run stopped"))
	default:
		// A worker that failed still has a report: every item is unreported, and the
		// files it did change are a fact the parent needs.
		return "", fmt.Errorf("%s failed: %s\n%s", child.AgentID(), firstNonEmpty(res.Err, res.Result), report(&b, "", changed, recorded))
	}
}

// changedFiles lists what this agent's runs captured before changing, from the
// checkpoint record: a fact, not the worker's claim. recorded is false when
// checkpoints are off, so "nothing" is never reported for "not measured".
func (a *Agent) changedFiles() (files []string, recorded bool) {
	if a.opts.Checkpoints == nil {
		return nil, false
	}
	runs, err := ListCheckpoints(a.cwd, a.id, *a.opts.Checkpoints)
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	for _, run := range runs {
		for _, f := range run.Files {
			if !seen[f.Path] {
				seen[f.Path] = true
				files = append(files, f.Path)
			}
		}
	}
	sort.Strings(files)
	return files, true
}
