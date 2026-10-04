//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// fakeLaunchctl answers launchctl from answers, keyed by the joined
// arguments, and records every call. An unknown call succeeds with no
// output.
type fakeLaunchctl struct {
	answers map[string]fakeAnswer
	calls   []string
	// stuck keeps the job loaded after a bootout, as launchd does while
	// the old process is still shutting down.
	stuck bool
}

type fakeAnswer struct {
	out string
	err error
}

func useFakeLaunchctl(t *testing.T) *fakeLaunchctl {
	t.Helper()
	f := &fakeLaunchctl{answers: map[string]fakeAnswer{}}
	saved := runLaunchctl
	runLaunchctl = func(args ...string) (string, error) {
		key := strings.Join(args, " ")
		f.calls = append(f.calls, key)
		// A bootout unloads the job, as launchd does.
		if args[0] == "bootout" && !f.stuck {
			f.answers["print "+job()] = notLoaded
		}
		a := f.answers[key]
		return a.out, a.err
	}
	t.Cleanup(func() { runLaunchctl = saved })
	return f
}

// launchdHome gives the test a home and a state folder of its own, and the
// program a path that a plist may name.
func launchdHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	saved := executablePath
	executablePath = func() (string, error) { return "/Users/dev/.local/bin/ccbabysitter", nil }
	t.Cleanup(func() { executablePath = saved })
}

var notLoaded = fakeAnswer{"", errors.New("exit status 113")}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }
func job() string    { return domain() + "/com.ccbabysitter" }

func TestLaunchdUsableNeedsAGUIDomain(t *testing.T) {
	f := useFakeLaunchctl(t)
	f.answers["print "+domain()] = fakeAnswer{"", errors.New("exit status 112")}
	if (launchdControl{}).Usable() {
		t.Fatal("usable without a GUI domain")
	}
	delete(f.answers, "print "+domain())
	if !(launchdControl{}).Usable() {
		t.Fatal("not usable with a GUI domain")
	}
}

func TestLaunchdActiveReadsTheState(t *testing.T) {
	f := useFakeLaunchctl(t)
	f.answers["print "+job()] = fakeAnswer{"com.ccbabysitter = {\n\tstate = running\n\tpid = 42\n}\n", nil}
	if !(launchdControl{}).Active() {
		t.Fatal("running job not active")
	}
	f.answers["print "+job()] = fakeAnswer{"com.ccbabysitter = {\n\tstate = not running\n}\n", nil}
	if (launchdControl{}).Active() {
		t.Fatal("stopped job active")
	}
	f.answers["print "+job()] = notLoaded
	if (launchdControl{}).Active() {
		t.Fatal("unloaded job active")
	}
}

func TestLaunchdWriteGoesToTheStateFolder(t *testing.T) {
	launchdHome(t)
	useFakeLaunchctl(t)
	var out strings.Builder
	if !(launchdControl{}).Write(&out, true) {
		t.Fatalf("write failed: %s", out.String())
	}
	text, err := os.ReadFile(offPath())
	if err != nil || !strings.Contains(string(text), "--service") {
		t.Fatalf("plist in the state folder: %v %q", err, text)
	}
	if on, _ := pathPresent(agentPath()); on {
		t.Fatal("a plist was put in LaunchAgents")
	}
	if !(launchdControl{}).Installed() {
		t.Fatal("not installed after a write")
	}
	if desktop, known := (launchdControl{}).UnitKind(); !desktop || !known {
		t.Fatalf("kind %v %v", desktop, known)
	}
}

func TestLaunchdStartBootstrapsOrKickstarts(t *testing.T) {
	launchdHome(t)
	f := useFakeLaunchctl(t)
	var out strings.Builder
	(launchdControl{}).Write(&out, true)

	f.answers["print "+job()] = notLoaded
	if !(launchdControl{}).Start(&out, true) {
		t.Fatalf("start failed: %s", out.String())
	}
	if got := strings.Join(f.calls, "; "); got != "print "+job()+"; bootstrap "+domain()+" "+offPath() {
		t.Fatalf("not loaded: %q", got)
	}

	// Loaded but not running, as after a quit: it is unloaded and loaded
	// again, so launchd reads the plist as it is now, never an old
	// definition it still holds.
	f.calls = nil
	f.answers["print "+job()] = fakeAnswer{"state = not running\n", nil}
	(launchdControl{}).Start(&out, true)
	if got := strings.Join(f.calls, "; "); !strings.HasPrefix(got, "print "+job()+"; bootout "+job()+"; ") || !strings.HasSuffix(got, "; bootstrap "+domain()+" "+offPath()) || strings.Contains(got, "kickstart") {
		t.Fatalf("loaded: %q", got)
	}
}

func TestLaunchdRestartReloadsThePlist(t *testing.T) {
	launchdHome(t)
	f := useFakeLaunchctl(t)
	var out strings.Builder
	(launchdControl{}).Write(&out, true)
	f.calls = nil
	if !(launchdControl{}).Restart(&out, true) {
		t.Fatalf("restart failed: %s", out.String())
	}
	if got := strings.Join(f.calls, "; "); got != "bootout "+job()+"; print "+job()+"; bootstrap "+domain()+" "+offPath() {
		t.Fatalf("restart: %q", got)
	}
}

