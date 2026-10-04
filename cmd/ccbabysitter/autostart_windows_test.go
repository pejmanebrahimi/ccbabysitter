//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReg records reg.exe calls; query answers with queryErr.
type fakeReg struct {
	calls    []string
	queryErr error
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
	t.Cleanup(func() { runReg = saved })
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
	script := legacyScript(t, appData)
	f := useFakeReg(t)
	path, err := installAutostartWindows(true)
	if err != nil {
		t.Fatal(err)
	}
	if path != runKey+`\CCBabysitter` {
		t.Fatalf("reported %q", path)
	}
	want := `add ` + runKey + ` /v CCBabysitter /t REG_SZ /d "C:\Programs\CCBabysitter\ccbabysitter-background.exe" --service /f`
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
	if len(f.calls) != 1 || f.calls[0] != `delete `+runKey+` /v CCBabysitter /f` {
		t.Fatalf("calls %q", f.calls)
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
