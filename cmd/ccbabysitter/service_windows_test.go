//go:build windows

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// windowsProgram puts the program and, when background is set, the
// windowless one in a temp folder of their own.
func windowsProgram(t *testing.T, background bool) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ccbabysitter.exe")
	saved := executablePath
	executablePath = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executablePath = saved })
	if background {
		if err := os.WriteFile(filepath.Join(dir, backgroundExe), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWindowsUsableNeedsTheBackgroundProgram(t *testing.T) {
	windowsProgram(t, false)
	if (windowsControl{}).Usable() {
		t.Fatal("usable without the windowless program")
	}
	windowsProgram(t, true)
	if !(windowsControl{}).Usable() {
		t.Fatal("not usable beside the windowless program")
	}
}

func TestWindowsStartRunsTheBackgroundProgram(t *testing.T) {
	dir := windowsProgram(t, true)
	var started []string
	saved := startBackground
	startBackground = func(path string) error { started = append(started, path); return nil }
	t.Cleanup(func() { startBackground = saved })
	var out strings.Builder
	if !(windowsControl{}).Start(&out, true) {
		t.Fatal(out.String())
	}
	if len(started) != 1 || started[0] != filepath.Join(dir, backgroundExe) {
		t.Fatalf("started %q", started)
	}
}

// An earlier version's Startup script means start at login was on: it is
// replaced by the Run value, so login start stays on without a window.
func TestWindowsRefreshReplacesTheOldScript(t *testing.T) {
	windowsProgram(t, true)
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	script := legacyScript(t, appData)
	f := useFakeReg(t)
	var out strings.Builder
	rewritten, ok := (windowsControl{}).RefreshUnit(&out, true)
	if !rewritten || !ok {
		t.Fatalf("rewritten %v ok %v %s", rewritten, ok, out.String())
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatal("the old script is still there")
	}
	if len(f.calls) == 0 || !strings.HasPrefix(f.calls[len(f.calls)-1], "add "+runKey) {
		t.Fatalf("calls %q", f.calls)
	}
}

func TestWindowsRefreshLeavesARightValueAlone(t *testing.T) {
	windowsProgram(t, true)
	t.Setenv("APPDATA", t.TempDir())
	f := useFakeReg(t)
	want, _ := runValueWanted()
	f.queryErr = nil
	f.value = want
	var out strings.Builder
	if rewritten, ok := (windowsControl{}).RefreshUnit(&out, true); rewritten || !ok {
		t.Fatalf("rewritten %v ok %v", rewritten, ok)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "add ") {
			t.Fatalf("a right value was written again: %q", f.calls)
		}
	}
}

// A Run value naming a program that has moved is written again.
func TestWindowsRefreshFollowsAMovedProgram(t *testing.T) {
	windowsProgram(t, true)
	t.Setenv("APPDATA", t.TempDir())
	f := useFakeReg(t)
	f.queryErr = nil
	var out strings.Builder
	if rewritten, ok := (windowsControl{}).RefreshUnit(&out, true); !rewritten || !ok {
		t.Fatalf("rewritten %v ok %v", rewritten, ok)
	}
}

// After a reboot the saved page address may be stale, and another
// account's server may listen on its port by now: uninstall sends the key
// nowhere unless a live copy holds the state folder's lock.
func TestWindowsUninstallSendsNoKeyToAStaleAddress(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	windowsProgram(t, true)
	useFakeReg(t)
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String()+" "+r.Header.Get("Authorization"))
	}))
	t.Cleanup(ts.Close)
	dir := state.DefaultDir()
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	// A lock left by a copy that is gone.
	if err := os.WriteFile(filepath.Join(dir, "ccbabysitter.lock"), []byte("999999 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runUninstall(&out)
	if len(seen) != 0 {
		t.Fatalf("the stale address was sent %q", seen)
	}
}

// A Run value that cannot be read is left alone, and nothing is restarted.
func TestWindowsRefreshLeavesAnUnreadableValueAlone(t *testing.T) {
	windowsProgram(t, true)
	t.Setenv("APPDATA", t.TempDir())
	f := useFakeReg(t)
	f.readErr = errors.New("access denied")
	var out strings.Builder
	if rewritten, ok := (windowsControl{}).RefreshUnit(&out, true); rewritten || !ok {
		t.Fatalf("rewritten %v ok %v", rewritten, ok)
	}
}

// The old Startup script goes only once the Run value is written, so a
// failed write never leaves start at login off.
func TestWindowsRefreshKeepsTheOldScriptWhenTheValueCannotBeWritten(t *testing.T) {
	windowsProgram(t, true)
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	script := legacyScript(t, appData)
	useFakeReg(t)
	saved := runReg
	runReg = func(args ...string) (string, error) { return "ERROR: Access is denied.", errors.New("exit status 1") }
	t.Cleanup(func() { runReg = saved })
	var out strings.Builder
	if _, ok := (windowsControl{}).RefreshUnit(&out, true); ok {
		t.Fatal("a failed write counted as done")
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatal("the old script was removed though the Run value was not written")
	}
}

// Starting the windowless program tries to leave the launcher's job; when
// that is refused it starts as Explorer's child; when that is refused too
// it starts anyway and says the window still owns it.
func TestStartBackgroundLeavesTheWindowsJob(t *testing.T) {
	type try struct {
		breakaway bool
		parent    bool
	}
	run := func(refuse func(try) bool, explorerOK bool) ([]try, error) {
		var tries []try
		savedSpawn, savedExplorer := spawn, explorerHandle
		t.Cleanup(func() { spawn, explorerHandle = savedSpawn, savedExplorer })
		spawn = func(path string, flags uint32, parent syscall.Handle) error {
			tr := try{flags&createBreakawayFromJob != 0, parent != 0}
			tries = append(tries, tr)
			if refuse(tr) {
				return errors.New("access denied")
			}
			return nil
		}
		explorerHandle = func() (syscall.Handle, func(), error) {
			if !explorerOK {
				return 0, nil, errors.New("no explorer")
			}
			return syscall.Handle(42), func() {}, nil
		}
		return tries, startBackground(`C:\x\ccbabysitter-background.exe`)
	}
	tries, err := run(func(try) bool { return false }, true)
	if err != nil || len(tries) != 1 || !tries[0].breakaway {
		t.Fatalf("breakaway allowed: %v %+v", err, tries)
	}
	tries, err = run(func(tr try) bool { return tr.breakaway }, true)
	if err != nil || len(tries) != 2 || !tries[1].parent {
		t.Fatalf("breakaway refused: %v %+v", err, tries)
	}
	tries, err = run(func(tr try) bool { return tr.breakaway || tr.parent }, true)
	if !errors.Is(err, errStillOwned) || len(tries) != 3 {
		t.Fatalf("both refused: %v %+v", err, tries)
	}
	if _, err = run(func(try) bool { return true }, false); err == nil || errors.Is(err, errStillOwned) {
		t.Fatalf("nothing could start: %v", err)
	}
}
