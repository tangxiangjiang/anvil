package anvil

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestSettingSourcesProjectLoadsMCP(t *testing.T) {
	exe := buildFakeMCP(t)
	dir := t.TempDir()
	cursorDir := filepath.Join(dir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpJSON := map[string]any{
		"mcpServers": map[string]any{
			"fake": map[string]any{
				"command": exe,
			},
		},
	}
	raw, _ := json.Marshal(mcpJSON)
	if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local: LocalOptions{
			CWD:            dir,
			SettingSources: []SettingSource{SettingSourceProject},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	found := false
	for _, name := range a.registry.Names() {
		if strings.HasPrefix(name, "mcp_fake_") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mcp tool from project mcp.json, tools=%v", a.registry.Names())
	}
}

func TestSettingSourcesEmptySkipsFileMCP(t *testing.T) {
	exe := buildFakeMCP(t)
	dir := t.TempDir()
	cursorDir := filepath.Join(dir, ".cursor")
	_ = os.MkdirAll(cursorDir, 0o755)
	raw, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{"fake": map[string]any{"command": exe}},
	})
	_ = os.WriteFile(filepath.Join(cursorDir, "mcp.json"), raw, 0o644)

	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir}, // no SettingSources
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range a.registry.Names() {
		if strings.HasPrefix(name, "mcp_") {
			t.Fatalf("file MCP should not load without SettingSources, got %s", name)
		}
	}
}

func TestDisallowedToolsOmitsShell(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey:          "k",
		Model:           Model{ID: "m"},
		Local:           LocalOptions{CWD: dir},
		DisallowedTools: []string{"shell"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range a.registry.Names() {
		if name == "shell" {
			t.Fatal("shell should be denied")
		}
	}
}

func TestSystemPromptOverride(t *testing.T) {
	dir := t.TempDir()
	custom := "CUSTOM_SYSTEM_PROMPT_XYZ"
	a, err := Create(context.Background(), Options{
		APIKey:       "k",
		Model:        Model{ID: "m"},
		Local:        LocalOptions{CWD: dir},
		SystemPrompt: custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !strings.Contains(a.systemPrompt, custom) {
		t.Fatalf("system prompt=%q", a.systemPrompt)
	}
	if strings.Contains(a.systemPrompt, "You are Anvil") {
		t.Fatal("default Anvil prompt should be replaced")
	}
}

func TestForceSendCancelsBusyRun(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.complete = blockingCompleter{}

	run1, err := a.Send(context.Background(), "block")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := a.Send(context.Background(), "second"); err == nil {
		t.Fatal("expected busy error without Force")
	}
	run2, err := a.Send(context.Background(), "forced", WithForce())
	if err != nil {
		t.Fatal(err)
	}
	res1, _ := run1.Wait(context.Background())
	if res1 == nil || res1.Status != StatusCancelled {
		t.Fatalf("prior run status=%v", res1)
	}
	// Unblock second run by cancelling it too — replace completer outcome via cancel.
	_ = run2.Cancel(context.Background())
	_, _ = run2.Wait(context.Background())
}

func TestListRuns(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()
	a.complete = scriptOnce{msg: llm.Message{Role: llm.RoleAssistant, Content: "one"}}
	run, err := a.Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		// finished may still return nil err
	}
	a.complete = scriptOnce{msg: llm.Message{Role: llm.RoleAssistant, Content: "two"}}
	run2, err := a.Send(context.Background(), "again")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = run2.Wait(context.Background())
	_ = a.Close()

	runs, err := ListRuns(context.Background(), id, ListOptions{Local: LocalOptions{CWD: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) < 2 {
		t.Fatalf("want >=2 runs, got %+v", runs)
	}
	finished := 0
	for _, r := range runs {
		if r.Status == string(StatusFinished) {
			finished++
		}
	}
	if finished < 2 {
		t.Fatalf("want finished runs, got %+v", runs)
	}
}

func TestHooksDenyShell(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755)
	hooksRaw := `{
		"version": 1,
		"beforeShell": [{"denyContains": "FORBIDDEN_CMD", "message": "blocked by fixture"}]
	}`
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "hooks.json"), []byte(hooksRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local: LocalOptions{
			CWD:            dir,
			SettingSources: []SettingSource{SettingSourceProject},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	res := a.registry.Execute(context.Background(), "shell", mustJSON(map[string]any{
		"command": "echo FORBIDDEN_CMD",
	}))
	if !res.IsError || !strings.Contains(res.Content, "blocked by fixture") {
		t.Fatalf("%+v", res)
	}
}

func TestPrewarm(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".cursor", "mcp.json"), []byte(`{"mcpServers":{}}`), 0o644)
	release, err := Prewarm(context.Background(), dir, []SettingSource{SettingSourceProject})
	if err != nil {
		t.Fatal(err)
	}
	if release == nil {
		t.Fatal("expected release func")
	}
	release()
}

func TestReloadRefreshesHooks(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anvil"), 0o755)
	a, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local: LocalOptions{
			CWD:            dir,
			SettingSources: []SettingSource{SettingSourceProject},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	hooksRaw := `{"version":1,"beforeShell":[{"denyContains":"NOPE","message":"after reload"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".anvil", "hooks.json"), []byte(hooksRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := a.registry.Execute(context.Background(), "shell", mustJSON(map[string]any{"command": "NOPE"}))
	if !res.IsError || !strings.Contains(res.Content, "after reload") {
		t.Fatalf("%+v", res)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func buildFakeMCP(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakemcp.exe")
	cmd := exec.Command("go", "build", "-o", out, "./internal/mcp/fakecmd")
	cmd.Dir = findSourceRoot(t)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake mcp: %v\n%s", err, outBytes)
	}
	return out
}

func findSourceRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
		return wd
	}
	return wd
}