package prompt_test

import (
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/prompt"
)

func TestSystemPromptHardening(t *testing.T) {
	s := prompt.System("/tmp/ws")
	for _, want := range []string{
		"untrusted",
		"exfiltrate",
		"ignore previous instructions",
		"Stay inside the workspace",
	} {
		if !strings.Contains(strings.ToLower(s), strings.ToLower(want)) {
			t.Fatalf("missing %q in prompt:\n%s", want, s)
		}
	}
}

func TestAllowShellCaution(t *testing.T) {
	plain := prompt.SystemWithOptions("/tmp/ws", "", prompt.Options{})
	withShell := prompt.SystemWithOptions("/tmp/ws", "", prompt.Options{AllowShell: true})
	if strings.Contains(plain, "Shell is enabled") {
		t.Fatal("plain prompt should not mention shell enabled")
	}
	if !strings.Contains(withShell, "Shell is enabled") {
		t.Fatal("expected shell caution")
	}
	if !strings.Contains(withShell, "exfiltration") {
		t.Fatal("expected exfiltration caution")
	}
}
