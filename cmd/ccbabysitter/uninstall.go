package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
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
	// link is a link the program was started through. It goes in place
	// of the program, which is someone's own arrangement and stays.
	link string
	// ownFolder is the full path of the folder the installer makes for the
	// program alone, "" on a system where it shares a folder with others.
	ownFolder string
	// otherCopy is another ccbabysitter found on PATH, named at the end.
	otherCopy string
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
	// removeFiles deletes the program's files, now or, when later is
	// true, once this command has ended.
	removeFiles func(out io.Writer, plan removalPlan) (later bool, err error)
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

	var plan removalPlan
	if s.link != "" {
		plan = removalPlan{folder: filepath.Dir(s.link), files: []string{s.link}}
	} else if entries, err := s.listFolder(filepath.Dir(s.program)); err == nil {
		plan = planRemoval(s.program, s.companions, s.ownFolder, entries)
	} else {
		// A folder that cannot be listed may hold anything, so it is never
		// deleted whole.
		plan = planRemoval(s.program, s.companions, "", []string{filepath.Base(s.program)})
	}
	fmt.Fprintln(out, "This removes CC Babysitter from this computer:")
	fmt.Fprintln(out, "  start at login")
	fmt.Fprintf(out, "  the state folder %s, with the settings, the babysat sessions and the activity log\n", s.stateDir)
	switch {
	case s.link != "":
		fmt.Fprintf(out, "  the link %s, to %s, which stays\n", s.link, s.program)
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
			fmt.Fprintln(out, "Nothing changed: there is no terminal here to ask in. To remove it, run: ccbabysitter uninstall --yes")
			return 2
		}
		fmt.Fprint(out, "Continue? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(line), "y") {
			fmt.Fprintln(out, "Nothing changed.")
			return 0
		}
	}

	// Start at login left behind would start a program that is gone, and
	// on Linux the note that lingering was turned on is in the state
	// folder, so nothing more goes until it is removed.
	if rc := s.removeStart(out); rc != 0 {
		fmt.Fprintln(out, "Start at login could not be removed, so the state folder and program stay. Fix what it says, then run ccbabysitter uninstall again.")
		return rc
	}
	rc := 0
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
	later := false
	switch {
	case s.link == "" && goRun(s.program):
		fmt.Fprintln(out, "This copy runs from go run, so there is no program to delete.")
	case len(plan.files) > 0:
		var err error
		if later, err = s.removeFiles(out, plan); err != nil {
			fmt.Fprintln(out, "could not delete the program:", err)
			rc = 1
		}
	}
	if s.otherCopy != "" {
		fmt.Fprintf(out, "Another ccbabysitter is still on your PATH, at %s. Delete it too if you no longer want it.\n", s.otherCopy)
	}
	if rc == 0 && !later {
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
// is the installer's own folder, by its full path, and holds nothing else.
// A copy run with go run lives in Go's build cache and has nothing to
// delete.
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
		if own[lower] || own[strings.TrimSuffix(lower, ".bak")] || leftover(lower) {
			plan.files = append(plan.files, filepath.Join(plan.folder, name))
		} else {
			others++
		}
	}
	plan.whole = ownFolder != "" && others == 0 && strings.EqualFold(plan.folder, ownFolder)
	return plan
}

// leftover reports whether name, in lower case, is what an install script
// cut short leaves beside the program: install.sh's .ccbabysitter.XXXXXX and
// install.ps1's .ccbabysitter-GUID.part.
func leftover(name string) bool {
	return strings.HasPrefix(name, ".ccbabysitter.") || strings.HasPrefix(name, ".ccbabysitter-") && strings.HasSuffix(name, ".part")
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
	s := uninstallSystem()
	if !a.keepFiles {
		// Where this program is matters only when it is deleted. A copy in
		// a temporary folder is deleted like any other, and one run with go
		// run has nothing to delete.
		exe, err := os.Executable()
		if err == nil {
			s.program, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			fmt.Fprintln(os.Stdout, "could not tell where this program is:", err)
			return 1
		}
		s.link = findLink(invokedPath(), s.program)
		if found, err := exec.LookPath("ccbabysitter"); err == nil {
			if found, err = filepath.EvalSymlinks(found); err == nil && found != s.program && !strings.EqualFold(found, s.program) {
				s.otherCopy = found
			}
		}
	}
	s.stateDir = stateDir
	s.terminal = stdinIsTerminal()
	s.quit = func(out io.Writer) { quitRunningCopy(out, stateDir) }
	s.stillHeld = func(dir string) bool { return !releasedWithin(dir, 10*time.Second) }
	s.removeState = removeStateFolder
	s.listFolder = folderNames
	return uninstallFlow(s, a, os.Stdin, os.Stdout)
}

// invokedPath is the path this program was started by, as found on PATH
// when it was started by its name alone, or "" when that cannot be told.
func invokedPath() string {
	arg0 := os.Args[0]
	if filepath.Base(arg0) == arg0 {
		p, err := exec.LookPath(arg0)
		if err != nil {
			return ""
		}
		arg0 = p
	}
	p, err := filepath.Abs(arg0)
	if err != nil {
		return ""
	}
	return p
}

// findLink returns invoked when it is a link that leads to program, and
// "" otherwise.
func findLink(invoked, program string) string {
	if invoked == "" {
		return ""
	}
	fi, err := os.Lstat(invoked)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	if target, err := filepath.EvalSymlinks(invoked); err != nil || target != program {
		return ""
	}
	return invoked
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

// removeStateFolder deletes the state folder, which may not be there. It
// deletes nothing but an absolute path whose last part is the state
// folder's name: with no home folder known, the path would be relative to
// wherever the command runs.
func removeStateFolder(dir string) error {
	if !filepath.IsAbs(dir) || !strings.EqualFold(filepath.Base(dir), "ccbabysitter") {
		return fmt.Errorf("%s is not where CC Babysitter keeps its state, so it stays", dir)
	}
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
func removeFilesNow(out io.Writer, plan removalPlan) (bool, error) {
	var failed error
	for _, f := range plan.files {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed = errors.Join(failed, err)
			continue
		}
		fmt.Fprintln(out, "Deleted", f)
	}
	return false, failed
}
