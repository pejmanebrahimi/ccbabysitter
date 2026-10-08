package main

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// fakeUninstall records the steps an uninstall takes, in order.
type fakeUninstall struct {
	steps    []string
	held     bool
	entries  []string
	stateErr error
}

func (f *fakeUninstall) deps(program string, terminal bool) uninstallSteps {
	return uninstallSteps{
		stateDir:   "/home/dev/.local/state/ccbabysitter",
		program:    program,
		companions: []string{"ccbabysitter-background.exe"},
		terminal:   terminal,
		removeStart: func(out io.Writer) int {
			f.steps = append(f.steps, "start")
			io.WriteString(out, "Removed start at login.\n")
			return 0
		},
		quit:        func(io.Writer) { f.steps = append(f.steps, "quit") },
		stillHeld:   func(string) bool { return f.held },
		removeState: func(dir string) error { f.steps = append(f.steps, "state "+dir); return f.stateErr },
		listFolder:  func(string) ([]string, error) { return f.entries, nil },
		removeFiles: func(_ io.Writer, plan removalPlan) error {
			f.steps = append(f.steps, "files "+strings.Join(plan.files, ",")+map[bool]string{true: " whole", false: ""}[plan.whole])
			return nil
		},
		removePath: func(_ io.Writer, folder string) error { f.steps = append(f.steps, "path "+folder); return nil },
	}
}

func runFlow(t *testing.T, f *fakeUninstall, d uninstallSteps, args []string, answer string) (int, string) {
	t.Helper()
	a, ok := parseUninstallArgs(args)
	if !ok {
		t.Fatalf("args %v refused", args)
	}
	var out strings.Builder
	rc := uninstallFlow(d, a, strings.NewReader(answer), &out)
	return rc, out.String()
}

// With --keep-files, uninstall only removes start at login, as it always
// did, and asks nothing.
func TestUninstallKeepFiles(t *testing.T) {
	f := &fakeUninstall{entries: []string{"ccbabysitter"}}
	rc, out := runFlow(t, f, f.deps("/home/dev/.local/bin/ccbabysitter", false), []string{"--keep-files"}, "")
	if rc != 0 || strings.Join(f.steps, "|") != "start" {
		t.Fatalf("rc %d, steps %v", rc, f.steps)
	}
	if !strings.Contains(out, foregroundHint) || strings.Contains(out, "Continue?") {
		t.Fatalf("output %q", out)
	}
}

// A full uninstall says what it removes and asks first. Anything but y
// changes nothing.
func TestUninstallAsksFirst(t *testing.T) {
	f := &fakeUninstall{entries: []string{"ccbabysitter"}}
	rc, out := runFlow(t, f, f.deps("/home/dev/.local/bin/ccbabysitter", true), nil, "n\n")
	if rc != 0 || len(f.steps) != 0 {
		t.Fatalf("rc %d, steps %v", rc, f.steps)
	}
	for _, want := range []string{"/home/dev/.local/state/ccbabysitter", "/home/dev/.local/bin/ccbabysitter", "Continue? [y/N]", "Nothing changed."} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
}

// Without a terminal to ask in, a full uninstall needs --yes: it says what
// it would remove, changes nothing and exits with 2.
func TestUninstallWithoutATerminalNeedsYes(t *testing.T) {
	f := &fakeUninstall{entries: []string{"ccbabysitter"}}
	rc, out := runFlow(t, f, f.deps("/home/dev/.local/bin/ccbabysitter", false), nil, "y\n")
	if rc != 2 || len(f.steps) != 0 || !strings.Contains(out, "ccbabysitter uninstall --yes") {
		t.Fatalf("rc %d, steps %v, output %q", rc, f.steps, out)
	}
}

// Confirmed, it removes start at login, quits a copy still running,
// deletes the state folder and then the program, in that order. In a
// shared folder only its own file goes, and the folder stays on PATH.
func TestUninstallRemovesEverythingInOrder(t *testing.T) {
	f := &fakeUninstall{entries: []string{"ccbabysitter", "rustup", "uv"}}
	rc, out := runFlow(t, f, f.deps("/home/dev/.local/bin/ccbabysitter", true), nil, "y\n")
	want := "start|quit|state /home/dev/.local/state/ccbabysitter|files /home/dev/.local/bin/ccbabysitter"
	if rc != 0 || strings.Join(f.steps, "|") != want {
		t.Fatalf("rc %d, steps %v, want %s", rc, f.steps, want)
	}
	if !strings.Contains(out, "Babysat sessions keep running where they are.") {
		t.Fatalf("output %q", out)
	}
	f = &fakeUninstall{entries: []string{"ccbabysitter"}}
	rc, _ = runFlow(t, f, f.deps("/home/dev/.local/bin/ccbabysitter", false), []string{"--yes"}, "")
	if rc != 0 || len(f.steps) != 4 {
		t.Fatalf("--yes asks nothing: rc %d, steps %v", rc, f.steps)
	}
}

// The installer's own folder on Windows goes whole, and comes out of the
// user Path, before the program files are removed.
func TestUninstallRemovesItsOwnFolderAndPath(t *testing.T) {
	folder := filepath.Join("C:", "Users", "dev", "AppData", "Local", "Programs", "CCBabysitter")
	f := &fakeUninstall{entries: []string{"ccbabysitter.exe", "ccbabysitter-background.exe"}}
	d := f.deps(filepath.Join(folder, "ccbabysitter.exe"), false)
	d.ownFolder = "CCBabysitter"
	rc, _ := runFlow(t, f, d, []string{"--yes"}, "")
	got := strings.Join(f.steps, "|")
	if rc != 0 || !strings.HasSuffix(got, "|path "+folder+"|files "+filepath.Join(folder, "ccbabysitter.exe")+","+filepath.Join(folder, "ccbabysitter-background.exe")+" whole") {
		t.Fatalf("rc %d, steps %s", rc, got)
	}
}

