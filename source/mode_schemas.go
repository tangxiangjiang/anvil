// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The named schemas a workflow step's final message can be required to satisfy
// (chapter 50 §5.3). Structure is for what a machine consumes; the text inside is for
// the next model.
const (
	schemaDecision = "decision"
	schemaRoadmap  = "roadmap"
)

// roadmapMaxItems is the most work items one roadmap may hold.
const roadmapMaxItems = 16

func knownSchema(name string) bool { return name == schemaDecision || name == schemaRoadmap }

// schemaEnv is what a validator needs from the run it validates for.
type schemaEnv struct {
	cwd string
	// budgetLeft is how many more agents the tree may create; negative means unbounded.
	// A roadmap with more items than this is refused before any Worker starts.
	budgetLeft int
}

// validateSchema checks a step's final message against a named schema and returns every
// problem, so the model that wrote it can fix it once. An empty result means it holds.
func validateSchema(name, text string, env schemaEnv) []string {
	doc, err := decodeWorkflowValue(strings.TrimSpace(text))
	if err != nil {
		return []string{"the message is not a JSON object"}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return []string{"the message cannot be read back as JSON"}
	}
	switch name {
	case schemaDecision:
		return validateDecision(raw)
	case schemaRoadmap:
		return validateRoadmap(raw, env)
	}
	return []string{fmt.Sprintf("unknown result_schema %q", name)}
}

type decisionOption struct {
	Name            string  `json:"name"`
	Rationale       string  `json:"rationale"`
	RejectedBecause *string `json:"rejected_because"`
}

type decisionDoc struct {
	Summary    string           `json:"summary"`
	Constraint []string         `json:"constraints"`
	Options    []decisionOption `json:"options"`
	Chosen     string           `json:"chosen"`
	Risks      []string         `json:"risks"`
	OutOfScope []string         `json:"out_of_scope"`
}

func validateDecision(raw []byte) []string {
	var d decisionDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return []string{"the decision document has the wrong shape: " + err.Error()}
	}
	var problems []string
	if strings.TrimSpace(d.Summary) == "" {
		problems = append(problems, "summary is required")
	}
	if len(d.Options) == 0 {
		problems = append(problems, "options needs at least one option")
	}
	names := map[string]bool{}
	for i, o := range d.Options {
		if strings.TrimSpace(o.Name) == "" {
			problems = append(problems, fmt.Sprintf("options[%d]: name is required", i))
			continue
		}
		names[o.Name] = true
	}
	switch {
	case strings.TrimSpace(d.Chosen) == "":
		problems = append(problems, "chosen is required")
	case !names[d.Chosen]:
		problems = append(problems, fmt.Sprintf("chosen %q is not the name of any option", d.Chosen))
	}
	return problems
}

// roadmapItem is one work item: a brief minus tools, effort, steps and background,
// which the stage's configuration sets, plus the item's place in the order.
type roadmapItem struct {
	ID         string   `json:"id"`
	Goal       string   `json:"goal"`
	Plan       []string `json:"plan"`
	Files      []string `json:"files"`
	Acceptance []string `json:"acceptance"`
	DependsOn  []string `json:"depends_on"`
}

func (it roadmapItem) brief() *Brief {
	return &Brief{Goal: it.Goal, Plan: it.Plan, Files: it.Files, Acceptance: it.Acceptance}
}

// decodeRoadmapItem reads one item strictly: a key that is not part of an item (a
// model writing tools or effort into it) is a problem to fix, not a field to ignore.
func decodeRoadmapItem(raw []byte) (roadmapItem, error) {
	var it roadmapItem
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&it)
	return it, err
}

