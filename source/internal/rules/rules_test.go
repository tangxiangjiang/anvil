package rules_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/rules"
)

func TestLoadRulesDefaultEmpty(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("secret rule"), 0o644)
	out, err := rules.Load(dir, rules.Options{})
	if err != nil || out != "" {
		t.Fatalf("got %q err=%v", out, err)
	}
}

func TestLoadProjectRules(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("use tabs"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, ".anvil", "rules"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anvil", "rules", "style.md"), []byte("go fmt"), 0o644)

	out, err := rules.Load(dir, rules.Options{LoadProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "use tabs") || !strings.Contains(out, "go fmt") {
		t.Fatalf("%q", out)
	}
	sys := prompt.SystemWithRules(dir, out)
	if !strings.Contains(sys, "Project rules") || !strings.Contains(sys, "use tabs") {
		t.Fatalf("%q", sys)
	}
}
