// Package procs identifies operating system processes and answers whether a
// pid is still the same process a session file described, using the
// process creation time as a fingerprint. A pid alone is never enough,
// since pids get reused: every check that could lead to ending a process
// first confirms the process it finds is the one it was looking for.
package procs

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Match is the result of comparing a process creation time against the raw
// start time value a session file recorded for it.
type Match int

const (
	// Mismatch means the two values describe different processes. A pid
	// carrying this result must never be treated as the process being
	// looked for.
	Mismatch Match = iota
	// Matches means the two values describe the same process.
	Matches
	// Unknown means the recorded start time could not be interpreted, so
	// identity could be neither confirmed nor denied. Code that might end
	// the process needs another signal before acting on Unknown.
	Unknown
)

// macLayout is the wall clock layout used when a session file records a
// process start time as rendered text instead of a number.
const macLayout = "Mon Jan _2 15:04:05 2006"

// filetimeEpochTicks is the number of 100 ns ticks between the Windows
// FILETIME epoch (1601-01-01) and the Unix epoch (1970-01-01).
const filetimeEpochTicks = 116444736000000000

// The unit a numeric start time is written in is decided by its
// magnitude, not by trying each unit in turn and keeping whichever happens
// to land near the creation time. Each range below is far away from the
// others for any date this program can be running on, so a value inside
// one of them is read in that unit and compared once; a value that does
// not match is then a real Mismatch rather than a value that gets another
// chance under a different unit.
const (
	// minFiletimeTicks is where the 100 ns tick counts since 1601 begin for
	// any plausible present-day date. FILETIME values passed the 1e17 mark
	// in 1918 and do not reach 1e18 until well past the year 4500.
	minFiletimeTicks = int64(1e17)
	// Unix milliseconds sit between 1e12 (2001) and 1e14 (5138).
	minUnixMs = int64(1e12)
	maxUnixMs = int64(1e14)
	// Unix seconds sit between 1e9 (2001) and 1e11 (5138).
	minUnixSec = int64(1e9)
	maxUnixSec = int64(1e11)
)

// The tolerances each unit is compared with. A Windows FILETIME and the
// creation time it is compared against are both read from the same clock
// at the same precision, so it is held to a tight window.
//
// Neither Unix form can be. Linux does not record a creation time at all:
// it reports the ticks since boot at which a process started, which a
// reader turns into a wall clock time by adding the boot time, and the
// boot time itself is only known to the second. Two readings of the same
// process taken by different programs can therefore land up to about a
// second apart, whether the value was written down in milliseconds or in
// seconds. Anything tighter would declare every session on such a machine
// a different process: an empty page and watches that never resume.
const (
	filetimeToleranceMs = int64(100)
	unixMsToleranceMs   = int64(1500)
	unixSecToleranceMs  = int64(1500)
	// A rendered wall clock time carries no zone and no sub-second part.
	renderedToleranceMs = int64(2000)
)

// MatchStart compares a process creation time, in Unix milliseconds,
// against procStart, a raw start time value recorded by a session file.
// procStart may be a Windows FILETIME, a Unix timestamp in milliseconds or
// in seconds, or text in macLayout. An empty value, an unparseable one, or
// a number too far outside every known range to name a date at all yields
// Unknown: identity could be neither confirmed nor denied. A number that
// does name a date and disagrees with the creation time is a Mismatch.
func MatchStart(createMs int64, procStart string) Match {
	s := strings.TrimSpace(procStart)
	if s == "" {
		return Unknown
	}

	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		switch {
		case v >= minFiletimeTicks:
			return withinTolerance((v-filetimeEpochTicks)/10000, createMs, filetimeToleranceMs)
		case v >= minUnixMs && v < maxUnixMs:
			return withinTolerance(v, createMs, unixMsToleranceMs)
		case v >= minUnixSec && v < maxUnixSec:
			return withinTolerance(v*1000, createMs, unixSecToleranceMs)
		default:
			return Unknown
		}
	}

	t, err := time.Parse(macLayout, s)
	if err != nil {
		return Unknown
	}
	if withinTolerance(t.UnixMilli(), createMs, renderedToleranceMs) == Matches {
		return Matches
	}
	if lt, err := time.ParseInLocation(macLayout, s, time.Local); err == nil {
		return withinTolerance(lt.UnixMilli(), createMs, renderedToleranceMs)
	}
	return Mismatch
}

// withinTolerance reports Matches when a and b differ by less than
// tolerance, Mismatch otherwise. Both arguments are in the same unit.
func withinTolerance(a, b, tolerance int64) Match {
	d := a - b
	if d < 0 {
		d = -d
	}
	if d < tolerance {
		return Matches
	}
	return Mismatch
}

// programIdentity is everything the Claude-program test below is allowed
// to look at: what the operating system calls the process, the executable
// it is running, and the first word of its command line. The rest of the
// command line is deliberately absent. Arguments name files a program was
// merely pointed at, and any program at all can be pointed at something
// under a Claude folder, so letting them decide would turn the last
// safeguard before ending an unidentified process into no safeguard at
// all.
type programIdentity struct {
	Name string
	Exe  string
	Argv []string
}

