// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/shellcmd"
	"github.com/anvil-dev/anvil/internal/tools"
)

// This file is the SDK's side of the workspace rule file: reading it, adding to
// it, evaluating a set of rules against a call, and working out which "always
// allow" a person could be offered for the call in front of them.
//
// It exists because the editor host may import this package and nothing else
// (internal/editorhost/independence_test.go), and the host has to answer three
// questions the rule file owns: what does it say, may I append to it, and would
// this new rule really decide the call I am showing somebody. Answering them
// with a second implementation is how an explanation starts disagreeing with the
// decision it explains, so the host asks here instead.

// PermissionRulesPath is the workspace rule file a user commits and an editor
// shows: <cwd>/.anvil/permissions.json. It is absolute, because the caller is a
// text editor or a diagnostic.
func PermissionRulesPath(cwd string) string {
	return filepath.Join(cwd, ".anvil", permissions.FileName)
}

// LoadPermissionRules reads the workspace rule file as written.
//
// A missing file is not an error and not an empty policy: it returns (nil, nil),
// the same "no rules" a session carries for a workspace that has none. A file
// that exists but cannot be read as a rule set returns a *PermissionRuleError
// naming the file and, when the parser knows it, the place — silently ignoring a
// policy somebody wrote is worse than refusing to read it.
func LoadPermissionRules(cwd string) ([]PermissionRule, error) {
	file, err := LoadPermissionRuleFile(cwd)
	if err != nil || file == nil {
		return nil, err
	}
	return file.Rules, nil
}

// PermissionRuleFile is the workspace rule file read whole: what it declares about
// new sessions, and the rules that decide calls.
type PermissionRuleFile struct {
	// Path is absolute, the same path PermissionRulesPath returns.
	Path string
	// DefaultMode is the mode the file declares for new sessions, empty when it
	// declares none. It is the declaration as written: whether the host honours it
	// is a separate question, and the answer belongs where the modes do.
	DefaultMode string
	// Rules are the file's rules, in the order that decides.
	Rules []PermissionRule
}

// LoadPermissionRuleFile reads the workspace rule file whole.
//
// It is the reader a host uses when it needs more of the file than the rules —
// which mode the workspace starts sessions in, say. One parser, so that the file's
// declaration and its rules can never be read two ways by two callers: a second
// parser of the same bytes is how two answers to "what does this file say" start
// disagreeing.
//
// A missing file returns (nil, nil) — the same "this workspace has written nothing
// down" the rule list reports — and a file that will not read returns a
// *PermissionRuleError with the place when there is one.
func LoadPermissionRuleFile(cwd string) (*PermissionRuleFile, error) {
	p := PermissionRulesPath(cwd)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &PermissionRuleError{Path: p, Message: err.Error()}
	}
	set, err := permissions.Parse(data)
	if err != nil {
		return nil, ruleFileError(p, err)
	}
	return &PermissionRuleFile{
		Path:        p,
		DefaultMode: set.DefaultMode(),
		Rules:       permissionRuleList(set.Rules()),
	}, nil
}

