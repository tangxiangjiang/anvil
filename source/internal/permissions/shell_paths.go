// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package permissions

import (
	"strings"
)

const (
	maxShellCommandBytes = 64 << 10
	maxShellCommands     = 256
	maxShellTokens       = 4096
	maxShellDepth        = 8
)

// PathKnowledge describes whether a shell command's lexical path-candidate set
// is complete. Indeterminate is fail-closed for allow rules.
type PathKnowledge string

const (
	PathsKnown         PathKnowledge = "known"
	PathsIndeterminate PathKnowledge = "indeterminate"
)

// Stable shell path-analysis problem categories.
const (
	PathProblemUnsupportedSyntax = "unsupported_syntax"
	PathProblemDynamicPath       = "dynamic_path"
	PathProblemUnclosedQuote     = "unclosed_quote"
	PathProblemLimitExceeded     = "limit_exceeded"
	PathProblemUnknownDialect    = "unknown_dialect"
)

// ShellPathAnalysis is the pure lexical result for one command line.
type ShellPathAnalysis struct {
	Paths   []string
	State   PathKnowledge
	Problem string
}

type shellDialect uint8

const (
	dialectPOSIX shellDialect = iota + 1
	dialectCMD
	dialectPowerShell
)

type shellWord struct {
	text     string
	dynamic  bool
	redirect bool
}

type shellBudget struct {
	commands int
	tokens   int
}

// AnalyzeShellPaths extracts conservative path candidates without filesystem,
// environment, or process access. Syntax whose path values require evaluation
// is indeterminate rather than guessed.
func AnalyzeShellPaths(command, goos string) (result ShellPathAnalysis) {
	defer func() {
		if recover() != nil {
			result = indeterminate(nil, PathProblemUnsupportedSyntax)
		}
	}()
	if len(command) > maxShellCommandBytes {
		return indeterminate(nil, PathProblemLimitExceeded)
	}
	dialect, ok := dialectFor(goos)
	if !ok {
		return indeterminate(nil, PathProblemUnknownDialect)
	}
	return analyzeShellPaths(command, dialect, &shellBudget{}, 0)
}

func dialectFor(goos string) (shellDialect, bool) {
	switch goos {
	case "windows":
		return dialectCMD, true
	case "aix", "android", "darwin", "dragonfly", "freebsd", "illumos",
		"ios", "linux", "netbsd", "openbsd", "solaris":
		return dialectPOSIX, true
	default:
		return 0, false
	}
}

func analyzeShellPaths(command string, dialect shellDialect, budget *shellBudget, depth int) ShellPathAnalysis {
	if depth > maxShellDepth || len(command) > maxShellCommandBytes {
		return indeterminate(nil, PathProblemLimitExceeded)
	}
	commands, problem := lexShellCommands(command, dialect, budget)
	if problem != "" {
		return indeterminate(nil, problem)
	}

	var paths []string
	for _, words := range commands {
		found, problem := pathsFromCommand(words, dialect, budget, depth)
		paths = append(paths, found...)
		if problem != "" {
			return indeterminate(uniquePaths(paths), problem)
		}
	}
	return ShellPathAnalysis{Paths: uniquePaths(paths), State: PathsKnown}
}

