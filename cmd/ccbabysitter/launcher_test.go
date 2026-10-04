package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// fakeDeps is a launcher's view of a running copy: its view, whether
// turning login start on works, and what was opened.
type fakeDeps struct {
	view     supervise.View
	loginErr error
	loginOns int
	opened   []string
	openErr  error
}

func (f *fakeDeps) deps(wait waitFunc) launchDeps {
	return launchDeps{
		wait: wait,
		view: func(string) (supervise.View, error) { return f.view, nil },
		loginOn: func(string) error {
			f.loginOns++
			if f.loginErr == nil {
				f.view.Settings.Autostart = true
			}
			return f.loginErr
		},
		launchURL: func(string) (string, error) { return "http://127.0.0.1:47391/?token=onetime", nil },
		open: func(u string) error {
			f.opened = append(f.opened, u)
			return f.openErr
		},
	}
}

func desktopView() supervise.View {
	return supervise.View{Version: "0.5.0", Env: hosts.Env{CLIVersion: "2.1.280"}, Settings: state.Settings{AutoOpenBrowser: true}}
}

// keyedDir is a state folder with a page key in it.
func keyedDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// okService is a service manager where every step works.
func okService() *fakeService {
	return &fakeService{usable: true, writeOK: true, enableOK: true, refreshOK: true, startOK: true, restartOK: true}
}

const pageAt = "http://127.0.0.1:47391"

func TestLauncherStartsADesktopCopyAndTurnsLoginStartOnOnce(t *testing.T) {
	dir := keyedDir(t)
	f := okService()
	d := &fakeDeps{view: desktopView()}
	var out strings.Builder

	if rc := runLauncher(&out, f, dir, launchOptions{}, d.deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d:\n%s", rc, out.String())
	}
	if !f.did("write desktop") || !f.did("start") || f.did("enable") || f.did("linger on") {
		t.Fatalf("calls %q", f.calls)
	}
	if d.loginOns != 1 || !state.LoginStartOffered(dir) {
		t.Fatalf("login start turned on %d times, noted %v", d.loginOns, state.LoginStartOffered(dir))
	}
	if got := state.TakeStartReason(dir); got != "Started in the background by ccbabysitter." {
		t.Fatalf("start reason %q", got)
	}
	got := out.String()
	if !strings.Contains(got, "Page: "+pageAt+"/?token="+state.ReadPageKey(dir)+"\n") || !strings.Contains(got, "starts again when you log in. You can close this window.") || strings.Contains(got, "Ctrl+C") {
		t.Fatalf("output:\n%s", got)
	}
	if len(d.opened) != 1 || d.opened[0] != pageAt+"/?token=onetime" {
		t.Fatalf("opened %q", d.opened)
	}

	// The person turns login start off; a later run leaves it off, and
	// leaves the running copy and its right unit alone.
	d.view.Settings.Autostart = false
	f.installed, f.active = true, true
	out.Reset()
	if rc := runLauncher(&out, f, dir, launchOptions{}, d.deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("second run rc %d", rc)
	}
	if d.loginOns != 1 {
		t.Fatal("a later run turned login start on again")
	}
	if f.count("start") != 1 || f.did("restart") {
		t.Fatalf("a running copy was started or restarted again: %q", f.calls)
	}
	if !strings.Contains(out.String(), "Start at login is off.") {
		t.Fatalf("second output:\n%s", out.String())
	}
}