// AddPermissionRule appends one rule to the workspace rule file, creating the
// file — and .anvil/ — when they do not exist, and returns the file's rules as
// they now stand.
//
// Two things it will not do. It will not write over a file it cannot read: a
// malformed rule file is reported, not replaced, because discarding policy
// somebody wrote is the failure this design is arranged around. And it will not
// write a rule the reader would refuse: the new text is parsed by the same
// parser the SDK uses before anything reaches the disk, so a rule added here is
// a rule the next session accepts.
//
// The file's other contents are carried through, because the write is whole: the
// declaration of a starting mode is a field of the same file, and dropping it
// would make the first click on "always allow" quietly change how every session
// in this workspace opens.
//
// The write is a temporary file in the same directory followed by a rename, so
// an interruption leaves the previous policy intact rather than half of a new
// one.
func AddPermissionRule(cwd string, rule PermissionRule) ([]PermissionRule, error) {
	existing, err := LoadPermissionRuleFile(cwd)
	if err != nil {
		return nil, err
	}
	rules := make([]permissions.Rule, 0, 1)
	var declared *string
	if existing != nil {
		rules = append(rules, permissionRules(existing.Rules)...)
		if existing.DefaultMode != "" {
			mode := existing.DefaultMode
			declared = &mode
		}
	}
	rules = append(rules, permissions.Rule{
		Effect:  rule.Effect,
		Tool:    rule.Tool,
		Pattern: rule.Pattern,
		Reason:  rule.Reason,
	})
	data, err := json.MarshalIndent(permissions.File{
		Version:     permissions.Version,
		DefaultMode: declared,
		Rules:       rules,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	p := PermissionRulesPath(cwd)
	set, err := permissions.Parse(data)
	if err != nil {
		return nil, ruleFileError(p, err)
	}
	if err := writeRuleFile(p, data); err != nil {
		return nil, err
	}
	return permissionRuleList(set.Rules()), nil
}

// SavePermissionRuleFile replaces the workspace rule file with exactly these
// rules and this declaration (0.41+).
//
// It is the writer behind a rules manager, and its contract is replacement: the
// caller has the list it showed a person, and the file becomes that list. It
// does not read the previous file — a caller that cannot read it must not
// overwrite it, and that decision belongs where the reading happened.
//
// The text is compiled by the same parser a session uses before anything
// reaches the disk, so a set this function accepts is a set the next session
// opens under. A malformed rule is reported with its position, like a rule
// appended through AddPermissionRule. defaultMode is passed through as written
// when non-empty; whether a word is a *mode* is the host's vocabulary, not this
// package's, so only its shape is checked here.
//
// Nothing else of the old file survives — there is nothing else to survive. The
// parser refuses unknown keys, so a file it reads holds only a version, this
// declaration and these rules.
func SavePermissionRuleFile(cwd string, rules []PermissionRule, defaultMode string) error {
	compiled := permissionRules(rules)
	if compiled == nil {
		compiled = []permissions.Rule{}
	}
	var declared *string
	if defaultMode != "" {
		mode := defaultMode
		declared = &mode
	}
	data, err := json.MarshalIndent(permissions.File{
		Version:     permissions.Version,
		DefaultMode: declared,
		Rules:       compiled,
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	p := PermissionRulesPath(cwd)
	if _, err := permissions.Parse(data); err != nil {
		return ruleFileError(p, err)
	}
	return writeRuleFile(p, data)
}

// EvaluatePermissionRules reports which of these rules decides a call, or nil
// when none does.
//
// It runs the same evaluation the SDK itself runs, against the same facts, so a
// client that explains a decision cannot drift from the decision. The call is
// given the way the registry receives it — a tool name and its arguments — and
// its facts are read from those arguments by the one function that reads them
// for a real decision.
func EvaluatePermissionRules(rules []PermissionRule, tool string, args json.RawMessage) *PermissionRuleMatch {
	set, err := permissions.New(permissionRules(rules))
	if err != nil {
		// The honest answer for a set that will not compile is "no rule decides
		// this": nobody can rely on a rule the evaluator refuses.
		return nil
	}
	decision := decisionFor(set, tools.CallFactsFor(tool, args))
	return permissionRuleMatch(&decision)
}

// DescribePermissionRules names every rule the way a decision quotes it: the
// file, the rule's position as a reader counts it, the rule as written, and its
// reason.
//
// The result is parallel to rules — entry i describes rules[i] — so a client
// listing a rule file shows the same words a denial and an approval card use.
// Nothing comes back for a set that will not compile: a rule the evaluator
// refuses is a rule nobody can rely on, and describing it would suggest
// otherwise.
func DescribePermissionRules(rules []PermissionRule) []string {
	set, err := permissions.New(permissionRules(rules))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rules))
	for i := range rules {
		out = append(out, set.Describe(i))
	}
	return out
}

// Permission option kinds, mirroring PermissionRuleOption.Kind.
const (
	// PermissionOptionExact is this command line, token for token.
	PermissionOptionExact = "exact"
	// PermissionOptionPrefix is commands starting with these tokens.
	PermissionOptionPrefix = "prefix"
	// PermissionOptionReadonly is any command the analyzer proves non-mutating.
	PermissionOptionReadonly = "readonly"
	// PermissionOptionPath is this one file.
	PermissionOptionPath = "path"
	// PermissionOptionTool is every call to this tool, whatever it is asked to do.
	PermissionOptionTool = "tool"
)

// PermissionRuleOption is one "always allow" a client can offer for a call: the
// rule to write, and what that rule is a claim about.
//
// Kind is what a client labels the button with, so the label never has to be
// derived from the pattern:
//
//	"exact"     this command line, exactly
//	"prefix"    commands starting with these tokens
//	"readonly"  any command the analyzer proves non-mutating
//	"path"      this file
//	"tool"      every call to this tool
//
// Every option returned has been evaluated against the call it was built for.
// An option that would not decide the call in front of the user is not offered,
// because a button that writes a rule which does nothing is worse than no
// button: it teaches the user that rules do not work.
type PermissionRuleOption struct {
	Kind string
	Rule PermissionRule
}

// PermissionRuleOptions works out which "always allow" choices a call can
// honestly be offered.
//
// The candidates are the shapes a rule can take, and each one is kept only if
// evaluating it against this very call matches. That is what makes a compound
// command (`git status && rm -rf /`) offer nothing exact or prefix-shaped: no
// single-command rule covers it, and the analyzer will not prove a line with two
// commands read-only either.
func PermissionRuleOptions(tool string, args json.RawMessage) []PermissionRuleOption {
	facts := tools.CallFactsFor(tool, args)
	var out []PermissionRuleOption
	offer := func(kind string, r PermissionRule) {
		if r.Effect == "" {
			r.Effect = "allow"
		}
		set, err := permissions.New(permissionRules([]PermissionRule{r}))
		if err != nil || !decisionFor(set, facts).Matched {
			return
		}
		out = append(out, PermissionRuleOption{Kind: kind, Rule: r})
	}

	switch {
	case facts.Command != "":
		offer(PermissionOptionExact, PermissionRule{Tool: tool, Pattern: facts.Command})
		if prefix := commandPrefix(facts.Command); prefix != "" {
			offer(PermissionOptionPrefix, PermissionRule{Tool: tool, Pattern: prefix + ":*"})
		}
		offer(PermissionOptionReadonly, PermissionRule{Tool: tool, Pattern: permissions.ReadonlyPattern})
	case len(facts.Paths) == 1:
		// One path, one rule. A call touching several files is offered nothing
		// path-shaped: a single glob covering all of them would have to be
		// broader than any of the files the user was shown.
		offer(PermissionOptionPath, PermissionRule{Tool: tool, Pattern: facts.Paths[0]})
	case len(facts.Paths) == 0:
		// A tool whose calls carry neither a command nor a path — an MCP server's,
		// say — can only be allowed as a whole, and that is the one thing on
		// offer. The mutating built-ins are not offered it: "allow every shell
		// call" is the all-or-nothing memory this rule table exists to replace,
		// and a call of theirs that shows no command is one that could not be
		// described in the first place.
		if !tools.IsMutating(tool) {
			offer(PermissionOptionTool, PermissionRule{Tool: tool})
		}
	}
	return out
}

// maxPrefixTokens bounds a prefix rule at the tokens that name what runs rather
// than how: `git status -s` gives `git status`, `npm run build` gives
// `npm run build`, and a line whose second token is already an option gives just
// the command.
const maxPrefixTokens = 3

// commandPrefix is the leading run of tokens before the first option.
//
// It is built from lexed tokens rather than from the text, so a prefix rule can
// only ever be a prefix the matcher will recognise. A line that cannot be lexed
// — quoting, expansion, globs — has no prefix, and a line that is several
// commands has none either.
func commandPrefix(command string) string {
	segments, ok := shellcmd.Split(command)
	if !ok || len(segments) != 1 {
		return ""
	}
	argv, ok := shellcmd.Lex(segments[0], runtime.GOOS)
	if !ok {
		return ""
	}
	prefix := make([]string, 0, maxPrefixTokens)
	for _, tok := range argv {
		if strings.HasPrefix(tok, "-") || len(prefix) == maxPrefixTokens {
			break
		}
		prefix = append(prefix, tok)
	}
	return strings.Join(prefix, " ")
}

// decisionFor evaluates a compiled set against the facts read from a call.
func decisionFor(set *permissions.Set, facts permissions.CallFacts) permissions.Decision {
	if set == nil || set.Len() == 0 {
		return permissions.Decision{}
	}
	return set.Evaluate(facts, runtime.GOOS)
}

// permissionRuleList projects the internal rules onto the public type, keeping
// the order that decides.
func permissionRuleList(rules []permissions.Rule) []PermissionRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]PermissionRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, PermissionRule{Effect: r.Effect, Tool: r.Tool, Pattern: r.Pattern, Reason: r.Reason})
	}
	return out
}

// ruleFileError turns a parser error into one that names the file and the place,
// without saying the path twice.
func ruleFileError(path string, err error) *PermissionRuleError {
	line, col := permissions.Position(err)
	msg := err.Error()
	// Load prefixes its own path; this one is built from bytes the caller read,
	// so there is nothing to strip.
	return &PermissionRuleError{Path: path, Message: msg, Line: line, Column: col}
}

// writeRuleFile writes data to path atomically enough that a reader never sees a
// partial file: same directory, temporary name, rename over the target.
func writeRuleFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".permissions-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// The file is meant to be committed: a temporary file's 0600 would make a
	// policy only its writer can read.
	_ = os.Chmod(tmp, 0o644)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
