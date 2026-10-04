//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	saved := runReg
	runReg = func(args ...string) (string, error) {
		f.calls = append(f.calls, strings.Join(args, " "))
		if args[0] == "query" {
			return "    CCBabysitter    REG_SZ    " + want + "\r\n", nil
		}
		return "", nil
	}
	t.Cleanup(func() { runReg = saved })
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