func TestLauncherOnAServerLingersAndOpensNothing(t *testing.T) {
	dir := keyedDir(t)
	f := okService()
	d := &fakeDeps{view: desktopView()}
	var out strings.Builder
	if rc := runLauncher(&out, f, dir, launchOptions{Headless: true, Server: true}, d.deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if !f.did("write server") || !f.did("linger on") || len(d.opened) != 0 {
		t.Fatalf("calls %q, opened %q", f.calls, d.opened)
	}
	if !strings.Contains(out.String(), "ssh -L 47391:127.0.0.1:47391") || !strings.Contains(out.String(), "starts again when this server boots.") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestLauncherNoOpenOpensNothing(t *testing.T) {
	d := &fakeDeps{view: desktopView()}
	var out strings.Builder
	if rc := runLauncher(&out, okService(), keyedDir(t), launchOptions{NoOpen: true}, d.deps(answeringAt(pageAt))); rc != 0 || len(d.opened) != 0 {
		t.Fatalf("rc %d, opened %q", rc, d.opened)
	}
}

func TestLauncherSaysWhenTheBrowserCannotOpen(t *testing.T) {
	d := &fakeDeps{view: desktopView(), openErr: errors.New("no browser")}
	var out strings.Builder
	if rc := runLauncher(&out, okService(), keyedDir(t), launchOptions{}, d.deps(answeringAt(pageAt))); rc != 0 || !strings.Contains(out.String(), "Could not open a browser: no browser. Open "+pageAt+"/?token=") {
		t.Fatalf("rc %d:\n%s", rc, out.String())
	}
}

func TestLauncherRefusesBesideACopyInATerminal(t *testing.T) {
	for _, o := range []launchOptions{{}, {Install: true}} {
		dir := filepath.Join(t.TempDir(), "ccbabysitter")
		holdLock(t, dir)
		f := &fakeService{usable: true, installed: true, writeOK: true, refreshOK: true, startOK: true, enableOK: true}
		var out strings.Builder
		if rc := runLauncher(&out, f, dir, o, (&fakeDeps{}).deps(neverAnswering)); rc != 1 {
			t.Fatalf("%+v: rc %d", o, rc)
		}
		if f.did("start") || f.did("enable") || f.did("refresh desktop") || f.did("refresh server") || !strings.Contains(out.String(), "already running in another terminal") {
			t.Fatalf("%+v: calls %q, out %q", o, f.calls, out.String())
		}
	}
}

// The background copy itself holds the lock while it runs: that is not a
// copy in a terminal.
func TestLauncherWithTheServiceHoldingTheLock(t *testing.T) {
	dir := keyedDir(t)
	holdLock(t, dir)
	f := okService()
	f.installed, f.active = true, true
	var out strings.Builder
	if rc := runLauncher(&out, f, dir, launchOptions{NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d, out %q", rc, out.String())
	}
}

func TestLauncherStopsWhenAStepFails(t *testing.T) {
	cases := map[string]func(*fakeService){
		"write":   func(f *fakeService) { f.writeOK = false },
		"start":   func(f *fakeService) { f.startOK = false },
		"refresh": func(f *fakeService) { f.installed, f.refreshOK = true, false },
		"restart": func(f *fakeService) { f.installed, f.active, f.staleUnit, f.restartOK = true, true, true, false },
	}
	for name, breakIt := range cases {
		f := okService()
		breakIt(f)
		var out strings.Builder
		d := &fakeDeps{view: desktopView()}
		if rc := runLauncher(&out, f, keyedDir(t), launchOptions{NoOpen: true}, d.deps(answeringAt(pageAt))); rc != 1 || d.loginOns != 0 {
			t.Errorf("%s: rc %d, login ons %d, out %q", name, rc, d.loginOns, out.String())
		}
	}
	f := okService()
	f.enableOK = false
	var out strings.Builder
	if rc := runLauncher(&out, f, keyedDir(t), launchOptions{Install: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt(pageAt))); rc != 1 {
		t.Errorf("install with a failing enable: rc %d", rc)
	}
}

func TestLauncherSaysWhenTheCopyNeverAnswers(t *testing.T) {
	f := okService()
	f.installed, f.active = true, true
	var out strings.Builder
	if rc := runLauncher(&out, f, keyedDir(t), launchOptions{}, (&fakeDeps{}).deps(neverAnswering)); rc != 1 || out.String() != notStartedLine()+"\n" {
		t.Fatalf("rc %d, out %q", rc, out.String())
	}
}

func TestLauncherRewritesAStaleUnit(t *testing.T) {
	// Running from the old unit: restarted once.
	f := okService()
	f.installed, f.active, f.staleUnit = true, true, true
	var out strings.Builder
	if rc := runLauncher(&out, f, keyedDir(t), launchOptions{NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt(pageAt))); rc != 0 || f.count("restart") != 1 || f.did("start") {
		t.Fatalf("active: rc %d, calls %q", rc, f.calls)
	}
	// Stopped: rewritten and started, not restarted.
	f = okService()
	f.installed, f.staleUnit = true, true
	out.Reset()
	if rc := runLauncher(&out, f, keyedDir(t), launchOptions{NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt(pageAt))); rc != 0 || f.did("restart") || !f.did("start") {
		t.Fatalf("stopped: rc %d, calls %q", rc, f.calls)
	}
}

func TestLauncherRestartsAnotherVersionOnce(t *testing.T) {
	// Another version answers, then this one: one restart, two waits.
	f := okService()
	f.installed, f.active = true, true
	wait, waits := answeringWith(pageAt, "0.3.0", buildinfo.Version)
	var out strings.Builder
	if rc := runLauncher(&out, f, keyedDir(t), launchOptions{NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(wait)); rc != 0 || f.count("restart") != 1 || *waits != 2 {
		t.Fatalf("rc %d, restarts %d, waits %d", rc, f.count("restart"), *waits)
	}
	// Still the old version after the restart: not restarted again.
	f = okService()
	f.installed, f.active = true, true
	wait, waits = answeringWith(pageAt, "0.3.0", "0.3.0")
	runLauncher(&out, f, keyedDir(t), launchOptions{NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(wait))
	if f.count("restart") != 1 || *waits != 2 {
		t.Fatalf("only once: restarts %d, waits %d", f.count("restart"), *waits)
	}
	// Already restarted for a stale unit: not restarted for the version.
	f = okService()
	f.installed, f.active, f.staleUnit = true, true, true
	wait, _ = answeringWith(pageAt, "0.3.0")
	runLauncher(&out, f, keyedDir(t), launchOptions{NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(wait))
	if f.count("restart") != 1 {
		t.Fatalf("at most once: restarts %d", f.count("restart"))
	}
}

func TestStartInBackgroundFallsBackWithoutAUserManager(t *testing.T) {
	var out strings.Builder
	rc, foreground := startInBackground(&out, &fakeService{usable: false}, t.TempDir(), launchOptions{}, (&fakeDeps{}).deps(neverAnswering))
	if !foreground || rc != 0 || out.String() != "Could not set CC Babysitter up as a service here, so it runs only while this terminal stays open.\n" {
		t.Fatalf("rc %d, foreground %v, %q", rc, foreground, out.String())
	}
}

func TestLauncherKeepsGoingWhenLoginStartCannotBeTurnedOn(t *testing.T) {
	dir := keyedDir(t)
	d := &fakeDeps{view: desktopView(), loginErr: errors.New("permission denied")}
	var out strings.Builder
	if rc := runLauncher(&out, okService(), dir, launchOptions{NoOpen: true}, d.deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if state.LoginStartOffered(dir) || !strings.Contains(out.String(), "Could not turn start at login on: permission denied") {
		t.Fatalf("noted %v, out %q", state.LoginStartOffered(dir), out.String())
	}
}

// install always turns start at boot on, with a server unit and lingering,
// whether or not this machine has a display, and never opens the page.
func TestLauncherForInstall(t *testing.T) {
	dir := keyedDir(t)
	f := okService()
	d := &fakeDeps{view: desktopView()}
	var out strings.Builder
	if rc := runLauncher(&out, f, dir, launchOptions{Install: true, NoOpen: true}, d.deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	// The running copy's own setting is turned on too, so the banner, the
	// page and ccbabysitter settings say start at boot is on straight away.
	if !f.did("write server") || !f.did("enable") || !f.did("linger on") || f.did("usable") || d.loginOns != 1 || !state.LoginStartOffered(dir) {
		t.Fatalf("calls %q, login ons %d", f.calls, d.loginOns)
	}
	if !strings.Contains(out.String(), "starts again when this server boots.") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// The copy's page needs its key like any other copy's, and the address
// printed once it answers carries that key. The key is read again on every
// look, since a copy started for the first time writes it only as it
// starts.
func TestLauncherPageCarriesTheKey(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "198.51.100.4 50000 203.0.113.7 22")
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	key := strings.Repeat("cd", 32)
	ts := keyedPage(t, key)
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	f := okService()
	// The copy, as it starts, takes the lock and writes its key.
	f.onWrite = func() {
		holdLock(t, dir)
		if err := os.WriteFile(filepath.Join(dir, "page-key"), []byte(key+"\n"), 0o600); err != nil {
			t.Error(err)
		}
	}
	wait := func(stateDir string) (servicePage, bool) {
		return waitForPage(stateDir, time.Second, 10*time.Millisecond, keyedPageState(stateDir))
	}
	var out strings.Builder
	if rc := runLauncher(&out, f, dir, launchOptions{Headless: true, Server: true}, (&fakeDeps{view: desktopView()}).deps(wait)); rc != 0 {
		t.Fatalf("rc %d, out %q", rc, out.String())
	}
	if want := "then open " + ts.URL + "/?token=" + key + " in its browser"; !strings.Contains(out.String(), want) {
		t.Fatalf("missing %q in\n%s", want, out.String())
	}
}

func TestLaunches(t *testing.T) {
	for _, c := range []struct {
		name   string
		opts   serveOptions
		system bool
		want   bool
	}{
		{"plain", serveOptions{}, false, true},
		{"--no-open", serveOptions{NoOpen: true}, false, true},
		{"--foreground", serveOptions{Foreground: true}, false, false},
		{"--demo", serveOptions{Demo: true}, false, false},
		{"--service", serveOptions{Service: true}, false, false},
		{"started by the service manager", serveOptions{}, true, false},
	} {
		if got := launches(c.opts, c.system); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// install set CC Babysitter up to start at boot: a plain run from a desktop
// terminal later keeps that server unit, restarts nothing, and says it
// starts at boot.
func TestLauncherFromADesktopKeepsAServerUnit(t *testing.T) {
	dir := keyedDir(t)
	if err := state.MarkLingeringTurnedOn(dir); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkLoginStartOffered(dir); err != nil {
		t.Fatal(err)
	}
	f := okService()
	f.installed, f.active, f.unitKnown, f.unitDesktop, f.lingerOn = true, true, true, false, true
	d := &fakeDeps{view: desktopView()}
	d.view.Settings.Autostart = true
	var out strings.Builder
	if rc := runLauncher(&out, f, dir, launchOptions{NoOpen: true}, d.deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if !f.did("refresh server") || f.did("refresh desktop") || f.did("restart") {
		t.Fatalf("calls %q", f.calls)
	}
	if !strings.Contains(out.String(), "starts again when this server boots.") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// A desktop reached over ssh keeps its desktop unit, and gets no lingering.
func TestLauncherOverSSHKeepsADesktopUnit(t *testing.T) {
	dir := keyedDir(t)
	f := okService()
	f.installed, f.active, f.unitKnown, f.unitDesktop = true, true, true, true
	var out strings.Builder
	if rc := runLauncher(&out, f, dir, launchOptions{Headless: true, Server: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if !f.did("refresh desktop") || f.did("refresh server") || f.did("linger on") || f.did("restart") {
		t.Fatalf("calls %q", f.calls)
	}
}

// A unit an earlier version wrote for start at login on a desktop starts
// at boot without lingering: a plain run from the desktop makes it a
// desktop unit.
func TestLauncherMovesAnOldDesktopUnitToTheGraphicalSession(t *testing.T) {
	f := okService()
	f.installed, f.unitKnown, f.unitDesktop, f.staleUnit = true, true, false, true
	var out strings.Builder
	if rc := runLauncher(&out, f, keyedDir(t), launchOptions{NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if !f.did("refresh desktop") {
		t.Fatalf("calls %q", f.calls)
	}
}

// A Mac reached over ssh is headless for the banner and the browser, but
// its LaunchAgent is a desktop's: no lingering, and it starts at login.
func TestLauncherHeadlessIsNotAServer(t *testing.T) {
	f := okService()
	var out strings.Builder
	if rc := runLauncher(&out, f, keyedDir(t), launchOptions{Headless: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt(pageAt))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if !f.did("write desktop") || f.did("linger on") || !strings.Contains(out.String(), "ssh -L") || !strings.Contains(out.String(), "when you log in") {
		t.Fatalf("calls %q\n%s", f.calls, out.String())
	}
}

// A service control that knows why it cannot be used says so in its own
// words, such as a Windows copy without its windowless program.
type reasonedService struct{ fakeService }

func (r *reasonedService) UnusableLine() string { return "No background program here." }

func TestStartInBackgroundSaysWhyItCannot(t *testing.T) {
	var out strings.Builder
	rc, foreground := startInBackground(&out, &reasonedService{fakeService{usable: false}}, t.TempDir(), launchOptions{}, (&fakeDeps{}).deps(neverAnswering))
	if !foreground || rc != 0 || out.String() != "No background program here.\n" {
		t.Fatalf("rc %d, foreground %v, %q", rc, foreground, out.String())
	}
}
