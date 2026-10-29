// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func item(id string, deps ...string) map[string]any {
	return map[string]any{"id": id, "goal": "g " + id, "plan": []string{"p"}, "files": []string{"pkg/"},
		"acceptance": []string{"a"}, "depends_on": deps}
}

func roadmapJSON(items ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"items": items})
	return string(b)
}

func schemaProblems(t *testing.T, name, text string, budget int) string {
	t.Helper()
	return strings.Join(validateSchema(name, text, schemaEnv{cwd: t.TempDir(), budgetLeft: budget}), "\n")
}

func TestSchemaDecision(t *testing.T) {
	good := `{"summary":"use A","options":[{"name":"A","rationale":"r","rejected_because":null},{"name":"B","rationale":"r","rejected_because":"slower"}],"chosen":"A","risks":["x"]}`
	if p := schemaProblems(t, schemaDecision, good, -1); p != "" {
		t.Fatalf("a valid decision was refused: %s", p)
	}
	fenced := "Here is my decision:\n```json\n" + good + "\n```"
	if p := schemaProblems(t, schemaDecision, fenced, -1); p != "" {
		t.Fatalf("a fenced decision was refused: %s", p)
	}
	rejects := map[string]struct{ text, want string }{
		"not json":          {"I think we should use A", "not a JSON object"},
		"no summary":        {`{"options":[{"name":"A","rationale":"r"}],"chosen":"A"}`, "summary is required"},
		"no options":        {`{"summary":"s","options":[],"chosen":"A"}`, "at least one option"},
		"no chosen":         {`{"summary":"s","options":[{"name":"A","rationale":"r"}]}`, "chosen is required"},
		"chosen not listed": {`{"summary":"s","options":[{"name":"A","rationale":"r"}],"chosen":"Z"}`, "not the name of any option"},
		"unnamed option":    {`{"summary":"s","options":[{"rationale":"r"}],"chosen":"A"}`, "name is required"},
		"wrong shape":       {`{"summary":["a"],"options":[],"chosen":"A"}`, "wrong shape"},
	}
	for name, tc := range rejects {
		if p := schemaProblems(t, schemaDecision, tc.text, -1); !strings.Contains(p, tc.want) {
			t.Errorf("%s: problems %q do not contain %q", name, p, tc.want)
		}
	}
}

func TestSchemaRoadmap(t *testing.T) {
	good := roadmapJSON(item("a"), item("b", "a"))
	if p := schemaProblems(t, schemaRoadmap, good, -1); p != "" {
		t.Fatalf("a valid roadmap was refused: %s", p)
	}
	if p := schemaProblems(t, schemaRoadmap, roadmapJSON(item("only")), -1); p != "" {
		t.Fatalf("a one-item roadmap was refused: %s", p)
	}

	var seventeen []map[string]any
	for i := 0; i < 17; i++ {
		seventeen = append(seventeen, item(fmt.Sprintf("i%d", i)))
	}
	noFiles := item("nofiles")
	noFiles["files"] = []string{}
	noAcc := item("noacc")
	delete(noAcc, "acceptance")
	escape := item("escape")
	escape["files"] = []string{"../outside"}
	extra := item("extra")
	extra["tools"] = []string{"shell"}
	rejects := map[string]struct{ text, want string }{
		"no items":             {`{"items":[]}`, "1 to 16"},
		"17 items":             {roadmapJSON(seventeen...), "17 entries"},
		"duplicate id":         {roadmapJSON(item("a"), item("a")), "used twice"},
		"cycle":                {roadmapJSON(item("a", "b"), item("b", "a")), "dependency cycle"},
		"self dependency":      {roadmapJSON(item("a", "a")), "depends on itself"},
		"unknown dependency":   {roadmapJSON(item("a", "ghost")), "unknown item"},
		"files escape":         {roadmapJSON(escape), "outside the workspace"},
		"no files":             {roadmapJSON(noFiles), "files is required"},
		"no acceptance":        {roadmapJSON(noAcc), "acceptance is required"},
		"a key the stage owns": {roadmapJSON(extra), "unknown field"},
		"not json":             {"the plan is to edit things", "not a JSON object"},
	}
	for name, tc := range rejects {
		if p := schemaProblems(t, schemaRoadmap, tc.text, -1); !strings.Contains(p, tc.want) {
			t.Errorf("%s: problems %q do not contain %q", name, p, tc.want)
		}
	}
}

// A roadmap larger than the agents still available is refused by the validator, before
// any Worker starts.
func TestSchemaRoadmapBudget(t *testing.T) {
	two := roadmapJSON(item("a"), item("b"))
	if p := schemaProblems(t, schemaRoadmap, two, 1); !strings.Contains(p, "only 1 more agents") {
		t.Fatalf("an over-budget roadmap was accepted: %q", p)
	}
	if p := schemaProblems(t, schemaRoadmap, two, 2); p != "" {
		t.Fatalf("a roadmap that fits was refused: %s", p)
	}
}

func TestSchemaBriefNamesTheShape(t *testing.T) {
	for _, name := range []string{schemaDecision, schemaRoadmap} {
		if !knownSchema(name) || schemaBrief(name) == "" || !strings.Contains(schemaBrief(name), "refused") {
			t.Fatalf("schema %q has no brief that says a failing message is refused", name)
		}
	}
	if knownSchema("plan") {
		t.Fatal("an unknown schema was accepted")
	}
}