func validateRoadmap(raw []byte, env schemaEnv) []string {
	var doc struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return []string{"the roadmap has the wrong shape: " + err.Error()}
	}
	var problems []string
	switch {
	case len(doc.Items) == 0:
		return []string{"items needs 1 to " + fmt.Sprint(roadmapMaxItems) + " work items"}
	case len(doc.Items) > roadmapMaxItems:
		return []string{fmt.Sprintf("items has %d entries; the limit is %d", len(doc.Items), roadmapMaxItems)}
	}
	if env.budgetLeft >= 0 && len(doc.Items) > env.budgetLeft {
		problems = append(problems, fmt.Sprintf("the roadmap has %d items but only %d more agents can be created in this run; make it smaller",
			len(doc.Items), env.budgetLeft))
	}
	var ids []string
	deps := make([][]string, len(doc.Items))
	seen := map[string]bool{}
	for i, rawItem := range doc.Items {
		it, err := decodeRoadmapItem(rawItem)
		label := fmt.Sprintf("items[%d]", i)
		if err != nil {
			problems = append(problems, label+": "+err.Error())
			ids = append(ids, "")
			continue
		}
		if !workflowIDPattern.MatchString(it.ID) {
			problems = append(problems, fmt.Sprintf("%s: id %q must match [A-Za-z0-9_-]{1,64}", label, it.ID))
		} else if seen[it.ID] {
			problems = append(problems, fmt.Sprintf("%s: id %q is used twice", label, it.ID))
		}
		seen[it.ID] = true
		ids = append(ids, it.ID)
		deps[i] = it.DependsOn
		if it.ID != "" {
			label = fmt.Sprintf("item %q", it.ID)
		}
		for _, p := range validateBrief(env.cwd, nil, it.brief()) {
			problems = append(problems, label+": "+p)
		}
	}
	problems = append(problems, itemOrderProblems(ids, deps)...)
	sort.Strings(problems)
	return problems
}

// itemOrderProblems checks the order a set of items declares for itself: every
// depends_on names another item, and the dependencies contain no cycle. It is the
// workflow's step check applied to items.
func itemOrderProblems(ids []string, deps [][]string) []string {
	index := map[string]int{}
	for i, id := range ids {
		if id != "" {
			index[id] = i
		}
	}
	var problems []string
	indegree := make([]int, len(ids))
	dependents := make([][]int, len(ids))
	for i, list := range deps {
		if ids[i] == "" {
			continue
		}
		seen := map[int]bool{}
		for _, dep := range list {
			j, ok := index[dep]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("item %q depends on unknown item %q", ids[i], dep))
			case j == i:
				problems = append(problems, fmt.Sprintf("item %q depends on itself", ids[i]))
			case !seen[j]:
				seen[j] = true
				indegree[i]++
				dependents[j] = append(dependents[j], i)
			}
		}
	}
	var queue []int
	nodes := 0
	for i := range ids {
		if ids[i] == "" {
			continue
		}
		nodes++
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	removed := 0
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		removed++
		for _, next := range dependents[i] {
			if indegree[next]--; indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if removed < nodes {
		var stuck []string
		for i := range ids {
			if ids[i] != "" && indegree[i] > 0 {
				stuck = append(stuck, ids[i])
			}
		}
		problems = append(problems, fmt.Sprintf("items %s form or depend on a dependency cycle", strings.Join(stuck, ", ")))
	}
	return problems
}

// schemaBrief is the SDK's own text telling a stage what shape its final message must
// have, rendered as the stage's brief layer (L3). A message that fails is refused and
// the step fails, so the stage is told that too.
func schemaBrief(name string) string {
	switch name {
	case schemaDecision:
		return `End with one JSON object and nothing after it. A document that does not hold is refused and the run stops.
{"summary":"the decision in a paragraph","constraints":["..."],
 "options":[{"name":"...","rationale":"...","rejected_because":"... or null"}],
 "chosen":"the name of one option","risks":["..."],"out_of_scope":["..."]}
summary, chosen and at least one option are required; chosen must name an option.`
	case schemaRoadmap:
		return `End with one JSON object and nothing after it. A roadmap that does not hold is refused and no work starts.
{"items":[{"id":"unique id","goal":"...","plan":["..."],"files":["paths the worker may change; a directory ends with /"],
 "acceptance":["checks the worker reports against"],"depends_on":["ids of items that must finish first"]}]}
1 to ` + fmt.Sprint(roadmapMaxItems) + ` items. Every item needs a goal, 1 to 20 plan steps, 1 to 64 files inside the workspace and 1 to 20
acceptance checks. depends_on must name other items and contain no cycle. Do not add other keys to an item.`
	}
	return ""
}

// briefFromItem reads one roadmap item, as a fan-out step hands it over, into a Brief and
// validates it. Tools, effort and steps are the stage's, so an item that carries them is
// refused the same way the roadmap schema refuses it.
func briefFromItem(cwd, text string) (*Brief, []string) {
	it, err := decodeRoadmapItem([]byte(strings.TrimSpace(text)))
	if err != nil {
		return nil, []string{err.Error()}
	}
	b := it.brief()
	if problems := validateBrief(cwd, nil, b); len(problems) > 0 {
		sort.Strings(problems)
		return nil, problems
	}
	return b, nil
}
