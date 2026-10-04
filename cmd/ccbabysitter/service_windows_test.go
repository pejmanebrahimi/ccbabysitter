//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

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
// that is refused it asks Explorer, which is outside the job, to start it,
// and checks that it then runs outside the job; when that fails too it
// starts it anyway and says the window still owns it.
func TestStartBackgroundLeavesTheWindowsJob(t *testing.T) {
	type result struct {
		spawns   []bool // whether each direct start asked to break away
		explorer int
		checked  int
		err      error
	}
	run := func(refuseBreakaway, refuseAll, explorerFails, outside bool) result {
		var r result
		savedSpawn, savedExplorer, savedOutside := spawn, viaExplorer, outsideJob
		t.Cleanup(func() { spawn, viaExplorer, outsideJob = savedSpawn, savedExplorer, savedOutside })
		spawn = func(path string, flags uint32) error {
			breakaway := flags&createBreakawayFromJob != 0
			r.spawns = append(r.spawns, breakaway)
			if refuseAll || (breakaway && refuseBreakaway) {
				return errors.New("access denied")
			}
			return nil
		}
		viaExplorer = func(path string) error {
			r.explorer++
			if path != `C:\x\ccbabysitter-background.exe` {
				t.Errorf("Explorer was asked to start %q", path)
			}
			if explorerFails {
				return errors.New("no explorer")
			}
			return nil
		}
		outsideJob = func(path string, since time.Time) bool {
			r.checked++
			return outside
		}
		r.err = startBackground(`C:\x\ccbabysitter-background.exe`)
		return r
	}
	if r := run(false, false, false, true); r.err != nil || len(r.spawns) != 1 || !r.spawns[0] || r.explorer != 0 {
		t.Fatalf("breakaway allowed: %+v", r)
	}
	if r := run(true, false, false, true); r.err != nil || len(r.spawns) != 1 || r.explorer != 1 || r.checked != 1 {
		t.Fatalf("breakaway refused, Explorer started it outside the job: %+v", r)
	}
	if r := run(true, false, false, false); !errors.Is(r.err, errStillOwned) || len(r.spawns) != 2 || r.spawns[1] {
		t.Fatalf("Explorer's start not seen outside the job: %+v", r)
	}
	if r := run(true, false, true, true); !errors.Is(r.err, errStillOwned) || r.checked != 0 || len(r.spawns) != 2 {
		t.Fatalf("Explorer could not be asked: %+v", r)
	}
	if r := run(true, true, true, false); r.err == nil || errors.Is(r.err, errStillOwned) {
		t.Fatalf("nothing could start: %+v", r)
	}
}

// Uninstall forgets that start at login was turned on once, so a later
// plain run turns it on again, as on a machine that never had it.
func TestWindowsUninstallForgetsTheLoginStartChoice(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	windowsProgram(t, true)
	useFakeReg(t)
	if err := state.MarkLoginStartOffered(state.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runUninstall(&out)
	if state.LoginStartOffered(state.DefaultDir()) {
		t.Fatalf("the choice is still remembered after uninstall:\n%s", out.String())
	}
}

// Without its windowless program, as after go install, the launcher names
// the missing program and says it runs only in this terminal.
func TestWindowsUnusableLineNamesTheBackgroundProgram(t *testing.T) {
	line := (windowsControl{}).UnusableLine()
	if !strings.Contains(line, backgroundExe) || !strings.Contains(line, "only while this terminal stays open") {
		t.Fatalf("%q", line)
	}
}

// The process id list QueryInformationJobObject fills in: two counts, then
// the ids, each as wide as a pointer. Only the ids it says it filled in
// count.
func TestParseJobPIDs(t *testing.T) {
	word := int(unsafe.Sizeof(uintptr(0)))
	buf := make([]byte, 8+4*word)
	binary.LittleEndian.PutUint32(buf[0:4], 5) // assigned
	binary.LittleEndian.PutUint32(buf[4:8], 3) // in the list
	for i, pid := range []uint64{100, 200, 300, 400} {
		off := 8 + i*word
		if word == 8 {
			binary.LittleEndian.PutUint64(buf[off:], pid)
		} else {
			binary.LittleEndian.PutUint32(buf[off:], uint32(pid))
		}
	}
	got := parseJobPIDs(buf)
	if len(got) != 3 || !got[100] || !got[200] || !got[300] || got[400] {
		t.Fatalf("%v", got)
	}
	if got := parseJobPIDs(buf[:6]); len(got) != 0 {
		t.Fatalf("a short buffer: %v", got)
	}
}