// A copy still running after it was asked to quit owns the state folder,
// so nothing more is deleted and uninstall says why.
func TestUninstallStopsWhileStillRunning(t *testing.T) {
	f := &fakeUninstall{held: true, entries: []string{"ccbabysitter"}}
	rc, out := runFlow(t, f, f.deps("/home/dev/.local/bin/ccbabysitter", false), []string{"--yes"}, "")
	if rc != 1 || strings.Join(f.steps, "|") != "start|quit" || !strings.Contains(out, "still running") {
		t.Fatalf("rc %d, steps %v, output %q", rc, f.steps, out)
	}
}

// A state folder that cannot be deleted is said, and the program still
// goes.
func TestUninstallGoesOnWhenTheStateFolderStays(t *testing.T) {
	f := &fakeUninstall{entries: []string{"ccbabysitter"}, stateErr: errors.New("busy")}
	rc, out := runFlow(t, f, f.deps("/home/dev/.local/bin/ccbabysitter", false), []string{"--yes"}, "")
	if rc != 1 || len(f.steps) != 4 || !strings.Contains(out, "busy") {
		t.Fatalf("rc %d, steps %v, output %q", rc, f.steps, out)
	}
}

// A copy run with go run lives in Go's build cache, which is not deleted.
func TestUninstallLeavesGoRunAlone(t *testing.T) {
	f := &fakeUninstall{entries: []string{"exe"}}
	program := filepath.Join("/tmp", "go-build123456", "b001", "exe", "ccbabysitter")
	rc, out := runFlow(t, f, f.deps(program, false), []string{"--yes"}, "")
	if rc != 0 || strings.Join(f.steps, "|") != "start|quit|state /home/dev/.local/state/ccbabysitter" || !strings.Contains(out, "go run") {
		t.Fatalf("rc %d, steps %v, output %q", rc, f.steps, out)
	}
}

// uninstall takes --yes and --keep-files, and any other word is a usage
// error, so nothing is removed.
func TestParseUninstallArgs(t *testing.T) {
	for _, c := range []struct {
		args          []string
		ok, yes, keep bool
	}{
		{nil, true, false, false}, {[]string{"--yes"}, true, true, false}, {[]string{"--keep-files"}, true, false, true},
		{[]string{"--yes", "--keep-files"}, true, true, true}, {[]string{"now"}, false, false, false},
		{[]string{"--force"}, false, false, false}, {[]string{"--yes", "extra"}, false, true, false},
	} {
		a, ok := parseUninstallArgs(c.args)
		if ok != c.ok || (ok && (a.yes != c.yes || a.keepFiles != c.keep)) {
			t.Errorf("%v: %+v %v", c.args, a, ok)
		}
	}
}

// What is removed of the program: its own files, and the folder whole
// only when it is the installer's own folder and holds nothing else.
func TestPlanRemoval(t *testing.T) {
	win := filepath.Join("C:", "Programs", "CCBabysitter")
	for _, c := range []struct {
		name      string
		program   string
		ownFolder string
		entries   []string
		files     []string
		whole     bool
	}{
		{"shared folder", "/home/dev/.local/bin/ccbabysitter", "", []string{"ccbabysitter", "uv"}, []string{"/home/dev/.local/bin/ccbabysitter"}, false},
		{"alone, no own folder here", "/home/dev/.local/bin/ccbabysitter", "", []string{"ccbabysitter"}, []string{"/home/dev/.local/bin/ccbabysitter"}, false},
		{"own folder", filepath.Join(win, "ccbabysitter.exe"), "CCBabysitter",
			[]string{"ccbabysitter.exe", "ccbabysitter-background.exe", "ccbabysitter.exe.bak", ".ccbabysitter-1f2e.part"},
			[]string{filepath.Join(win, "ccbabysitter.exe"), filepath.Join(win, "ccbabysitter-background.exe"), filepath.Join(win, "ccbabysitter.exe.bak"), filepath.Join(win, ".ccbabysitter-1f2e.part")}, true},
		{"own folder with someone else's file", filepath.Join(win, "ccbabysitter.exe"), "CCBabysitter",
			[]string{"ccbabysitter.exe", "notes.txt"}, []string{filepath.Join(win, "ccbabysitter.exe")}, false},
		{"another folder", filepath.Join("C:", "tools", "ccbabysitter.exe"), "CCBabysitter",
			[]string{"ccbabysitter.exe", "ccbabysitter-background.exe"}, []string{filepath.Join("C:", "tools", "ccbabysitter.exe"), filepath.Join("C:", "tools", "ccbabysitter-background.exe")}, false},
		{"go run", "/tmp/go-build99/b001/exe/ccbabysitter", "", []string{"ccbabysitter"}, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := planRemoval(c.program, []string{"ccbabysitter-background.exe"}, c.ownFolder, c.entries)
			if strings.Join(p.files, ",") != strings.Join(c.files, ",") || p.whole != c.whole || p.folder != filepath.Dir(c.program) {
				t.Fatalf("got %+v, want files %v whole %v", p, c.files, c.whole)
			}
		})
	}
}
