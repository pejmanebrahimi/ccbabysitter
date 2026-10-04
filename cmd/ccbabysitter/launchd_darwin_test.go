//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLaunchctl answers launchctl from answers, keyed by the joined
// arguments, and records every call. An unknown call succeeds with no
// output.
type fakeLaunchctl struct {
	answers map[string]fakeAnswer
	calls   []string
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

	f.calls = nil
	f.answers["print "+job()] = fakeAnswer{"state = not running\n", nil}
	(launchdControl{}).Start(&out, true)
	if got := strings.Join(f.calls, "; "); got != "print "+job()+"; kickstart "+job() {
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
	if got := strings.Join(f.calls, "; "); got != "bootout "+job()+"; bootstrap "+domain()+" "+offPath() {
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
