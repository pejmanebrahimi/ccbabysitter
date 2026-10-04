//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReg records reg.exe calls and answers the Run value read: absent
// while queryErr is set, value otherwise; readErr makes the read fail.
type fakeReg struct {
	calls    []string
	queryErr error
	value    string
	readErr  error
}

func useFakeReg(t *testing.T) *fakeReg {
	t.Helper()
	f := &fakeReg{queryErr: errors.New("exit status 1")}
	saved := runReg
	runReg = func(args ...string) (string, error) {
		f.calls = append(f.calls, strings.Join(args, " "))
		if args[0] == "query" {
			if f.queryErr != nil {
				return "", f.queryErr
			}
			return `    CCBabysitter    REG_SZ    "C:\x\ccbabysitter-background.exe" --service`, nil
		}
		return "", nil
	}
	savedQuery := queryRunValue
	queryRunValue = func() (string, bool, error) {
		f.calls = append(f.calls, "read")
		if f.readErr != nil {
			return "", false, f.readErr
		}
		if f.queryErr != nil {
			return "", false, nil
		}
		if f.value == "" {
			return `"C:\x\ccbabysitter-background.exe" --service`, true, nil
		}
		return f.value, true, nil
	}
	t.Cleanup(func() { runReg = saved; queryRunValue = savedQuery })
	return f
}

// windowsHome gives the test its own APPDATA and a program folder.
func windowsHome(t *testing.T) string {
	t.Helper()
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	saved := executablePath
	executablePath = func() (string, error) { return `C:\Programs\CCBabysitter\ccbabysitter.exe`, nil }
	t.Cleanup(func() { executablePath = saved })
	return appData
}

func legacyScript(t *testing.T, appData string) string {
	t.Helper()
	path := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "CCBabysitter.cmd")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("@echo off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunValueStartsTheBackgroundProgram(t *testing.T) {
	windowsHome(t)
	want, err := runValueWanted()
	if err != nil || want != `"C:\Programs\CCBabysitter\ccbabysitter-background.exe" --service` {
		t.Fatalf("%q %v", want, err)
	}
}

// Start at login on sets the Run value and removes the Startup script an
// earlier version wrote, which opened a console window at every login.
func TestStartAtLoginOnSetsTheRunValue(t *testing.T) {
	appData := windowsHome(t)
	dir := windowsProgram(t, true)
	script := legacyScript(t, appData)
	f := useFakeReg(t)
	path, err := installAutostartWindows(true)
	if err != nil {
		t.Fatal(err)
	}
	if path != runKey+`\CCBabysitter` {
		t.Fatalf("reported %q", path)
	}
	want := `add ` + runKey + ` /v CCBabysitter /t REG_SZ /d "` + filepath.Join(dir, backgroundExe) + `" --service /f`
	if len(f.calls) != 1 || f.calls[0] != want {
		t.Fatalf("calls %q, want %q", f.calls, want)
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatal("the Startup script is still there")
	}
}

func TestStartAtLoginOffDeletesTheRunValue(t *testing.T) {
	windowsHome(t)
	f := useFakeReg(t)
	if _, err := installAutostartWindows(false); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != "read" {
		t.Fatalf("an absent value was deleted: %q", f.calls)
	}
	f.calls = nil
	f.queryErr = nil
	if _, err := installAutostartWindows(false); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, "; ") != `read; delete `+runKey+` /v CCBabysitter /f` {
		t.Fatalf("calls %q", f.calls)
	}
}

// A Run value that cannot be read is not taken for "off": the error goes
// back, so the saved setting is left alone.
func TestStartAtLoginReadFailureIsAnError(t *testing.T) {
	windowsHome(t)
	f := useFakeReg(t)
	f.readErr = errors.New("access denied")
	if _, err := autostartInstalledWindows(); err == nil {
		t.Fatal("a failed read counted as off")
	}
}

func TestStartAtLoginIsOnWithTheValueOrTheOldScript(t *testing.T) {
	appData := windowsHome(t)
	f := useFakeReg(t)
	if on, err := autostartInstalledWindows(); err != nil || on {
		t.Fatalf("nothing there: %v %v", on, err)
	}
	f.queryErr = nil
	if on, err := autostartInstalledWindows(); err != nil || !on {
		t.Fatalf("the value is there: %v %v", on, err)
	}
	f.queryErr = errors.New("exit status 1")
	legacyScript(t, appData)
	if on, err := autostartInstalledWindows(); err != nil || !on {
		t.Fatalf("the old script is there: %v %v", on, err)
	}
}

// A copy installed with go install has no windowless program: start at
// login is refused, rather than a Run value naming a missing file, and an
// earlier version's working Startup script is kept.
func TestStartAtLoginNeedsTheBackgroundProgram(t *testing.T) {
	appData := windowsHome(t)
	windowsProgram(t, false)
	script := legacyScript(t, appData)
	f := useFakeReg(t)
	if _, err := installAutostartWindows(true); err == nil || !strings.Contains(err.Error(), backgroundExe) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatal("the working Startup script was removed")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "add ") {
			t.Fatalf("a Run value was written: %q", f.calls)
		}
	}
}
