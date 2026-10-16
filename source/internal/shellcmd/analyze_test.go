// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package shellcmd

import "testing"

func TestAnalyze(t *testing.T) {
	cases := []struct {
		command string
		want    Verdict
	}{
		// Proved read-only.
		{"ls", ReadOnly},
		{"ls -la", ReadOnly},
		{"cat README.md", ReadOnly},
		{"echo hello", ReadOnly},
		{"wc -l f", ReadOnly},
		{"du -sh .", ReadOnly},
		{"/bin/ls", ReadOnly},
		{"ls.exe", ReadOnly},
		{"type ps", ReadOnly},
		{"date", ReadOnly},
		{"env", ReadOnly},
		{`grep -rn "name" .`, ReadOnly},
		{`find . -name '*.go'`, ReadOnly},
		{`sed -n '1p' f`, ReadOnly},
		{`sed 's/a/b/g' f`, ReadOnly},
		{"timeout 5 ls", ReadOnly},
		{"timeout -k 5 10 ls", ReadOnly},
		{"nice -n 5 ls", ReadOnly},
		{"env A=1 ls", ReadOnly},
		{"git status", ReadOnly},
		{"git status --short", ReadOnly},
		{"git log --oneline -1", ReadOnly},
		{"git diff", ReadOnly},
		{"git branch", ReadOnly},
		{"git branch -a", ReadOnly},
		{"git --version", ReadOnly},
		{"go version", ReadOnly},
		{"go env", ReadOnly},
		{"cd sub && git status", ReadOnly},

		// Provably mutating. These are the calls that must keep prompting.
		{"rm -rf x", NotReadOnly},
		{"touch f", NotReadOnly},
		{"mkdir d", NotReadOnly},
		{"cp a b", NotReadOnly},
		{"date -s 2026-01-01", NotReadOnly},
		{"find . -delete", NotReadOnly},
		{"find . -exec rm {} +", NotReadOnly},
		{"find . -fprint out", NotReadOnly},
		{"sed -i 's/a/b/' f", NotReadOnly},
		{"sed -ni 'p' f", NotReadOnly},
		{"sed 'w out' f", NotReadOnly},
		{"sed 's/a/b/w out' f", NotReadOnly},
		{"git fetch", NotReadOnly},
		{"git push origin main", NotReadOnly},
		{"git commit -m 'x'", NotReadOnly},
		{"git diff --output=x", NotReadOnly},
		{"go build ./...", NotReadOnly},
		{"go test ./...", NotReadOnly},
		{"go env -w GOFLAGS=-v", NotReadOnly},
		{"ls && rm -rf x", NotReadOnly},

		// Unknown: asked about, never allowed.
		{"", CannotProve},
		{"ls > out", CannotProve},
		{"ls >> out", CannotProve},
		{"cat < in", CannotProve},
		{"echo `date`", CannotProve},
		{"echo $(date)", CannotProve},
		{"x=$(date)", CannotProve},
		{"ls *.go", CannotProve},
		{"eval ls", CannotProve},
		{"sudo ls", CannotProve},
		{"xargs rm", CannotProve},
		{"curl -s https://example.com", CannotProve},
		{"sh -c 'ls'", CannotProve},
		{"foo bar", CannotProve},
		{"git", CannotProve},
		{"git frobnicate", CannotProve},
		{"go frobnicate", CannotProve},
		{"sed -f script.sed f", CannotProve},
		{"timeout ls", CannotProve},
		{"nice", CannotProve},
		{"ps aux | grep go", CannotProve}, // ps has no curated rule, so the line is unknown
	}
	for _, tc := range cases {
		if got := Analyze(tc.command, "linux"); got != tc.want {
			t.Errorf("Analyze(%q) = %s, want %s", tc.command, got, tc.want)
		}
	}
}

// A quoted metacharacter is literal to the shell, so a quoted glob or path
// separator must not cost the verdict — that is what makes `find . -name '*.go'`
// usable while `find . -name *.go` stays unknown.
func TestAnalyzeQuotingIsTheDifference(t *testing.T) {
	cases := []struct {
		command string
		goos    string
		want    Verdict
	}{
		{"find . -name *.go", "linux", CannotProve},
		{"find . -name '*.go'", "linux", ReadOnly},
		{`dir C:\temp`, "windows", ReadOnly},
		{`dir C:\temp`, "linux", CannotProve},
		{"echo %PATH%", "windows", CannotProve},
		{"dir C:\\a", "windows", ReadOnly}, // in cmd a backslash separates path segments, it does not escape
		{"ls -la", "windows", ReadOnly},
	}
	for _, tc := range cases {
		if got := Analyze(tc.command, tc.goos); got != tc.want {
			t.Errorf("Analyze(%q, %s) = %s, want %s", tc.command, tc.goos, got, tc.want)
		}
	}
}

// readOnlySamples is every command the table above expects to be proved.
var readOnlySamples = []string{
	"ls -la", "cat README.md", "echo hello", "wc -l f", "du -sh .",
	`grep -rn "name" .`, `find . -name '*.go'`, `sed -n '1p' f`,
	`sed 's/a/b/g' f`, "timeout 5 ls", "nice -n 5 ls", "env A=1 ls",
	"git status", "git log --oneline -1", "cd sub && git status",
	"go version", "go env",
}

// The property a caller depends on, stated structurally rather than by example:
// a proved line stays proved only because *every* segment was proved. Appending
// a mutating segment — or a redirection, which is unprovable — must take the
// verdict away. An implementation that classified only the first segment would
// pass the table above and fail here.
func TestReadOnlyDoesNotSurviveAnAppend(t *testing.T) {
	for _, command := range readOnlySamples {
		if got := Analyze(command, "linux"); got != ReadOnly {
			t.Fatalf("Analyze(%q) = %s, want read-only; the sample is wrong, not the property", command, got)
		}
		for _, suffix := range []string{" && rm -rf x", " > out", " | tee out", "; touch f", " && echo $(date)"} {
			if got := Analyze(command+suffix, "linux"); got == ReadOnly {
				t.Errorf("Analyze(%q) = read-only: a line must not stay proved once an unproved segment is appended", command+suffix)
			}
		}
	}
}

// ReadOnly means the line was taken apart literally: every segment lexed into
// tokens the shell would not rewrite. If a segment stops lexing, the verdict
// must stop being read-only — a rule matcher compares these tokens, and
// comparing a token the shell rewrites is comparing a string the user never
// wrote.
func TestReadOnlyImpliesEverySegmentLexed(t *testing.T) {
	for _, command := range readOnlySamples {
		segments, ok := Split(command)
		if !ok {
			t.Fatalf("Split(%q) not ok while Analyze says read-only", command)
		}
		for _, segment := range segments {
			if _, ok := Lex(segment, "linux"); !ok {
				t.Errorf("Analyze(%q) = read-only but segment %q did not lex", command, segment)
			}
		}
	}
}
