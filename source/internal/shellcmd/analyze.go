// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package shellcmd

import "strings"

// Verdict is what the analyzer could establish about a command line. Only
// VerdictReadOnly may ever be used to run a command without asking the user;
// the other two both mean "ask".
type Verdict uint8

const (
	// CannotProve means the analysis did not complete — an unknown command, an
	// expansion, a redirection, a construct this package declines to model.
	// It is the normal answer for anything unusual, not an error.
	CannotProve Verdict = iota
	// NotReadOnly means some part of the line is known to change something.
	NotReadOnly
	// ReadOnly means every part of the line was proved non-mutating.
	ReadOnly
)

func (v Verdict) String() string {
	switch v {
	case ReadOnly:
		return "read-only"
	case NotReadOnly:
		return "not-read-only"
	default:
		return "cannot-prove"
	}
}

// Analyze classifies a whole command line. It is a pure function of its
// arguments: no filesystem, no environment, no process. goos is the target
// platform ("windows" enables cmd.exe's metacharacters, everything else is
// treated as POSIX).
func Analyze(command, goos string) Verdict {
	segments, ok := Split(command)
	if !ok {
		return CannotProve
	}
	unknown := false
	for _, segment := range segments {
		argv, ok := Lex(segment, goos)
		if !ok {
			unknown = true
			continue
		}
		switch classify(argv, goos) {
		case NotReadOnly:
			// A provably mutating segment settles the whole line, and says so
			// more usefully than "unknown" would.
			return NotReadOnly
		case CannotProve:
			unknown = true
		}
	}
	if unknown {
		return CannotProve
	}
	return ReadOnly
}

// classify strips the wrappers that do not change what a command does, then
// classifies what is left.
func classify(argv []string, goos string) Verdict {
	name := commandName(argv[0])
	switch name {
	case "timeout":
		i := 1
		for i < len(argv) {
			switch argv[i] {
			case "-k", "-s", "--kill-after", "--signal":
				i += 2 // flag plus its argument
				continue
			}
			if strings.HasPrefix(argv[i], "-") {
				i++
				continue
			}
			break
		}
		if i >= len(argv) || !isDuration(argv[i]) {
			return CannotProve
		}
		i++
		if i >= len(argv) {
			return CannotProve // `timeout 5` runs nothing today
		}
		return classify(argv[i:], goos)
	case "nice":
		i := 1
		for i < len(argv) && strings.HasPrefix(argv[i], "-") {
			if argv[i] == "-n" {
				i++
			}
			i++
		}
		if i >= len(argv) {
			return CannotProve
		}
		return classify(argv[i:], goos)
	case "env":
		i := 1
		for i < len(argv) {
			switch {
			case argv[i] == "-u" || argv[i] == "--unset":
				i += 2
				continue
			case strings.HasPrefix(argv[i], "-"), isAssignment(argv[i]):
				i++
				continue
			}
			break
		}
		if i >= len(argv) {
			return ReadOnly // bare `env` prints the environment
		}
		return classify(argv[i:], goos)
	}
	return classifyCommand(name, argv[1:])
}

// readonlyCommands change nothing, whatever arguments they are given. The list
// is curated rather than inferred: adding a name to it is a deliberate claim
// that the command has no mutating mode at all.
var readonlyCommands = map[string]bool{
	"ls": true, "dir": true, "cat": true, "type": true, "findstr": true,
	"head": true, "tail": true, "wc": true, "file": true, "stat": true,
	"pwd": true, "whoami": true, "id": true, "echo": true, "printf": true,
	"uname": true, "which": true, "where": true, "true": true, "false": true,
	"test": true, "cd": true, "grep": true, "egrep": true, "fgrep": true,
	"rg": true, "printenv": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true, "du": true, "df": true, "cut": true,
	"tr": true, "seq": true, "diff": true, "comm": true, "expr": true,
	"sleep": true, "date": true,
}