// nodeRunners are the interpreters Claude Code can be installed to run
// under, where the program being run is named by the first argument rather
// than by the executable.
var nodeRunners = map[string]bool{"node": true, "bun": true}

// claudeScriptMarker is the fragment of a path that identifies Claude
// Code's own entry script when it is run through an interpreter.
const claudeScriptMarker = "claude-code/cli"

// reSemverPrefix matches a path element that begins with a three part
// version number, the shape every Claude Code install uses for the folder
// holding one version of itself.
var reSemverPrefix = regexp.MustCompile(`^\d+\.\d+\.\d+`)

// looksLikeClaudeProgram reports whether a process is recognisably Claude
// Code itself. It is the last thing standing between an unconfirmed
// identity and a process being ended, so it asks about the program and
// nothing else: the program's own name, the executable behind it, or, for
// an install that runs through an interpreter, the script named as that
// interpreter's first argument.
//
// Recognising the program means one of two things. Either the executable
// is plainly called "claude", or its path passes through a folder laid out
// the way a Claude Code install is: a "claude" folder holding "versions"
// and a version number under it, a "claude-code" folder with a version
// number directly inside it, or the macOS application bundle.
func looksLikeClaudeProgram(id programIdentity) bool {
	if isClaudeProgramName(id.Name) || isClaudeProgramName(pathBase(id.Exe)) {
		return true
	}
	if len(id.Argv) > 0 && isClaudeProgramName(pathBase(id.Argv[0])) {
		return true
	}
	if isClaudeInstallPath(id.Exe) {
		return true
	}
	return runsClaudeScript(id.Argv)
}

// isClaudeProgramName reports whether a bare program name is Claude Code's
// own, ignoring case and a Windows executable suffix. It is an exact
// comparison rather than a substring test: a program merely containing the
// word would otherwise be enough to have it ended.
func isClaudeProgramName(name string) bool {
	n := strings.ToLower(name)
	n = strings.TrimSuffix(n, ".exe")
	return n == "claude"
}

// isClaudeInstallPath reports whether path runs through a folder laid out
// the way Claude Code installs itself.
func isClaudeInstallPath(path string) bool {
	parts := pathElements(path)
	for i, part := range parts {
		switch part {
		case "claude.app":
			return true
		case "claude":
			if i+2 < len(parts) && parts[i+1] == "versions" && reSemverPrefix.MatchString(parts[i+2]) {
				return true
			}
		case "claude-code":
			if i+1 < len(parts) && reSemverPrefix.MatchString(parts[i+1]) {
				return true
			}
		}
	}
	return false
}

// runsClaudeScript reports whether argv is an interpreter being pointed at
// Claude Code itself. Only the first argument is read, and only when the
// program running it is one of the interpreters Claude Code is installed
// to run under.
//
// Two shapes count. A package install names the entry script inside the
// package folder, and a global install made by a package manager names its
// own shim, a file called exactly "claude" that the interpreter is handed.
// Anything else the interpreter was pointed at, including a file that
// merely sits under a Claude folder, is not Claude Code.
func runsClaudeScript(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	runner := baseWithoutExtension(argv[0])
	if !nodeRunners[runner] {
		return false
	}
	script := strings.ToLower(strings.ReplaceAll(argv[1], `\`, "/"))
	if strings.Contains(script, claudeScriptMarker) {
		return true
	}
	return baseWithoutExtension(argv[1]) == "claude"
}

// baseWithoutExtension returns the lowercased last element of a path with
// any extension removed. A name that is nothing but an extension, such as
// ".claude", keeps it: the leading dot is what makes it a hidden name
// rather than a program called "claude".
func baseWithoutExtension(path string) string {
	base := strings.ToLower(pathBase(path))
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return base
}

// pathBase returns the last element of a path written with either
// separator, since a path read from a process can come from either kind of
// system.
func pathBase(path string) string {
	p := strings.ReplaceAll(path, `\`, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// pathElements splits a path written with either separator into its
// lowercased elements.
func pathElements(path string) []string {
	p := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	return strings.Split(p, "/")
}

// TreeStats summarizes a process and its descendants at one point in time.
type TreeStats struct {
	CPUPercent float64
	// CPUSeconds is the CPU time the processes in the tree have used so
	// far. A process that ends takes its share with it.
	CPUSeconds float64
	RSS        uint64
	Children   []string
	Uptime     time.Duration
	Processes  int
	PIDs       []int
}

// Procs answers identity and resource questions about operating system
// processes, and can end one once its identity has been confirmed.
type Procs interface {
	// Alive reports whether pid names a running process whose creation
	// time is consistent with procStart.
	Alive(pid int, procStart string) bool
	// Terminate ends the process tree rooted at pid. It never touches a
	// process whose identity is a definite mismatch, and it only acts on
	// an unconfirmed identity when the process looks like a Claude
	// process. It reports whether the root process is gone afterwards.
	Terminate(pid int, procStart string) bool
	// Tree reports resource usage for pid and everything descended from
	// it. The bool is false when pid does not name a running process.
	Tree(pid int) (TreeStats, bool)
	// CreateTime reports pid's creation time in Unix milliseconds.
	CreateTime(pid int) (int64, bool)
	// Exists reports whether pid is running with the given creation time.
	Exists(pid int, createMs int64) bool
}
