// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"reflect"
	"strings"
	"testing"
)

func TestAnalyzeShellPathsKnown(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		command string
		want    []string
	}{
		{"posix operands", "linux", `rm -rf ../.git/config /tmp/out`, []string{"../.git/config", "/tmp/out"}},
		{"quoted and escaped", "linux", `cat "dir/a b" a\ b`, []string{"dir/a b", "a b"}},
		{"chain pipeline redirection", "linux", `cat src/a | tee out.log && cp x y > logs/z`, []string{"src/a", "out.log", "x", "y", "logs/z"}},
		{"long option value", "linux", `git --git-dir=.git status`, []string{".git", "status"}},
		{"nested posix", "linux", `sh -c 'rm -rf .git/config'`, []string{".git/config"}},
		{"nested posix argument", "linux", `sh -c 'echo ok' .git/config`, []string{"ok", ".git/config"}},
		{"wrapped nested posix", "linux", `env X=1 timeout 5 sh -c 'rm -rf .git/config'`, []string{"X=1", "timeout", "5", ".git/config"}},
		{"wrapped option operand", "linux", `env -C .git sh -c 'pwd'`, []string{".git"}},
		{"exec nested posix", "linux", `exec -a shell sh -c 'cat .git/config'`, []string{"shell", ".git/config"}},
		{"sudo nested posix", "linux", `sudo sh -c 'rm -rf .git/config'`, []string{".git/config"}},
		{"cmd forms", "windows", `type ".git\config" & copy a b > log.txt`, []string{`.git\config`, "a", "b", "log.txt"}},
		{"cmd nested", "windows", `cmd /c "del .git\config"`, []string{`.git\config`}},
		{"cmd keep-open nested", "windows", `cmd /k "type .git\config"`, []string{`.git\config`}},
		{"powershell nested", "windows", `powershell -Command "Remove-Item '.git\config'; Get-Content out.txt"`, []string{`.git\config`, "out.txt"}},
		{"redirect file descriptor", "linux", `cat a 2> errors.log`, []string{"a", "errors.log"}},
		{"no candidates", "linux", `pwd`, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeShellPaths(tc.command, tc.goos)
			if got.State != PathsKnown || got.Problem != "" {
				t.Fatalf("AnalyzeShellPaths() = %+v, want known", got)
			}
			if !reflect.DeepEqual(got.Paths, tc.want) {
				t.Fatalf("Paths = %#v, want %#v", got.Paths, tc.want)
			}
		})
	}
}

func TestAnalyzeShellPathsIndeterminate(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		command string
		problem string
	}{
		{"posix variable", "linux", `rm "$TARGET"`, PathProblemDynamicPath},
		{"command substitution", "linux", `cat "$(pick)"`, PathProblemDynamicPath},
		{"process substitution", "linux", `cat <(pick)`, PathProblemDynamicPath},
		{"glob", "linux", `rm *.tmp`, PathProblemDynamicPath},
		{"cmd variable", "windows", `del %TARGET%`, PathProblemDynamicPath},
		{"unquoted cmd text", "windows", `cmd /c del .git\config`, PathProblemUnsupportedSyntax},
		{"powershell variable", "windows", `powershell -Command "Remove-Item $target"`, PathProblemDynamicPath},
		{"unquoted powershell text", "windows", `powershell -Command Get-Content .git\config`, PathProblemUnsupportedSyntax},
		{"unclosed quote", "linux", `cat "x`, PathProblemUnclosedQuote},
		{"unsupported control", "linux", `for f in x`, PathProblemUnsupportedSyntax},
		{"unsupported heredoc", "linux", "cat <<EOF\nx\nEOF", PathProblemUnsupportedSyntax},
		{"unsupported sudo option", "linux", `sudo -u root sh -c 'cat .git/config'`, PathProblemUnsupportedSyntax},
		{"unsupported interpreter", "linux", `python -c 'open(".git/config")'`, PathProblemUnsupportedSyntax},
		{"encoded powershell", "windows", `powershell -EncodedCommand ZABpAHIA`, PathProblemUnsupportedSyntax},
		{"unknown dialect", "plan9", `cat x`, PathProblemUnknownDialect},
		{"byte limit", "linux", strings.Repeat("x", maxShellCommandBytes+1), PathProblemLimitExceeded},
		{"command limit", "linux", strings.Repeat("pwd;", maxShellCommands+1), PathProblemLimitExceeded},
		{"token limit", "linux", "echo " + strings.Repeat("x ", maxShellTokens), PathProblemLimitExceeded},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeShellPaths(tc.command, tc.goos)
			if got.State != PathsIndeterminate || got.Problem != tc.problem {
				t.Fatalf("AnalyzeShellPaths() = %+v, want indeterminate %q", got, tc.problem)
			}
		})
	}
}

func TestShellAnalysisOnlyRemovesAllows(t *testing.T) {
	set, err := New([]Rule{{Effect: "allow", Tool: "shell"}})
	if err != nil {
		t.Fatal(err)
	}

	d := set.Evaluate(CallFacts{Tool: "shell", Command: `rm "$TARGET"`}, "linux")
	if d.Matched {
		t.Fatalf("Evaluate = %+v, want indeterminate analysis to suppress allow", d)
	}
	if d.PathState != PathsIndeterminate || d.PathProblem != PathProblemDynamicPath {
		t.Fatalf("analysis finding was not preserved: %+v", d)
	}

	ask, err := New([]Rule{{Effect: "ask", Tool: "shell"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ask.Evaluate(CallFacts{Tool: "shell", Command: `rm "$TARGET"`}, "linux"); !got.Matched || got.Effect != Ask {
		t.Fatalf("Evaluate = %+v, want ask rule to remain eligible", got)
	}
}

func FuzzAnalyzeShellPaths(f *testing.F) {
	for _, seed := range []string{
		`rm -rf .git`,
		`cat "$FILE"`,
		`cmd /c "type a"`,
		`a && b | c > out`,
		`'`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, command string) {
		if len(command) > maxShellCommandBytes*2 {
			return
		}
		for _, goos := range []string{"linux", "windows", "unknown"} {
			got := AnalyzeShellPaths(command, goos)
			switch got.State {
			case PathsKnown:
				if got.Problem != "" {
					t.Fatalf("known result has problem: %+v", got)
				}
			case PathsIndeterminate:
				if got.Problem == "" {
					t.Fatalf("indeterminate result has no problem: %+v", got)
				}
			default:
				t.Fatalf("invalid state: %+v", got)
			}
		}
	})
}