// deniedArgs lists flags that give an otherwise read-only command a mutating
// mode. `date -s` sets the system clock; the command is in the table because it
// is read-only without this flag.
var deniedArgs = map[string][]string{
	"date": {"-s", "--set"},
}

// mutatingCommands are known to write or to run something. They are named so a
// verdict can say "this changes something" rather than the weaker "unknown".
var mutatingCommands = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true, "mv": true, "cp": true,
	"dd": true, "tee": true, "truncate": true, "chmod": true, "chown": true,
	"mkdir": true, "touch": true, "ln": true, "install": true, "kill": true,
	"pkill": true, "shutdown": true, "reboot": true,
}

func classifyCommand(name string, args []string) Verdict {
	if prefixes, ok := deniedArgs[name]; ok {
		for _, arg := range args {
			for _, denied := range prefixes {
				if strings.HasPrefix(arg, denied) {
					return NotReadOnly
				}
			}
		}
	}
	switch {
	case readonlyCommands[name]:
		return ReadOnly
	case mutatingCommands[name]:
		return NotReadOnly
	case name == "find":
		return checkFind(args)
	case name == "sed":
		return checkSed(args)
	case name == "git":
		return checkGit(args)
	case name == "go":
		return checkGo(args)
	}
	return CannotProve
}

// checkFind allows find only in the forms that report. Every action that runs a
// command or writes a file is named here.
func checkFind(args []string) Verdict {
	for _, arg := range args {
		switch arg {
		case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fls":
			return NotReadOnly
		}
		if strings.HasPrefix(arg, "-fprint") {
			return NotReadOnly
		}
	}
	return ReadOnly
}

// checkSed allows sed only when it cannot write: no in-place editing, no script
// file (whose contents are not visible here), and no w/r/e command in the
// script text.
func checkSed(args []string) Verdict {
	var scripts []string
	explicit := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-f" || arg == "--file":
			return CannotProve // the script is in a file; unknown here
		case arg == "-e" || arg == "--expression":
			explicit = true
			if i+1 < len(args) {
				i++
				scripts = append(scripts, args[i])
			}
		case strings.HasPrefix(arg, "--"):
			if strings.HasPrefix(arg, "--in-place") {
				return NotReadOnly
			}
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			// Short flag cluster: -i and -ni both edit in place.
			if strings.ContainsRune(arg[1:], 'i') {
				return NotReadOnly
			}
		default:
			// Without -e the first positional is the script and any further
			// positionals are input files; with -e every positional is a file.
			if !explicit && len(scripts) == 0 {
				scripts = append(scripts, arg)
			}
		}
	}
	for _, script := range scripts {
		if v := checkSedScript(script); v != ReadOnly {
			return v
		}
	}
	return ReadOnly
}

// checkSedScript inspects the commands in one sed script. The letters that make
// a command write — w (write file), r (read file into output), e (execute) —
// are rejected as a command letter, and as an s/y flag after the closing
// delimiter. Anything it cannot read is unknown, never read-only.
func checkSedScript(script string) Verdict {
	for _, cmd := range strings.FieldsFunc(script, func(r rune) bool {
		return r == ';' || r == '\n'
	}) {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		switch cmd[0] {
		case 'w', 'W', 'r', 'R', 'e':
			return NotReadOnly
		case 's', 'y':
			if len(cmd) < 2 {
				return CannotProve
			}
			delim := cmd[1]
			rest := cmd[2:]
			for n := 0; n < 2; n++ {
				idx := strings.IndexByte(rest, delim)
				if idx < 0 {
					return CannotProve // not a shape this package reads
				}
				rest = rest[idx+1:]
			}
			if strings.ContainsAny(rest, "wWrRe") {
				return NotReadOnly
			}
		}
	}
	return ReadOnly
}