func lexShellCommands(command string, dialect shellDialect, budget *shellBudget) ([][]shellWord, string) {
	var (
		commands       [][]shellWord
		words          []shellWord
		cur            strings.Builder
		started        bool
		dynamic        bool
		redirectTarget bool
		quote          byte
	)

	resetWord := func() {
		cur.Reset()
		started = false
		dynamic = false
	}
	flushWord := func() string {
		if !started {
			return ""
		}
		budget.tokens++
		if budget.tokens > maxShellTokens {
			return PathProblemLimitExceeded
		}
		words = append(words, shellWord{
			text:     cur.String(),
			dynamic:  dynamic,
			redirect: redirectTarget,
		})
		redirectTarget = false
		resetWord()
		return ""
	}
	flushCommand := func() string {
		if problem := flushWord(); problem != "" {
			return problem
		}
		if redirectTarget {
			return PathProblemUnsupportedSyntax
		}
		if len(words) == 0 {
			return ""
		}
		budget.commands++
		if budget.commands > maxShellCommands {
			return PathProblemLimitExceeded
		}
		commands = append(commands, words)
		words = nil
		return ""
	}

	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				started = true
				continue
			}
			if (dialect == dialectPOSIX || dialect == dialectPowerShell) && quote == '\'' {
				cur.WriteByte(c)
				started = true
				continue
			}
			if dialect == dialectPOSIX && c == '\\' {
				if i+1 >= len(command) {
					return nil, PathProblemUnclosedQuote
				}
				dynamic = true
				cur.WriteByte(command[i+1])
				i++
				started = true
				continue
			}
			if dialect == dialectPowerShell && c == '`' {
				if i+1 >= len(command) {
					return nil, PathProblemUnclosedQuote
				}
				i++
				cur.WriteByte(command[i])
				started = true
				continue
			}
			if isDynamicByte(c, dialect) {
				dynamic = true
			}
			cur.WriteByte(c)
			started = true
			continue
		}

		switch {
		case (dialect == dialectPOSIX || dialect == dialectPowerShell) && (c == '\'' || c == '"'):
			quote = c
			started = true
		case dialect == dialectCMD && c == '"':
			quote = c
			started = true
		case c == ' ' || c == '\t' || c == '\r':
			if problem := flushWord(); problem != "" {
				return nil, problem
			}
		case c == '\n' || c == ';' && dialect != dialectCMD:
			if problem := flushCommand(); problem != "" {
				return nil, problem
			}
		case c == '&' || c == '|':
			if problem := flushCommand(); problem != "" {
				return nil, problem
			}
			if i+1 < len(command) && command[i+1] == c {
				i++
			}
		case c == '<' || c == '>':
			if dialect != dialectCMD && i+1 < len(command) && command[i+1] == '(' {
				return nil, PathProblemDynamicPath
			}
			if c == '<' && i+1 < len(command) && command[i+1] == '<' {
				return nil, PathProblemUnsupportedSyntax
			}
			if started && allDigits(cur.String()) {
				resetWord() // a redirection file descriptor, e.g. 2>log
			} else if problem := flushWord(); problem != "" {
				return nil, problem
			}
			if redirectTarget {
				return nil, PathProblemUnsupportedSyntax
			}
			redirectTarget = true
			if i+1 < len(command) && command[i+1] == '>' {
				i++
			}
		case dialect == dialectPOSIX && c == '\\':
			if i+1 >= len(command) {
				return nil, PathProblemUnsupportedSyntax
			}
			i++
			cur.WriteByte(command[i])
			started = true
		case dialect == dialectCMD && c == '^':
			if i+1 >= len(command) {
				return nil, PathProblemUnsupportedSyntax
			}
			i++
			cur.WriteByte(command[i])
			started = true
		case dialect == dialectPowerShell && c == '`':
			if i+1 >= len(command) {
				return nil, PathProblemUnsupportedSyntax
			}
			i++
			cur.WriteByte(command[i])
			started = true
		case c == '(' || c == ')' || c == '{' || c == '}':
			if dynamic {
				return nil, PathProblemDynamicPath
			}
			return nil, PathProblemUnsupportedSyntax
		default:
			if isDynamicByte(c, dialect) {
				dynamic = true
			}
			cur.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, PathProblemUnclosedQuote
	}
	if problem := flushCommand(); problem != "" {
		return nil, problem
	}
	if len(commands) == 0 {
		return nil, PathProblemUnsupportedSyntax
	}
	return commands, ""
}

func pathsFromCommand(words []shellWord, dialect shellDialect, budget *shellBudget, depth int) ([]string, string) {
	for _, word := range words {
		if word.dynamic {
			return nil, PathProblemDynamicPath
		}
	}
	commandIndex := -1
	for i, word := range words {
		if !isAssignmentWord(word.text) {
			commandIndex = i
			break
		}
	}
	if commandIndex < 0 {
		return nil, ""
	}
	if dialect != dialectCMD && isShellControlWord(strings.ToLower(words[commandIndex].text)) {
		return nil, PathProblemUnsupportedSyntax
	}

	nestedIndex, problem := unwrapCommandIndex(words, commandIndex)
	if problem != "" {
		return nil, problem
	}
	if nested, nestedDialect, trailing, ok, problem := nestedCommand(words[nestedIndex:], dialect); problem != "" {
		return nil, problem
	} else if ok {
		result := analyzeShellPaths(nested, nestedDialect, budget, depth+1)
		var prefix []string
		if nestedIndex > commandIndex {
			prefix = operandCandidates(words[commandIndex+1:nestedIndex], dialect)
		}
		paths := append(prefix, result.Paths...)
		paths = append(paths, operandCandidates(trailing, dialect)...)
		return paths, result.Problem
	}

	return operandCandidates(words[commandIndex+1:], dialect), ""
}

