package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/client"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// uninstallSteps are what uninstall does on this system, one function per
// step, so the order, the question and the refusals can be tested with
// fakes.
type uninstallSteps struct {
	stateDir string
	// program is this program, its links resolved, and companions the
	// names of the other files of the program that live beside it.
	program    string
	companions []string
	// ownFolder is the name of the folder the installer makes for the
	// program alone, "" on a system where it shares a folder with others.
	ownFolder string
	// terminal says a question can be asked on stdin.
	terminal bool

	// removeStart removes start at login, as uninstall --keep-files does.
	removeStart func(out io.Writer) int
	// quit asks a copy that is still running to quit.
	quit func(out io.Writer)
	// stillHeld reports whether a copy still owns the state folder after
	// it was given time to quit.
	stillHeld   func(dir string) bool
	removeState func(dir string) error
	listFolder  func(dir string) ([]string, error)
	removeFiles func(out io.Writer, plan removalPlan) error
	// removePath takes the program's own folder out of PATH. It is nil on
	// a system where the installer changes no PATH.
	removePath func(out io.Writer, folder string) error
}

// uninstallArgs are the flags uninstall takes.
type uninstallArgs struct {
	yes, keepFiles bool
}

// parseUninstallArgs reads uninstall's flags. Any other word is a usage
// error.
func parseUninstallArgs(args []string) (uninstallArgs, bool) {
	var a uninstallArgs
	for _, arg := range args {
		switch arg {
		case "--keep-files":
			a.keepFiles = true
		case "--yes":
			a.yes = true
		default:
			return a, false
		}
	}
	return a, true
}

// uninstallFlow removes CC Babysitter from this computer, after saying what
// goes and asking: start at login, a copy still running, the state folder,
// the program's folder on PATH where the installer put it there, and the
// program. Babysat sessions are not touched. With --keep-files it only
// removes start at login, and asks nothing.
func uninstallFlow(s uninstallSteps, a uninstallArgs, in io.Reader, out io.Writer) int {
	if a.keepFiles {
		rc := s.removeStart(out)
		fmt.Fprintln(out, foregroundHint)
		return rc
	}

	entries, err := s.listFolder(filepath.Dir(s.program))
	if err != nil {
		entries = []string{filepath.Base(s.program)}
	}
	plan := planRemoval(s.program, s.companions, s.ownFolder, entries)
	fmt.Fprintln(out, "This removes CC Babysitter from this computer:")
	fmt.Fprintln(out, "  start at login")
	fmt.Fprintf(out, "  the state folder %s, with the settings, the babysat sessions and the activity log\n", s.stateDir)
	switch {
	case plan.whole && s.removePath != nil:
		fmt.Fprintf(out, "  the folder %s, and its place in your user Path\n", plan.folder)
	case plan.whole:
		fmt.Fprintf(out, "  the folder %s\n", plan.folder)
	default:
		for _, f := range plan.files {
			fmt.Fprintf(out, "  %s\n", f)
		}
	}
	fmt.Fprintln(out, "Babysat sessions keep running where they are.")
	if !a.yes {
		if !s.terminal {
			fmt.Fprintln(out, "Nothing changed. To remove it, run: ccbabysitter uninstall --yes")
			return 2
		}
		fmt.Fprint(out, "Continue? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(line), "y") {
			fmt.Fprintln(out, "Nothing changed.")
			return 0
		}
	}

	rc := s.removeStart(out)
	s.quit(out)
	if s.stillHeld(s.stateDir) {
		fmt.Fprintln(out, "CC Babysitter is still running, so its state folder and program stay. Quit it, then run ccbabysitter uninstall again.")
		return 1
	}
	if err := s.removeState(s.stateDir); err != nil {
		fmt.Fprintln(out, "could not delete the state folder:", err)
		rc = 1
	} else {
		fmt.Fprintln(out, "Deleted the state folder", s.stateDir)
	}
	if plan.whole && s.removePath != nil {
		if err := s.removePath(out, plan.folder); err != nil {
			fmt.Fprintf(out, "could not take %s out of your user Path: %v\n", plan.folder, err)
			rc = 1
		}
	}
	switch {
	case goRun(s.program):
		fmt.Fprintln(out, "This copy runs from go run, so there is no program to delete.")
	case len(plan.files) > 0:
		if err := s.removeFiles(out, plan); err != nil {
			fmt.Fprintln(out, "could not delete the program:", err)
			rc = 1
		}
	}
	if rc == 0 {
		fmt.Fprintln(out, "CC Babysitter is removed.")
	}
	return rc
}