// A LaunchAgent an earlier version wrote, which starts the bare binary, is
// written again where it is, so start at login stays on.
func TestLaunchdRefreshRewritesAnOldAgentInPlace(t *testing.T) {
	launchdHome(t)
	useFakeLaunchctl(t)
	old := `<plist><dict><key>Label</key><string>com.ccbabysitter</string><key>ProgramArguments</key><array><string>/Users/dev/.local/bin/ccbabysitter</string></array></dict></plist>`
	if err := os.MkdirAll(filepath.Dir(agentPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentPath(), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	rewritten, ok := (launchdControl{}).RefreshUnit(&out, true)
	if !rewritten || !ok {
		t.Fatalf("rewritten %v ok %v: %s", rewritten, ok, out.String())
	}
	text, _ := os.ReadFile(agentPath())
	if !strings.Contains(string(text), "--service") {
		t.Fatalf("not rewritten in place:\n%s", text)
	}
	if on, _ := pathPresent(offPath()); on {
		t.Fatal("a copy appeared in the state folder")
	}
	if rewritten, ok := (launchdControl{}).RefreshUnit(&out, true); rewritten || !ok {
		t.Fatalf("a right plist was rewritten: %v %v", rewritten, ok)
	}
}

func TestDarwinUninstallRemovesBothPlists(t *testing.T) {
	launchdHome(t)
	f := useFakeLaunchctl(t)
	var out strings.Builder
	(launchdControl{}).Write(&out, true)
	if err := os.MkdirAll(filepath.Dir(agentPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentPath(), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	out.Reset()
	if rc := runUninstall(&out); rc != 0 {
		t.Fatalf("rc %d: %s", rc, out.String())
	}
	for _, path := range []string{agentPath(), offPath()} {
		if on, _ := pathPresent(path); on {
			t.Errorf("%s is still there", path)
		}
		if !strings.Contains(out.String(), "Removed "+path) {
			t.Errorf("output does not name %s:\n%s", path, out.String())
		}
	}
	if len(f.calls) != 1 || f.calls[0] != "bootout "+job() {
		t.Fatalf("calls %q", f.calls)
	}
}

// A LaunchAgent switched off under Login Items cannot be loaded: the
// failure says where to switch it back on, and how to run without it.
func TestLaunchdLoadFailureSaysWhatToDo(t *testing.T) {
	launchdHome(t)
	f := useFakeLaunchctl(t)
	var out strings.Builder
	(launchdControl{}).Write(&out, true)
	out.Reset()
	f.answers["print "+job()] = notLoaded
	f.answers["bootstrap "+domain()+" "+offPath()] = fakeAnswer{"Bootstrap failed: 5: Input/output error", errors.New("exit status 5")}
	if (launchdControl{}).Start(&out, true) {
		t.Fatal("a failed bootstrap counted as started")
	}
	for _, want := range []string{"Input/output error", "Login Items", "ccbabysitter --foreground"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in %q", want, out.String())
		}
	}
}

// launchd starts the LaunchAgent's program with launchd as its parent and
// XPC_SERVICE_NAME set to the label; that run is the service, even when an
// earlier version's plist starts it with no --service. A program the
// service starts in turn inherits XPC_SERVICE_NAME but not the parent.
func TestStartedByLaunchd(t *testing.T) {
	saved := parentPID
	t.Cleanup(func() { parentPID = saved })
	t.Setenv("XPC_SERVICE_NAME", "com.ccbabysitter")
	parentPID = func() int { return 1 }
	if !startedByServiceManager() {
		t.Fatal("the LaunchAgent's own run was not seen")
	}
	parentPID = func() int { return 4242 }
	if startedByServiceManager() {
		t.Fatal("a child of the service counted as the service")
	}
	parentPID = func() int { return 1 }
	t.Setenv("XPC_SERVICE_NAME", "com.apple.Terminal")
	if startedByServiceManager() {
		t.Fatal("another job counted as the service")
	}
}

func TestNotStartedLineOnMacOSNeedsNoJournal(t *testing.T) {
	if line := notStartedLine(); strings.Contains(line, "journalctl") || !strings.Contains(line, "launchctl print") {
		t.Fatalf("%q", line)
	}
}

// A job launchd still holds after the wait is never loaded over: the load
// would fail, and nothing would start the copy once the old one ended.
func TestLaunchdRestartWaitsForTheOldJob(t *testing.T) {
	launchdHome(t)
	f := useFakeLaunchctl(t)
	saved := launchdUnloadWait
	launchdUnloadWait = 200 * time.Millisecond
	t.Cleanup(func() { launchdUnloadWait = saved })
	var out strings.Builder
	(launchdControl{}).Write(&out, true)
	f.calls = nil
	f.stuck = true
	f.answers["print "+job()] = fakeAnswer{"state = running\n", nil}
	if (launchdControl{}).Restart(&out, true) {
		t.Fatal("restarted over a job that is still loaded")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "bootstrap") {
			t.Fatalf("loaded again while the old job was there: %q", f.calls)
		}
	}
	if !strings.Contains(out.String(), "still") {
		t.Fatalf("output %q", out.String())
	}
}

func TestDarwinUninstallSaysWhenBootoutFailed(t *testing.T) {
	launchdHome(t)
	f := useFakeLaunchctl(t)
	f.answers["bootout "+job()] = fakeAnswer{"Boot-out failed: 3: No such process", errors.New("exit status 3")}
	var out strings.Builder
	runUninstall(&out)
	if strings.Contains(out.String(), "Stopped the CC Babysitter LaunchAgent.") || !strings.Contains(out.String(), "was not running") {
		t.Fatalf("output %q", out.String())
	}
}

// Uninstall forgets that start at login was turned on once, so a later
// plain run turns it on again, as on a machine that never had it.
func TestDarwinUninstallForgetsTheLoginStartChoice(t *testing.T) {
	launchdHome(t)
	useFakeLaunchctl(t)
	if err := state.MarkLoginStartOffered(state.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runUninstall(&out)
	if state.LoginStartOffered(state.DefaultDir()) {
		t.Fatalf("the choice is still remembered after uninstall:\n%s", out.String())
	}
}