func operandCandidates(words []shellWord, dialect shellDialect) []string {
	var paths []string
	for _, word := range words {
		if word.redirect {
			paths = appendCandidate(paths, word.text)
			continue
		}
		if strings.HasPrefix(word.text, "--") {
			if eq := strings.IndexByte(word.text, '='); eq >= 0 {
				paths = appendCandidate(paths, word.text[eq+1:])
			}
			continue
		}
		if strings.HasPrefix(word.text, "-") || dialect == dialectCMD && strings.HasPrefix(word.text, "/") {
			continue
		}
		paths = appendCandidate(paths, word.text)
	}
	return paths
}

func unwrapCommandIndex(words []shellWord, start int) (int, string) {
	index := start
	for index < len(words) {
		name := strings.ToLower(words[index].text)
		if slash := strings.LastIndexAny(name, `/\`); slash >= 0 {
			name = name[slash+1:]
		}
		name = strings.TrimSuffix(name, ".exe")
		switch name {
		case "env":
			index++
			for index < len(words) {
				switch {
				case words[index].text == "--":
					index++
					goto nextWrapper
				case words[index].text == "-u" || words[index].text == "--unset" ||
					words[index].text == "-C" || words[index].text == "--chdir" ||
					words[index].text == "-S" || words[index].text == "--split-string" ||
					words[index].text == "--argv0":
					index += 2
				case words[index].text == "-i" || words[index].text == "--ignore-environment" ||
					words[index].text == "-0" || words[index].text == "--null" ||
					words[index].text == "--debug", isAssignmentWord(words[index].text),
					strings.HasPrefix(words[index].text, "--unset="),
					strings.HasPrefix(words[index].text, "--chdir="),
					strings.HasPrefix(words[index].text, "--split-string="),
					strings.HasPrefix(words[index].text, "--argv0="):
					index++
				case strings.HasPrefix(words[index].text, "-"):
					return 0, PathProblemUnsupportedSyntax
				default:
					goto nextWrapper
				}
			}
		case "command":
			index++
			for index < len(words) {
				switch words[index].text {
				case "--", "-p":
					index++
				default:
					if strings.HasPrefix(words[index].text, "-") {
						return 0, PathProblemUnsupportedSyntax
					}
					goto nextWrapper
				}
			}
		case "exec":
			index++
			for index < len(words) {
				switch words[index].text {
				case "--", "-c", "-l":
					index++
				case "-a":
					index += 2
				default:
					if strings.HasPrefix(words[index].text, "-") {
						return 0, PathProblemUnsupportedSyntax
					}
					goto nextWrapper
				}
			}
		case "sudo":
			index++
			if index < len(words) && strings.HasPrefix(words[index].text, "-") {
				return 0, PathProblemUnsupportedSyntax
			}
		case "nice":
			index++
			for index < len(words) {
				switch {
				case words[index].text == "--":
					index++
					goto nextWrapper
				case words[index].text == "-n" || words[index].text == "--adjustment":
					index += 2
				case strings.HasPrefix(words[index].text, "-n") && len(words[index].text) > 2,
					strings.HasPrefix(words[index].text, "--adjustment="):
					index++
				case strings.HasPrefix(words[index].text, "-"):
					return 0, PathProblemUnsupportedSyntax
				default:
					goto nextWrapper
				}
			}
		case "timeout":
			index++
			for index < len(words) {
				switch {
				case words[index].text == "--":
					index++
					goto timeoutDuration
				case words[index].text == "-k" || words[index].text == "-s" ||
					words[index].text == "--kill-after" || words[index].text == "--signal":
					index += 2
				case words[index].text == "--preserve-status" || words[index].text == "--foreground" ||
					words[index].text == "--verbose",
					strings.HasPrefix(words[index].text, "--kill-after="),
					strings.HasPrefix(words[index].text, "--signal="):
					index++
				case strings.HasPrefix(words[index].text, "-"):
					return 0, PathProblemUnsupportedSyntax
				default:
					goto timeoutDuration
				}
			}
		timeoutDuration:
			if index < len(words) {
				index++
			}
		default:
			return index, ""
		}
	nextWrapper:
		if index >= len(words) {
			return 0, PathProblemUnsupportedSyntax
		}
	}
	return 0, PathProblemUnsupportedSyntax
}

func nestedCommand(words []shellWord, parent shellDialect) (string, shellDialect, []shellWord, bool, string) {
	name := strings.ToLower(words[0].text)
	if slash := strings.LastIndexAny(name, `/\`); slash >= 0 {
		name = name[slash+1:]
	}
	name = strings.TrimSuffix(name, ".exe")

	var (
		flag    string
		dialect shellDialect
	)
	switch name {
	case "sh", "bash", "dash", "zsh", "ksh":
		flag, dialect = "-c", dialectPOSIX
	case "cmd":
		for i := 1; i < len(words); i++ {
			if strings.EqualFold(words[i].text, "/c") || strings.EqualFold(words[i].text, "/k") {
				if i+1 >= len(words) {
					return "", 0, nil, false, PathProblemUnsupportedSyntax
				}
				if i+2 != len(words) {
					return "", 0, nil, false, PathProblemUnsupportedSyntax
				}
				return words[i+1].text, dialectCMD, nil, true, ""
			}
		}
		return "", parent, nil, false, ""
	case "powershell", "pwsh":
		for i := 1; i < len(words); i++ {
			if strings.EqualFold(words[i].text, "-command") || strings.EqualFold(words[i].text, "-c") {
				if i+1 >= len(words) {
					return "", 0, nil, false, PathProblemUnsupportedSyntax
				}
				if words[i+1].dynamic {
					return "", 0, nil, false, PathProblemDynamicPath
				}
				if i+2 != len(words) {
					return "", 0, nil, false, PathProblemUnsupportedSyntax
				}
				return words[i+1].text, dialectPowerShell, nil, true, ""
			}
			if strings.EqualFold(words[i].text, "-encodedcommand") || strings.EqualFold(words[i].text, "-enc") {
				return "", 0, nil, false, PathProblemUnsupportedSyntax
			}
		}
		return "", parent, nil, false, ""
	case "fish", "python", "python3", "node", "ruby", "perl":
		for i := 1; i < len(words); i++ {
			if words[i].text == "-c" || words[i].text == "-e" {
				return "", 0, nil, false, PathProblemUnsupportedSyntax
			}
		}
		return "", parent, nil, false, ""
	default:
		return "", parent, nil, false, ""
	}
	for i := 1; i < len(words); i++ {
		if !strings.EqualFold(words[i].text, flag) {
			continue
		}
		if i+1 >= len(words) {
			return "", 0, nil, false, PathProblemUnsupportedSyntax
		}
		if words[i+1].dynamic {
			return "", 0, nil, false, PathProblemDynamicPath
		}
		return words[i+1].text, dialect, words[i+2:], true, ""
	}
	return "", parent, nil, false, ""
}

func isDynamicByte(c byte, dialect shellDialect) bool {
	if dialect == dialectCMD {
		return c == '%' || c == '!' || c == '*' || c == '?'
	}
	if dialect == dialectPowerShell {
		return c == '$' || c == '*' || c == '?' || c == '[' || c == ']'
	}
	return c == '$' || c == '`' || c == '*' || c == '?' || c == '[' || c == ']'
}

func isShellControlWord(s string) bool {
	switch s {
	case "if", "then", "else", "elif", "fi", "for", "while", "until", "do",
		"done", "case", "esac", "function", "select", "coproc":
		return true
	default:
		return false
	}
}

func isAssignmentWord(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := s[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func appendCandidate(paths []string, candidate string) []string {
	if candidate == "" || candidate == "-" {
		return paths
	}
	return append(paths, candidate)
}

func uniquePaths(paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func indeterminate(paths []string, problem string) ShellPathAnalysis {
	return ShellPathAnalysis{
		Paths:   paths,
		State:   PathsIndeterminate,
		Problem: problem,
	}
}