// removalPlan is what uninstall deletes of the program: its own files in
// its folder, and the folder itself when whole is set.
type removalPlan struct {
	folder string
	files  []string
	whole  bool
}

// planRemoval works out, from the names in the program's folder, which
// are the program's own: the program, its companions, and what an install
// that was cut short leaves beside them. The folder goes whole only when it
// is the installer's own folder, by name, and holds nothing else. A copy
// run with go run lives in Go's build cache and has nothing to delete.
func planRemoval(program string, companions []string, ownFolder string, entries []string) removalPlan {
	plan := removalPlan{folder: filepath.Dir(program)}
	if goRun(program) {
		return plan
	}
	own := map[string]bool{strings.ToLower(filepath.Base(program)): true}
	for _, c := range companions {
		own[strings.ToLower(c)] = true
	}
	others := 0
	for _, name := range entries {
		lower := strings.ToLower(name)
		if own[lower] || own[strings.TrimSuffix(lower, ".bak")] || strings.HasPrefix(lower, ".ccbabysitter") {
			plan.files = append(plan.files, filepath.Join(plan.folder, name))
		} else {
			others++
		}
	}
	plan.whole = ownFolder != "" && others == 0 && strings.EqualFold(filepath.Base(plan.folder), ownFolder)
	return plan
}

// goRun reports whether program was built by go run, in Go's build cache.
func goRun(program string) bool {
	return strings.Contains(filepath.ToSlash(program), "/go-build")
}

// runUninstall is the uninstall command, wired to this system.
func runUninstall(args []string) int {
	a, ok := parseUninstallArgs(args)
	if !ok {
		printUsage(os.Stderr)
		return 2
	}
	stateDir := state.DefaultDir()
	// The page's key goes only to a copy whose lock names a live process,
	// never to a stale address another account may listen on by now.
	state.SetPIDChecker(procs.NewReal().Exists)
	program, err := executablePath()
	if err != nil {
		fmt.Fprintln(os.Stdout, "could not tell where this program is:", err)
		return 1
	}
	s := uninstallSystem()
	s.stateDir = stateDir
	s.program = program
	s.terminal = stdinIsTerminal()
	s.quit = func(out io.Writer) { quitRunningCopy(out, stateDir) }
	s.stillHeld = func(dir string) bool { return !releasedWithin(dir, 10*time.Second) }
	s.removeState = removeStateFolder
	s.listFolder = folderNames
	return uninstallFlow(s, a, os.Stdin, os.Stdout)
}

// quitRunningCopy asks a copy that is still running to quit, which a copy
// started in a terminal is after start at login is gone.
func quitRunningCopy(out io.Writer, stateDir string) {
	cl, err := client.New(stateDir, "")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cl.Quit(ctx)
	switch {
	case err != nil:
		fmt.Fprintln(out, "could not ask CC Babysitter to quit:", err)
	case !res.OK:
		fmt.Fprintln(out, res.Message)
	default:
		fmt.Fprintln(out, "Quit CC Babysitter.")
	}
}

// releasedWithin waits up to timeout for the lock on stateDir to be let
// go, and reports whether it was.
func releasedWithin(stateDir string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for state.IsHeld(stateDir) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
	return true
}

// removeStateFolder deletes the state folder, which may not be there.
func removeStateFolder(dir string) error {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(dir)
}

// folderNames are the names in dir.
func folderNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

// stdinIsTerminal reports whether stdin is a terminal a question can be
// asked in, rather than a pipe, a file or the null device.
func stdinIsTerminal() bool { return isTerminal(os.Stdin) }

// removeFilesNow deletes the program's files at once, which a system that
// lets a running program delete itself allows.
func removeFilesNow(out io.Writer, plan removalPlan) error {
	var failed error
	for _, f := range plan.files {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed = errors.Join(failed, err)
			continue
		}
		fmt.Fprintln(out, "Deleted", f)
	}
	return failed
}
