package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

func TestLingerFromShowUser(t *testing.T) {
	for text, want := range map[string]bool{"Linger=yes\n": true, "Linger=no\n": false, "Linger=yes": true} {
		got, err := lingerFromShowUser(text)
		if err != nil || got != want {
			t.Fatalf("%q: got %v, %v", text, got, err)
		}
	}
	for _, bad := range []string{"", "Linger=maybe\n", "Failed to get user\n", "Linger=yes\nLinger=no\n"} {
		if _, err := lingerFromShowUser(bad); err == nil {
			t.Fatalf("%q was read as an answer", bad)
		}
	}
}

// Lingering that was off is turned on and noted, so uninstall turns it
// off again later.
func TestSetupTurnsLingeringOnAndNotesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	var out strings.Builder
	f := &fakeService{usable: true, writeOK: true, startOK: true}
	if rc, _ := startInBackground(&out, f, dir, launchOptions{Headless: true, Server: true, NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt("http://127.0.0.1:47391"))); rc != 0 {
		t.Fatalf("rc %d, out %q", rc, out.String())
	}
	if !f.did("linger on") || !f.lingerOn {
		t.Fatalf("calls %v", f.calls)
	}
	if !state.LingeringTurnedOn(dir) {
		t.Fatal("CC Babysitter turned lingering on, which uninstall needs to know")
	}

	// Every later run finds it on, leaves it alone and keeps the note.
	f.calls = nil
	if rc, _ := startInBackground(&out, &fakeService{usable: true, installed: true, active: true, enableOK: true, refreshOK: true, startOK: true, lingerOn: true}, dir, launchOptions{Headless: true, Server: true, NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt("http://127.0.0.1:47391"))); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if !state.LingeringTurnedOn(dir) {
		t.Fatal("the note was lost on a later run")
	}
}

// Lingering that was on before CC Babysitter came is the user's own: it is
// not changed, and not noted, so uninstall leaves it on.
func TestSetupLeavesLingeringThatWasOnAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	var out strings.Builder
	f := &fakeService{usable: true, installed: true, enableOK: true, refreshOK: true, startOK: true, lingerOn: true}
	if rc := runLauncher(&out, f, dir, launchOptions{Install: true, NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt("http://127.0.0.1:47391"))); rc != 0 {
		t.Fatalf("rc %d, out %q", rc, out.String())
	}
	if f.did("linger on") || f.did("linger off") {
		t.Fatalf("calls %v", f.calls)
	}
	if state.LingeringTurnedOn(dir) {
		t.Fatal("lingering the user had on was noted as CC Babysitter's")
	}
}

// When whether lingering is on cannot be found out, it is still turned on
// for the service, but not noted, since it may have been on already.
func TestSetupDoesNotNoteLingeringItCouldNotAskAbout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	var out strings.Builder
	f := &fakeService{usable: true, writeOK: true, startOK: true, lingerErr: errors.New("exit status 1")}
	if rc, _ := startInBackground(&out, f, dir, launchOptions{Headless: true, Server: true, NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt("http://127.0.0.1:47391"))); rc != 0 {
		t.Fatalf("rc %d, out %q", rc, out.String())
	}
	if !f.did("linger on") {
		t.Fatalf("calls %v", f.calls)
	}
	if state.LingeringTurnedOn(dir) {
		t.Fatal("lingering that may have been on already was noted as CC Babysitter's")
	}
}

// Turning lingering on can need rights the user does not have. That is
// said with the command to run, nothing is noted, and the setup still
// succeeds.
func TestSetupReportsLingeringItCouldNotTurnOn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	var out strings.Builder
	f := &fakeService{usable: true, writeOK: true, startOK: true, setLingerErr: errors.New("exit status 1")}
	rc, _ := startInBackground(&out, f, dir, launchOptions{Headless: true, Server: true, NoOpen: true}, (&fakeDeps{view: desktopView()}).deps(answeringAt("http://127.0.0.1:47391")))
	if rc != 0 {
		t.Fatalf("rc %d, out %q", rc, out.String())
	}
	user := currentUser()
	want := "loginctl enable-linger " + user + " failed. Run it yourself to keep the service running after you log out:\n" +
		"  sudo loginctl enable-linger " + user + "\n" +
		"Everything else is set up.\n"
	if !strings.HasPrefix(out.String(), want) {
		t.Fatalf("got\n%s", out.String())
	}
	if state.LingeringTurnedOn(dir) {
		t.Fatal("lingering that was not turned on was noted")
	}
}

func TestUninstallTurnsOffLingeringItTurnedOn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	if err := state.MarkLingeringTurnedOn(dir); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	f := &fakeService{lingerOn: true}
	releaseLingering(&out, f, dir, "alice")
	if f.lingerOn || !f.did("linger off") {
		t.Fatalf("calls %v", f.calls)
	}
	if out.String() != "Turned lingering off for alice, which CC Babysitter had turned on.\n" {
		t.Fatalf("got %q", out.String())
	}
	if state.LingeringTurnedOn(dir) {
		t.Fatal("the note is gone once lingering is off again")
	}
}

// Lingering CC Babysitter did not turn on may be keeping other services
// of the user's own running, so uninstall leaves it as it was.
func TestUninstallLeavesOtherLingeringAlone(t *testing.T) {
	var out strings.Builder
	f := &fakeService{lingerOn: true}
	releaseLingering(&out, f, filepath.Join(t.TempDir(), "ccbabysitter"), "alice")
	if !f.lingerOn || len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
	if out.String() != "Left lingering for alice as it was, since CC Babysitter has no note that it turned it on.\n" {
		t.Fatalf("got %q", out.String())
	}
}

// A failure to turn it off says how, and keeps the note so a later
// uninstall tries again.
func TestUninstallReportsLingeringItCouldNotTurnOff(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ccbabysitter")
	if err := state.MarkLingeringTurnedOn(dir); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	f := &fakeService{lingerOn: true, setLingerErr: errors.New("exit status 1")}
	releaseLingering(&out, f, dir, "alice")
	want := "loginctl disable-linger alice failed. Run it yourself if you no longer want lingering:\n" +
		"  loginctl disable-linger alice\n"
	if out.String() != want {
		t.Fatalf("got %q", out.String())
	}
	if !state.LingeringTurnedOn(dir) {
		t.Fatal("the note was dropped although lingering is still on")
	}
}