// checkGit is subcommand-aware: reading history and status is read-only,
// touching objects or the worktree is not, and an unrecognised subcommand is
// unknown. It also refuses --output, which turns any of the reading
// subcommands into a file writer.
func checkGit(args []string) Verdict {
	if len(args) == 0 {
		return CannotProve
	}
	if args[0] == "--version" || args[0] == "-v" {
		return ReadOnly
	}
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree",
			arg == "--namespace" || arg == "--exec-path":
			i += 2
			continue
		case strings.HasPrefix(arg, "--git-dir="), strings.HasPrefix(arg, "--work-tree="),
			strings.HasPrefix(arg, "-c"), strings.HasPrefix(arg, "--namespace="),
			strings.HasPrefix(arg, "--exec-path="):
			i++
			continue
		case arg == "--no-pager" || arg == "--bare" || arg == "--no-optional-locks",
			arg == "-p" || arg == "--paginate" || arg == "--literal-pathspecs":
			i++
			continue
		case strings.HasPrefix(arg, "-"):
			return CannotProve // an unrecognised global flag
		}
		break
	}
	if i >= len(args) {
		return CannotProve
	}
	sub, rest := args[i], args[i+1:]
	for _, arg := range rest {
		if strings.HasPrefix(arg, "--output") || arg == "-o" {
			return NotReadOnly
		}
	}
	switch sub {
	case "status", "log", "diff", "show", "rev-parse", "ls-files", "blame",
		"describe", "help", "shortlog", "cat-file", "ls-tree", "show-ref",
		"for-each-ref", "name-rev", "whatchanged", "grep", "count-objects",
		"verify-pack", "check-ignore", "check-attr", "diff-tree", "diff-files",
		"merge-base", "symbolic-ref", "var", "version":
		return ReadOnly
	case "branch":
		// `git branch` lists; anything that names a branch to change is not a
		// listing form we can recognise as read-only.
		for _, arg := range rest {
			switch arg {
			case "-a", "-r", "-v", "-vv", "--list", "--all", "--remotes",
				"--verbose", "--contains", "--merged", "--no-merged",
				"--show-current":
				continue
			}
			if strings.HasPrefix(arg, "--format=") || strings.HasPrefix(arg, "-v") {
				continue
			}
			return NotReadOnly
		}
		return ReadOnly
	case "add", "am", "apply", "archive", "checkout", "cherry-pick", "clean",
		"clone", "commit", "config", "fetch", "filter-branch", "gc", "init",
		"merge", "mv", "pull", "push", "rebase", "remote", "reset", "restore",
		"revert", "rm", "stash", "submodule", "switch", "tag", "update-ref",
		"worktree", "notes", "sparse-checkout", "lfs":
		return NotReadOnly
	}
	return CannotProve
}

// checkGo classifies the standard tool subcommands. Anything that compiles,
// downloads, or rewrites source is not read-only.
func checkGo(args []string) Verdict {
	if len(args) == 0 {
		return CannotProve
	}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i >= len(args) {
		return CannotProve
	}
	sub := args[i]
	switch sub {
	case "version", "doc", "list":
		return ReadOnly
	case "env":
		for _, arg := range args[i+1:] {
			if arg == "-w" || arg == "-u" {
				return NotReadOnly // env -w writes the go env file
			}
		}
		return ReadOnly
	case "build", "test", "run", "install", "fmt", "generate", "get", "mod",
		"clean", "tool", "work", "telemetry", "fix", "vet":
		return NotReadOnly
	}
	return CannotProve
}

func commandName(arg string) string {
	name := arg
	if idx := strings.LastIndexAny(name, `/\`); idx >= 0 {
		name = name[idx+1:]
	}
	if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		name = name[:len(name)-4]
	}
	return name
}

// isDuration reports whether arg looks like a timeout(1) duration.
func isDuration(arg string) bool {
	if arg == "" {
		return false
	}
	digits := 0
	for i := 0; i < len(arg); i++ {
		c := arg[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
		case c == 's' || c == 'm' || c == 'h' || c == 'd':
			if i != len(arg)-1 {
				return false
			}
		default:
			return false
		}
	}
	return digits > 0
}

// isAssignment reports whether arg is a NAME=VALUE word, as env accepts.
func isAssignment(arg string) bool {
	eq := strings.IndexByte(arg, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := arg[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
